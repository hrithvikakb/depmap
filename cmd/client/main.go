package main

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"log"
	"os"
	"os/signal"
	"syscall"

	pb "hubbleclone/proto/flow"

	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"
)

var (
	serverAddr = flag.String("server", "localhost:4245", "The server address in the format of host:port")
	namespace  = flag.String("namespace", "", "Filter flows by namespace")
	verdict    = flag.String("verdict", "", "Filter flows by verdict (FORWARDED or DROPPED)")
	format     = flag.String("format", "json", "Output format (json or text)")
)

func main() {
	flag.Parse()

	// Set up connection to server
	conn, err := grpc.Dial(*serverAddr, grpc.WithTransportCredentials(insecure.NewCredentials()))
	if err != nil {
		log.Fatalf("Failed to connect: %v", err)
	}
	defer conn.Close()

	client := pb.NewFlowServiceClient(conn)

	// Create context that's canceled on SIGTERM/SIGINT
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	// Handle shutdown gracefully
	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, syscall.SIGTERM, syscall.SIGINT)
	go func() {
		<-sigCh
		cancel()
	}()

	// Start streaming flows
	stream, err := client.GetFlows(ctx, &pb.GetFlowsRequest{
		Namespace: *namespace,
		Verdict:   *verdict,
	})
	if err != nil {
		log.Fatalf("Failed to start flow stream: %v", err)
	}

	log.Printf("Observing flows...")
	for {
		flow, err := stream.Recv()
		if err == io.EOF {
			return
		}
		if err != nil {
			log.Fatalf("Failed to receive flow: %v", err)
		}

		// Format and print the flow
		switch *format {
		case "json":
			printJSON(flow)
		case "text":
			printText(flow)
		default:
			log.Fatalf("Unknown format: %s", *format)
		}
	}
}

func printJSON(flow *pb.Flow) {
	jsonBytes, err := json.Marshal(flow)
	if err != nil {
		log.Printf("Failed to marshal flow to JSON: %v", err)
		return
	}
	fmt.Println(string(jsonBytes))
}

func printText(flow *pb.Flow) {
	// Format: source -> destination [protocol] (verdict)
	src := flow.SourcePod
	if src == "" {
		src = flow.SourceIp
	}
	if flow.SourceNamespace != "" {
		src = fmt.Sprintf("%s (%s)", src, flow.SourceNamespace)
	}

	dst := flow.DestinationPod
	if dst == "" {
		dst = flow.DestinationIp
	}
	if flow.DestinationNamespace != "" {
		dst = fmt.Sprintf("%s (%s)", dst, flow.DestinationNamespace)
	}

	fmt.Printf("%s -> %s [%s] (%s)\n", src, dst, flow.L4Protocol, flow.Verdict)
}
