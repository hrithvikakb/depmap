package main

import (
	"bytes"
	"context"
	"encoding/binary"
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

	"hubbleclone/pkg/k8s"
	pb "hubbleclone/pkg/proto/flow"
)

// flowEvent matches the C struct in the eBPF program
type flowEvent struct {
	SrcIP     uint32
	DstIP     uint32
	SrcPort   uint16
	DstPort   uint16
	Protocol  uint8
	Verdict   uint8
	Timestamp uint64
}

type flowServer struct {
	pb.UnimplementedFlowServiceServer
	flowChan chan *pb.Flow
}

func newFlowServer() *flowServer {
	return &flowServer{
		flowChan: make(chan *pb.Flow, 1000), // Buffer size of 1000 flows
	}
}

func (s *flowServer) GetFlows(req *pb.GetFlowsRequest, stream pb.FlowService_GetFlowsServer) error {
	for flow := range s.flowChan {
		if err := stream.Send(flow); err != nil {
			return err
		}
	}
	return nil
}

func (s *flowServer) SendFlow(flow *pb.Flow) {
	select {
	case s.flowChan <- flow:
		// Flow sent successfully
	default:
		// Channel is full, drop the flow
		log.Printf("Flow channel is full, dropping flow")
	}
}

func main() {
	// Start gRPC server
	grpcServer := grpc.NewServer()
	flowServer := newFlowServer()
	pb.RegisterFlowServiceServer(grpcServer, flowServer)

	lis, err := net.Listen("tcp", ":50051")
	if err != nil {
		log.Fatalf("Failed to listen: %v", err)
	}

	go func() {
		if err := grpcServer.Serve(lis); err != nil {
			log.Fatalf("Failed to serve: %v", err)
		}
	}()

	// Load eBPF program
	spec, err := ebpf.LoadCollectionSpec("pkg/bpf/flow_observer.o")
	if err != nil {
		log.Fatalf("Failed to load eBPF spec: %v", err)
	}

	var objs struct {
		FlowObserver *ebpf.Program `ebpf:"tc_flow_observer"`
		Events       *ebpf.Map     `ebpf:"events"`
	}

	if err := spec.LoadAndAssign(&objs, nil); err != nil {
		log.Fatalf("Failed to load eBPF objects: %v", err)
	}
	defer objs.Events.Close()
	defer objs.FlowObserver.Close()

	// Set up Kubernetes client
	config, err := rest.InClusterConfig()
	if err != nil {
		log.Fatalf("Failed to get Kubernetes config: %v", err)
	}

	clientset, err := kubernetes.NewForConfig(config)
	if err != nil {
		log.Fatalf("Failed to create Kubernetes client: %v", err)
	}

	// Create pod watcher
	podWatcher := k8s.NewPodWatcher(clientset)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	if err := podWatcher.Start(ctx); err != nil {
		log.Fatalf("Failed to start pod watcher: %v", err)
	}

	// Set up perf reader
	rd, err := perf.NewReader(objs.Events, os.Getpagesize())
	if err != nil {
		log.Fatalf("Failed to create perf reader: %v", err)
	}
	defer rd.Close()

	// Handle signals
	sig := make(chan os.Signal, 1)
	signal.Notify(sig, syscall.SIGINT, syscall.SIGTERM)

	// Process events
	go func() {
		for {
			record, err := rd.Read()
			if err != nil {
				if err == perf.ErrClosed {
					return
				}
				log.Printf("Error reading perf event: %v", err)
				continue
			}

			if record.LostSamples != 0 {
				log.Printf("Lost %d samples", record.LostSamples)
				continue
			}

			var event flowEvent
			if err := binary.Read(bytes.NewReader(record.RawSample), binary.LittleEndian, &event); err != nil {
				log.Printf("Failed to parse flow event: %v", err)
				continue
			}

			srcIP := net.IP(make([]byte, 4))
			dstIP := net.IP(make([]byte, 4))
			binary.LittleEndian.PutUint32(srcIP, event.SrcIP)
			binary.LittleEndian.PutUint32(dstIP, event.DstIP)

			srcPod, srcExists := podWatcher.GetPodInfo(srcIP.String())
			dstPod, dstExists := podWatcher.GetPodInfo(dstIP.String())

			if !srcExists || !dstExists {
				continue // Skip if we can't map IPs to pods
			}

			flow := &pb.Flow{
				SourcePod:            srcPod.Name,
				SourceNamespace:      srcPod.Namespace,
				DestinationPod:       dstPod.Name,
				DestinationNamespace: dstPod.Namespace,
				Protocol:             protocolToString(event.Protocol),
				SourcePort:           uint32(event.SrcPort),
				DestinationPort:      uint32(event.DstPort),
				Verdict:              verdictToString(event.Verdict),
				Time:                 int64(event.Timestamp),
				SourceIp:             srcIP.String(),
				DestinationIp:        dstIP.String(),
			}

			// Send flow to gRPC server
			flowServer.SendFlow(flow)
		}
	}()

	<-sig
	log.Println("Shutting down...")
	grpcServer.GracefulStop()
}

func protocolToString(proto uint8) string {
	switch proto {
	case syscall.IPPROTO_TCP:
		return "TCP"
	case syscall.IPPROTO_UDP:
		return "UDP"
	default:
		return fmt.Sprintf("UNKNOWN(%d)", proto)
	}
}

func verdictToString(verdict uint8) string {
	switch verdict {
	case 0:
		return "FORWARDED"
	case 1:
		return "DROPPED"
	default:
		return fmt.Sprintf("UNKNOWN(%d)", verdict)
	}
}
