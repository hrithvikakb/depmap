package main

import (
	"encoding/json"
	"fmt"
	"log"
	"os"

	"context"
	pb "hubbleclone/pkg/proto/flow"

	"github.com/spf13/cobra"
	"google.golang.org/grpc"
)

var rootCmd = &cobra.Command{
	Use:   "depmap",
	Short: "A lightweight service dependency mapper for Kubernetes",
}

var startCmd = &cobra.Command{
	Use:   "start",
	Short: "Start the depmap agent and server",
	Run: func(cmd *cobra.Command, args []string) {
		// TODO: Implement start logic
		fmt.Println("Starting depmap agent and server...")
	},
}

var observeCmd = &cobra.Command{
	Use:   "observe",
	Short: "Observe live service dependencies",
	Run: func(cmd *cobra.Command, args []string) {
		format, _ := cmd.Flags().GetString("format")
		if format != "json" {
			log.Fatal("Only JSON format is supported")
		}

		conn, err := grpc.Dial("localhost:50051", grpc.WithInsecure())
		if err != nil {
			log.Fatalf("Failed to connect: %v", err)
		}
		defer conn.Close()

		client := pb.NewFlowServiceClient(conn)
		stream, err := client.GetFlows(context.Background(), &pb.GetFlowsRequest{})
		if err != nil {
			log.Fatalf("Failed to get flows: %v", err)
		}

		for {
			flow, err := stream.Recv()
			if err != nil {
				log.Fatalf("Failed to receive flow: %v", err)
			}

			// Create a simplified output format
			output := map[string]string{
				"source":      fmt.Sprintf("%s (%s)", flow.SourcePod, flow.SourceNamespace),
				"destination": fmt.Sprintf("%s (%s)", flow.DestinationPod, flow.DestinationNamespace),
				"protocol":    flow.Protocol,
			}

			jsonOutput, err := json.Marshal(output)
			if err != nil {
				log.Printf("Failed to marshal flow: %v", err)
				continue
			}

			fmt.Println(string(jsonOutput))
		}
	},
}

func init() {
	observeCmd.Flags().String("format", "json", "Output format (only json supported)")
	rootCmd.AddCommand(startCmd)
	rootCmd.AddCommand(observeCmd)
}

func main() {
	if err := rootCmd.Execute(); err != nil {
		fmt.Println(err)
		os.Exit(1)
	}
}
