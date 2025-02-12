package main

import (
	"context"
	"crypto/rand"
	"fmt"
	"io"
	"log"
	"time"

	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"

	pb "grpc-with-http3/grpc-server-client/perf"
)

type PerformanceClient struct {
	client pb.PerfTestServiceClient
	conn   *grpc.ClientConn
}

// NewPerformanceClient creates a new client connection
func NewPerformanceClient(address string) (*PerformanceClient, error) {
	// Set up a connection to the server
	conn, err := grpc.Dial(address, 
		grpc.WithTransportCredentials(insecure.NewCredentials()),
		grpc.WithBlock(),
		grpc.WithTimeout(10*time.Second),
	)
	if err != nil {
		return nil, fmt.Errorf("did not connect: %v", err)
	}

	// Create the client
	client := pb.NewPerfTestServiceClient(conn)

	return &PerformanceClient{
		client: client,
		conn:   conn,
	}, nil
}

// Close terminates the client connection
func (pc *PerformanceClient) Close() {
	if pc.conn != nil {
		pc.conn.Close()
	}
}

// TestPingPong demonstrates latency testing
func (pc *PerformanceClient) TestPingPong(message string) error {
	start := time.Now()
	
	// Send PingPong request
	resp, err := pc.client.PingPong(context.Background(), &pb.PingRequest{
		Message:         message,
		ClientTimestamp: time.Now().UnixNano(),
	})
	if err != nil {
		return fmt.Errorf("PingPong error: %v", err)
	}

	latency := time.Since(start)
	
	fmt.Printf("PingPong Test:\n")
	fmt.Printf("  Message sent:     %s\n", message)
	fmt.Printf("  Server echo:      %s\n", resp.Echo)
	fmt.Printf("  Latency:          %v\n", latency)
	fmt.Printf("  Server Timestamp: %d\n", resp.Timestamp)

	return nil
}

// TestStreamingDownload demonstrates server-side streaming and throughput
func (pc *PerformanceClient) TestStreamingDownload(payloadSize, numMessages int32) error {
	start := time.Now()
	
	// Create streaming download request
	stream, err := pc.client.StreamingDownload(context.Background(), &pb.DownloadRequest{
		PayloadSizeBytes: payloadSize,
		NumMessages:      numMessages,
	})
	if err != nil {
		return fmt.Errorf("StreamingDownload error: %v", err)
	}

	var totalBytesReceived int64
	var messagesReceived int32 = 0

	// Receive messages
	for {
		resp, err := stream.Recv()
		if err == io.EOF {
			break
		}
		if err != nil {
			return fmt.Errorf("error receiving stream: %v", err)
		}

		totalBytesReceived += int64(len(resp.Payload))
		messagesReceived++
	}

	duration := time.Since(start)
	throughput := float64(totalBytesReceived) / duration.Seconds()

	fmt.Printf("Streaming Download Test:\n")
	fmt.Printf("  Payload Size:     %d bytes\n", payloadSize)
	fmt.Printf("  Total Messages:   %d\n", messagesReceived)
	fmt.Printf("  Total Bytes:      %d bytes\n", totalBytesReceived)
	fmt.Printf("  Duration:         %v\n", duration)
	fmt.Printf("  Throughput:       %.2f MB/s\n", throughput/1024/1024)

	return nil
}

// TestStreamingUpload demonstrates client-side streaming and upload throughput
func (pc *PerformanceClient) TestStreamingUpload(payloadSize, numMessages int64) error {
	start := time.Now()

	// Create streaming upload
	stream, err := pc.client.StreamingUpload(context.Background())
	if err != nil {
		return fmt.Errorf("StreamingUpload error: %v", err)
	}

	// Generate and send payloads
	payload := make([]byte, payloadSize)
	for i := int64(0); i < numMessages; i++ {
		rand.Read(payload)
		err := stream.Send(&pb.UploadRequest{
			SequenceNumber:    i,
			Payload:          payload,
			ClientTimestamp: time.Now().UnixNano(),
		})
		if err != nil {
			return fmt.Errorf("error sending stream: %v", err)
		}
	}

	// Close stream and get summary
	summary, err := stream.CloseAndRecv()
	if err != nil {
		return fmt.Errorf("error closing stream: %v", err)
	}

	duration := time.Since(start)

	fmt.Printf("Streaming Upload Test:\n")
	fmt.Printf("  Payload Size:     %d bytes\n", payloadSize)
	fmt.Printf("  Total Messages:   %d\n", numMessages)
	fmt.Printf("  Total Bytes:      %d bytes\n", summary.TotalBytesReceived)
	fmt.Printf("  Duration:         %d ms\n", summary.DurationMs)
	fmt.Printf("  Throughput:       %.2f MB/s\n", summary.Throughput/1024/1024)
	fmt.Printf("  Duration:         %v\n", duration)

	return nil
}

// TestBidirectionalStream demonstrates full-duplex communication
func (pc *PerformanceClient) TestBidirectionalStream(numMessages int64) error {
	start := time.Now()

	stream, err := pc.client.BidirectionalStream(context.Background())
	if err != nil {
		return fmt.Errorf("BidirectionalStream error: %v", err)
	}

	// Send goroutine
	go func() {
		payload := make([]byte, 1024)
		for i := int64(0); i < numMessages; i++ {
			rand.Read(payload)
			err := stream.Send(&pb.StreamRequest{
				SequenceNumber:    i,
				Payload:          payload,
				ClientTimestamp: time.Now().UnixNano(),
			})
			if err != nil {
				log.Printf("Error sending message: %v", err)
				return
			}
			time.Sleep(10 * time.Millisecond) // Add small delay between messages
		}
		stream.CloseSend()
	}()

	// Receive goroutine
	var receivedMessages int32 = 0
	for {
		resp, err := stream.Recv()
		if err == io.EOF {
			break
		}
		if err != nil {
			return fmt.Errorf("error receiving stream: %v", err)
		}

		receivedMessages++
		log.Printf("Received message %d, Timestamp: %d", resp.SequenceNumber, resp.Timestamp)
	}

	duration := time.Since(start)

	fmt.Printf("Bidirectional Stream Test:\n")
	fmt.Printf("  Total Messages Sent:     %d\n", numMessages)
	fmt.Printf("  Total Messages Received: %d\n", receivedMessages)
	fmt.Printf("  Duration:                %v\n", duration)

	return nil
}

// RunPerformanceTests runs a comprehensive set of performance tests
func RunPerformanceTests(address string) error {
	// Create client
	client, err := NewPerformanceClient(address)
	if err != nil {
		return fmt.Errorf("failed to create client: %v", err)
	}
	defer client.Close()

	// Run different performance tests
	fmt.Println("Running Performance Tests...")

	// PingPong Latency Test
	if err := client.TestPingPong("Hello, Performance Server!"); err != nil {
		log.Printf("PingPong Test Failed: %v", err)
	}

	// Streaming Download Test
	if err := client.TestStreamingDownload(1024, 100); err != nil {
		log.Printf("Streaming Download Test Failed: %v", err)
	}

	// Streaming Upload Test
	if err := client.TestStreamingUpload(1024, 100); err != nil {
		log.Printf("Streaming Upload Test Failed: %v", err)
	}

	// Bidirectional Stream Test
	if err := client.TestBidirectionalStream(50); err != nil {
		log.Printf("Bidirectional Stream Test Failed: %v", err)
	}

	return nil
}

func main() {
	// Example usage
	err := RunPerformanceTests("localhost:50051")
	if err != nil {
		log.Fatalf("Performance tests failed: %v", err)
	}
}