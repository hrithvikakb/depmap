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
	flowChan := make(chan *pb.Flow, 1000)
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

	// Attach eBPF program to all veth interfaces
	if err := attachTCProgram(objs.FlowObserver, "veth"); err != nil {
		log.Fatalf("Failed to attach TC program: %v", err)
	}

	// Create perf reader with larger buffer
	bufferSize := os.Getpagesize() * 16 // Use 16 pages for buffer
	rd, err := perf.NewReader(objs.FlowEvents, bufferSize)
	if err != nil {
		log.Fatalf("Failed to create perf reader: %v", err)
	}
	defer rd.Close()

	// Start reading events
	log.Printf("Starting flow observer with perf buffer size: %d bytes", bufferSize)
	for {
		select {
		case <-ctx.Done():
			return
		default:
		}

		record, err := rd.Read()
		if err != nil {
			if err == perf.ErrClosed {
				return
			}
			log.Printf("Error reading perf event: %v", err)
			continue
		}

		// Parse flow data
		var flow struct {
			SrcIP     uint32
			DstIP     uint32
			SrcPort   uint16
			DstPort   uint16
			Protocol  uint8
			Verdict   uint8
			_         uint16 // padding to align Timestamp
			Timestamp uint64
		}

		log.Printf("DEBUG: Perf event details: raw_size=%d, lost_samples=%d, expected_struct_size=%d",
			len(record.RawSample), record.LostSamples, binary.Size(flow))

		if len(record.RawSample) == 0 {
			log.Printf("ERROR: Empty perf event received, skipping")
			continue
		}

		if len(record.RawSample) != binary.Size(flow) {
			log.Printf("WARNING: Unexpected raw sample size: got=%d, want=%d",
				len(record.RawSample), binary.Size(flow))
			log.Printf("DEBUG: Raw bytes (hex): % x", record.RawSample)
			continue
		}

		if err := binary.Read(bytes.NewReader(record.RawSample), binary.LittleEndian, &flow); err != nil {
			log.Printf("Failed to parse flow data: raw_size=%d, expected_size=%d, error=%v",
				len(record.RawSample), binary.Size(flow), err)
			// Dump raw bytes for debugging
			log.Printf("Raw bytes: %v", record.RawSample)
			continue
		}

		// Convert IPs to string format
		srcIP := net.IP(make([]byte, 4))
		dstIP := net.IP(make([]byte, 4))
		binary.BigEndian.PutUint32(srcIP, flow.SrcIP)
		binary.BigEndian.PutUint32(dstIP, flow.DstIP)

		log.Printf("Parsed flow: src=%s:%d dst=%s:%d proto=%d verdict=%d timestamp=%d",
			srcIP.String(), flow.SrcPort, dstIP.String(), flow.DstPort, flow.Protocol, flow.Verdict, flow.Timestamp)

		// Get pod info
		srcPod := podWatcher.GetPodInfo(srcIP.String())
		dstPod := podWatcher.GetPodInfo(dstIP.String())

		// Create flow event
		event := &pb.Flow{
			SourceIp:        srcIP.String(),
			DestinationIp:   dstIP.String(),
			SourcePort:      uint32(flow.SrcPort),
			DestinationPort: uint32(flow.DstPort),
			L4Protocol:      getProtocolName(flow.Protocol),
			Time:            int64(flow.Timestamp),
			Verdict:         getVerdictString(flow.Verdict),
		}

		// Add pod metadata if available
		if srcPod != nil {
			event.SourcePod = srcPod.Name
			event.SourceNamespace = srcPod.Namespace
		}
		if dstPod != nil {
			event.DestinationPod = dstPod.Name
			event.DestinationNamespace = dstPod.Namespace
		}

		// Send flow to channel
		select {
		case flowChan <- event:
		default:
			log.Printf("Flow channel full, dropping event")
		}
	}
}

func attachTCProgram(prog *ebpf.Program, ifacePrefix string) error {
	links, err := netlink.LinkList()
	if err != nil {
		return fmt.Errorf("failed to list network interfaces: %w", err)
	}

	for _, link := range links {
		if _, ok := link.(*netlink.Veth); ok {
			if err := attachQdisc(link); err != nil {
				log.Printf("Failed to attach qdisc to %s: %v. Continuing...", link.Attrs().Name, err)
				continue
			}

			if err := attachFilter(prog, link, netlink.HANDLE_MIN_EGRESS, "egress"); err != nil {
				log.Printf("Failed to attach egress filter to %s: %v", link.Attrs().Name, err)
			} else {
				log.Printf("Attached egress TC program to %s", link.Attrs().Name)
			}

			if err := attachFilter(prog, link, netlink.HANDLE_MIN_INGRESS, "ingress"); err != nil {
				log.Printf("Failed to attach ingress filter to %s: %v", link.Attrs().Name, err)
			} else {
				log.Printf("Attached ingress TC program to %s", link.Attrs().Name)
			}
		}
	}
	return nil
}

func attachQdisc(link netlink.Link) error {
	qdisc := &netlink.GenericQdisc{
		QdiscAttrs: netlink.QdiscAttrs{
			LinkIndex: link.Attrs().Index,
			Handle:    netlink.MakeHandle(0xffff, 0),
			Parent:    netlink.HANDLE_CLSACT,
		},
		QdiscType: "clsact",
	}

	if err := netlink.QdiscReplace(qdisc); err != nil {
		return fmt.Errorf("could not replace qdisc: %w", err)
	}
	return nil
}

func attachFilter(prog *ebpf.Program, link netlink.Link, parent uint32, direction string) error {
	filter := &netlink.BpfFilter{
		FilterAttrs: netlink.FilterAttrs{
			LinkIndex: link.Attrs().Index,
			Parent:    parent,
			Handle:    1,
			Protocol:  unix.ETH_P_ALL,
		},
		Fd:           prog.FD(),
		Name:         fmt.Sprintf("flow-observer-%s", direction),
		DirectAction: true,
	}

	if err := netlink.FilterReplace(filter); err != nil {
		return fmt.Errorf("could not replace filter: %w", err)
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
