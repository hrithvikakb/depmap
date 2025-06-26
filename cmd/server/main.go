package main

import (
	"flag"
	"fmt"
	"log"
	"net"

	pb "hubbleclone/proto/flow"

	"google.golang.org/grpc"
)

var (
	port = flag.Int("port", 50051, "The server port")
)

// flowServer implements the FlowService gRPC service
type flowServer struct {
	pb.UnimplementedFlowServiceServer
	flowChan chan *pb.Flow
}

// newFlowServer creates a new flow server
func newFlowServer() *flowServer {
	return &flowServer{
		flowChan: make(chan *pb.Flow, 1000), // Buffer 1000 flows
	}
}

// GetFlows implements the GetFlows RPC method
func (s *flowServer) GetFlows(req *pb.GetFlowsRequest, stream pb.FlowService_GetFlowsServer) error {
	log.Printf("New client connected, streaming flows...")

	for {
		select {
		case <-stream.Context().Done():
			return nil
		case flow := <-s.flowChan:
			// Apply filters if specified in request
			if req.Namespace != "" && flow.SourceNamespace != req.Namespace && flow.DestinationNamespace != req.Namespace {
				continue
			}
			if req.Verdict != "" && flow.Verdict != req.Verdict {
				continue
			}

			if err := stream.Send(flow); err != nil {
				return fmt.Errorf("failed to send flow: %v", err)
			}
		}
	}
}

// SendFlow sends a flow event to all connected clients
func (s *flowServer) SendFlow(flow *pb.Flow) {
	select {
	case s.flowChan <- flow:
		// Flow sent successfully
	default:
		log.Printf("Flow channel full, dropping flow")
	}
}

func main() {
	flag.Parse()

	lis, err := net.Listen("tcp", fmt.Sprintf(":%d", *port))
	if err != nil {
		log.Fatalf("failed to listen: %v", err)
	}

	server := grpc.NewServer()
	flowServer := newFlowServer()
	pb.RegisterFlowServiceServer(server, flowServer)

	log.Printf("Flow server listening on port %d", *port)
	if err := server.Serve(lis); err != nil {
		log.Fatalf("failed to serve: %v", err)
	}
}
