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

	// Create perf reader
	rd, err := perf.NewReader(objs.FlowEvents, os.Getpagesize())
	if err != nil {
		log.Fatalf("Failed to create perf reader: %v", err)
	}
	defer rd.Close()

	// Start reading events
	log.Println("Starting flow observer...")
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
			Timestamp uint64
		}

		if err := binary.Read(bytes.NewReader(record.RawSample), binary.LittleEndian, &flow); err != nil {
			log.Printf("Failed to parse flow data: %v", err)
			continue
		}

		// Convert IPs to string format
		srcIP := fmt.Sprintf("%d.%d.%d.%d",
			byte(flow.SrcIP>>24), byte(flow.SrcIP>>16),
			byte(flow.SrcIP>>8), byte(flow.SrcIP))
		dstIP := fmt.Sprintf("%d.%d.%d.%d",
			byte(flow.DstIP>>24), byte(flow.DstIP>>16),
			byte(flow.DstIP>>8), byte(flow.DstIP))

		// Get pod info
		srcPod := podWatcher.GetPodInfo(srcIP)
		dstPod := podWatcher.GetPodInfo(dstIP)

		// Create flow event
		event := &pb.Flow{
			SourceIp:        srcIP,
			DestinationIp:   dstIP,
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

		// Send event to gRPC clients
		select {
		case flowChan <- event:
		default:
			log.Println("Flow channel full, dropping event")
		}
	}
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
