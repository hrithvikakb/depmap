//go:build linux

package main

import (
	"bytes"
	"context"
	"encoding/binary"
	"flag"
	"fmt"
	"log"
	"net"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/cilium/ebpf"
	"github.com/cilium/ebpf/perf"
	"github.com/vishvananda/netlink"
	"golang.org/x/sys/unix"
	"google.golang.org/grpc"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/rest"
	"k8s.io/client-go/tools/clientcmd"

	"hubbleclone/pkg/k8s"
	pb "hubbleclone/proto/flow"
)

var (
	kubeconfig = flag.String("kubeconfig", "", "Path to kubeconfig file")
	grpcPort   = flag.String("grpc-port", "4245", "gRPC server port")
)

// flowServer implements the FlowService gRPC server
type flowServer struct {
	pb.UnimplementedFlowServiceServer
	flowChan chan *pb.Flow
}

func (s *flowServer) GetFlows(req *pb.GetFlowsRequest, stream pb.FlowService_GetFlowsServer) error {
	for flow := range s.flowChan {
		// Apply filters if specified
		if req.Namespace != "" && flow.SourceNamespace != req.Namespace && flow.DestinationNamespace != req.Namespace {
			continue
		}
		if req.Verdict != "" && flow.Verdict != req.Verdict {
			continue
		}

		if err := stream.Send(flow); err != nil {
			return err
		}
	}
	return nil
}

