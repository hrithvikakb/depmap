package main

import (
	"fmt"
	"log"
	"net"

	"google.golang.org/grpc"

	pb "hubbleclone/pkg/proto/flow"
)

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
	lis, err := net.Listen("tcp", ":50051")
	if err != nil {
		log.Fatalf("Failed to listen: %v", err)
	}

	server := grpc.NewServer()
	flowServer := newFlowServer()
	pb.RegisterFlowServiceServer(server, flowServer)

	fmt.Println("Flow server listening on :50051")
	if err := server.Serve(lis); err != nil {
		log.Fatalf("Failed to serve: %v", err)
	}
}