func main() {
	flag.Parse()

	// Create context that's canceled on SIGTERM
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	// Handle shutdown gracefully
	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, syscall.SIGTERM, syscall.SIGINT)
	go func() {
		<-sigCh
		cancel()
	}()

	// Initialize Kubernetes client
	var config *rest.Config
	var err error
	if *kubeconfig != "" {
		config, err = clientcmd.BuildConfigFromFlags("", *kubeconfig)
	} else {
		config, err = rest.InClusterConfig()
	}
	if err != nil {
		log.Fatalf("Failed to get Kubernetes config: %v", err)
	}

	clientset, err := kubernetes.NewForConfig(config)
	if err != nil {
		log.Fatalf("Failed to create Kubernetes client: %v", err)
	}

	// Start pod watcher
	podWatcher := k8s.NewPodWatcher(clientset)
	if err := podWatcher.Start(ctx); err != nil {
		log.Fatalf("Failed to start pod watcher: %v", err)
	}

	// Create flow channel for gRPC server
	flowChan := make(chan *pb.Flow, 10000)
	defer close(flowChan)

	// Start gRPC server
	lis, err := net.Listen("tcp", fmt.Sprintf("0.0.0.0:%s", *grpcPort))
	if err != nil {
		log.Fatalf("Failed to listen: %v", err)
	}
	log.Printf("Starting gRPC server on 0.0.0.0:%s", *grpcPort)
	grpcServer := grpc.NewServer()
	pb.RegisterFlowServiceServer(grpcServer, &flowServer{flowChan: flowChan})
	go func() {
		if err := grpcServer.Serve(lis); err != nil {
			log.Printf("Failed to serve: %v", err)
		}
	}()
	defer grpcServer.GracefulStop()

	// Get eBPF object path from environment variable or use default
	bpfObjectPath := os.Getenv("BPF_OBJECT_PATH")
	if bpfObjectPath == "" {
		bpfObjectPath = "bpf/flow_observer.o"
	}

	// Load pre-compiled eBPF program
	spec, err := ebpf.LoadCollectionSpec(bpfObjectPath)
	if err != nil {
		log.Fatalf("Failed to load eBPF spec: %v", err)
	}

	var objs struct {
		FlowObserver *ebpf.Program `ebpf:"flow_observer"`
		FlowEvents   *ebpf.Map     `ebpf:"flow_events"`
	}

	if err := spec.LoadAndAssign(&objs, nil); err != nil {
		log.Fatalf("Failed to load eBPF objects: %v", err)
	}
	defer objs.FlowEvents.Close()
	defer objs.FlowObserver.Close()

	// Get interface to monitor from environment variable
	monitorInterface := os.Getenv("MONITOR_INTERFACE")
	if monitorInterface == "" {
		monitorInterface = "cni0" // Default to cni0
	}

	log.Printf("Attaching eBPF program to interface: %s", monitorInterface)

	// Check if interface exists
	link, err := netlink.LinkByName(monitorInterface)
	if err != nil {
		log.Printf("Available interfaces:")
		links, _ := netlink.LinkList()
		for _, l := range links {
			log.Printf("  - %s (type: %s)", l.Attrs().Name, l.Type())
		}
		log.Fatalf("Failed to find interface %s: %v", monitorInterface, err)
	}

	// Attach eBPF program to the specified interface
	if err := attachTCProgram(objs.FlowObserver, link); err != nil {
		log.Fatalf("Failed to attach TC program: %v", err)
	}
	log.Printf("Successfully attached eBPF program to interface %s", monitorInterface)

	// Create perf reader with larger buffer
	bufferSize := os.Getpagesize() * 16 // Use 16 pages for buffer
	rd, err := perf.NewReader(objs.FlowEvents, bufferSize)
	if err != nil {
		log.Fatalf("Failed to create perf reader: %v", err)
	}
	defer rd.Close()

	// Start reading events
	log.Printf("Starting flow observer with perf buffer size: %d bytes", bufferSize)

	// Create error channel for monitoring read errors
	errChan := make(chan error, 1)

	// Start event processing in a goroutine
	go func() {
		consecutiveErrors := 0
		maxConsecutiveErrors := 5
		backoffDuration := time.Second

		for {
			select {
			case <-ctx.Done():
				return
			default:
			}

			record, err := rd.Read()
			if err != nil {
				if err == perf.ErrClosed {
					errChan <- fmt.Errorf("perf reader closed unexpectedly")
					return
				}

				consecutiveErrors++
				log.Printf("Error reading perf event (%d/%d): %v", consecutiveErrors, maxConsecutiveErrors, err)

				if consecutiveErrors >= maxConsecutiveErrors {
					errChan <- fmt.Errorf("too many consecutive read errors: %v", err)
					return
				}

				// Apply backoff
				time.Sleep(backoffDuration)
				backoffDuration *= 2 // Exponential backoff
				if backoffDuration > 30*time.Second {
					backoffDuration = 30 * time.Second
				}
				continue
			}

			// Reset error counters on successful read
			consecutiveErrors = 0
			backoffDuration = time.Second

			// flow struct must be defined before use in logging
			var flow struct {
				Timestamp uint64
				SrcIP     uint32
				DstIP     uint32
				SrcPort   uint16
				DstPort   uint16
				Protocol  uint8
				Verdict   uint8
			}

			// Add detailed logging for raw data
			if len(record.RawSample) < binary.Size(flow) {
				log.Printf("ERROR: Truncated perf event received: got %d bytes, want %d bytes. Raw data (hex): %x",
					len(record.RawSample), binary.Size(flow), record.RawSample)
				continue
			}

			// Parse flow data
			if err := binary.Read(bytes.NewReader(record.RawSample), binary.LittleEndian, &flow); err != nil {
				log.Printf("Failed to parse flow data: %v", err)
				continue
			}

			// Convert IPs to string format
			srcIP := net.IP(make([]byte, 4))
			dstIP := net.IP(make([]byte, 4))
			binary.LittleEndian.PutUint32(srcIP, flow.SrcIP)
			binary.LittleEndian.PutUint32(dstIP, flow.DstIP)

			// Get pod information for source and destination IPs
			srcPod := podWatcher.GetPodInfo(srcIP.String())
			dstPod := podWatcher.GetPodInfo(dstIP.String())

			// Create flow event with basic network information
			flowEvent := &pb.Flow{
				Time:            int64(flow.Timestamp),
				SourceIp:        srcIP.String(),
				DestinationIp:   dstIP.String(),
				SourcePort:      uint32(flow.SrcPort),
				DestinationPort: uint32(flow.DstPort),
				L4Protocol:      getProtocolName(flow.Protocol),
				Verdict:         getVerdictString(flow.Verdict),
			}

			// Add pod information if available
			if srcPod != nil {
				flowEvent.SourcePod = srcPod.Name
				flowEvent.SourceNamespace = srcPod.Namespace
				log.Printf("Source pod found: %s/%s", srcPod.Namespace, srcPod.Name)
			} else {
				log.Printf("No pod found for source IP: %s", srcIP.String())
			}

			if dstPod != nil {
				flowEvent.DestinationPod = dstPod.Name
				flowEvent.DestinationNamespace = dstPod.Namespace
				log.Printf("Destination pod found: %s/%s", dstPod.Namespace, dstPod.Name)
			} else {
				log.Printf("No pod found for destination IP: %s", dstIP.String())
			}

			// Send flow event with timeout
			select {
			case flowChan <- flowEvent:
				// Successfully sent
			case <-time.After(time.Second):
				log.Printf("Warning: Flow channel is full, dropping event")
			}
		}
	}()

	// Monitor for errors
	select {
	case err := <-errChan:
		log.Fatalf("Fatal error in event processing: %v", err)
	case <-ctx.Done():
		log.Println("Shutting down flow observer")
	}
}

// attachTCProgram attaches the eBPF program to the specified network interface
func attachTCProgram(prog *ebpf.Program, link netlink.Link) error {
	// Attach qdisc first
	if err := attachQdisc(link); err != nil {
		return fmt.Errorf("failed to attach qdisc: %v", err)
	}

	// Attach filter for ingress
	if err := attachFilter(prog, link, netlink.HANDLE_MIN_INGRESS, "ingress"); err != nil {
		return fmt.Errorf("failed to attach ingress filter: %v", err)
	}

	// Attach filter for egress
	if err := attachFilter(prog, link, netlink.HANDLE_MIN_EGRESS, "egress"); err != nil {
		return fmt.Errorf("failed to attach egress filter: %v", err)
	}

	return nil
}

// attachQdisc attaches the necessary qdisc to the interface
func attachQdisc(link netlink.Link) error {
	qdisc := &netlink.GenericQdisc{
		QdiscAttrs: netlink.QdiscAttrs{
			LinkIndex: link.Attrs().Index,
			Handle:    netlink.MakeHandle(0xffff, 0),
			Parent:    netlink.HANDLE_CLSACT,
		},
		QdiscType: "clsact",
	}

	// Try to delete existing qdisc first (ignore errors)
	_ = netlink.QdiscDel(qdisc)

	// Replace qdisc
	if err := netlink.QdiscReplace(qdisc); err != nil {
		return fmt.Errorf("could not replace qdisc: %w", err)
	}
	return nil
}

// attachFilter attaches a TC filter with the eBPF program
func attachFilter(prog *ebpf.Program, link netlink.Link, parent uint32, direction string) error {
	filter := &netlink.BpfFilter{
		FilterAttrs: netlink.FilterAttrs{
			LinkIndex: link.Attrs().Index,
			Parent:    parent,
			Handle:    1,
			Protocol:  unix.ETH_P_ALL,
			Priority:  1,
		},
		Fd:           prog.FD(),
		Name:         fmt.Sprintf("flow-observer-%s", direction),
		DirectAction: true,
	}

	// Try to delete existing filter first (ignore errors)
	_ = netlink.FilterDel(filter)

	// Replace filter
	if err := netlink.FilterReplace(filter); err != nil {
		return fmt.Errorf("could not replace filter for %s: %w", direction, err)
	}
	return nil
}

func getProtocolName(proto uint8) string {
	switch proto {
	case syscall.IPPROTO_TCP:
		return "TCP"
	case syscall.IPPROTO_UDP:
		return "UDP"
	default:
		return fmt.Sprintf("UNKNOWN(%d)", proto)
	}
}

func getVerdictString(verdict uint8) string {
	if verdict == 0 {
		return "DROPPED"
	}
	return "FORWARDED"
}
