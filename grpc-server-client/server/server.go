package main

import (
	"context"
	"crypto/rand"
	"crypto/tls"
	"log"
	"net"
	"net/http"
	"time"

	"github.com/quic-go/quic-go"
	"github.com/quic-go/quic-go/http3"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/grpc/reflection"

	pb "grpc-with-http3/grpc-server-client/perf"

)

// Server represents the gRPC server
type Server struct {
	pb.UnimplementedPerfTestServiceServer
}

// PingPong implements a simple latency test
func (s *Server) PingPong(ctx context.Context, req *pb.PingRequest) (*pb.PongResponse, error) {
	return &pb.PongResponse{
		Timestamp: time.Now().UnixNano(),
		Echo:      req.Message,
	}, nil
}

// StreamingDownload implements throughput testing for server->client streaming
func (s *Server) StreamingDownload(req *pb.DownloadRequest, stream pb.PerfTestService_StreamingDownloadServer) error {
	payloadSize := int(req.PayloadSizeBytes)
	if payloadSize <= 0 {
		payloadSize = 1024 // Default 1KB
	}

	payload := make([]byte, payloadSize)
	for i := 0; i < int(req.NumMessages); i++ {
		_, err := rand.Read(payload) // Generate random payload
		if err != nil {
			return err
		}

		response := &pb.DownloadResponse{
			SequenceNumber: int64(i),
			Payload:       payload,
			Timestamp:     time.Now().UnixNano(),
		}

		if err := stream.Send(response); err != nil {
			return err
		}
	}
	return nil
}

// StreamingUpload implements throughput testing for client->server streaming
func (s *Server) StreamingUpload(stream pb.PerfTestService_StreamingUploadServer) error {
	var totalBytes int64
	startTime := time.Now()

	for {
		req, err := stream.Recv()
		if err != nil {
			// Send final statistics
			return stream.SendAndClose(&pb.UploadSummary{
				TotalBytesReceived: totalBytes,
				DurationMs:        time.Since(startTime).Milliseconds(),
				Throughput:        float64(totalBytes) / time.Since(startTime).Seconds(),
			})
		}
		totalBytes += int64(len(req.Payload))
	}
}

// BidirectionalStream implements bidirectional streaming for testing full-duplex communication
func (s *Server) BidirectionalStream(stream pb.PerfTestService_BidirectionalStreamServer) error {
	for {
		req, err := stream.Recv()
		if err != nil {
			return err
		}

		// Echo back with timestamp
		response := &pb.StreamResponse{
			SequenceNumber: req.SequenceNumber,
			Timestamp:     time.Now().UnixNano(),
			Payload:       req.Payload,
		}

		if err := stream.Send(response); err != nil {
			return err
		}
	}
}

func main() {
	// Create a context for graceful shutdown
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	// Create gRPC server
	grpcServer := grpc.NewServer()
	perfServer := &Server{}
	pb.RegisterPerfTestServiceServer(grpcServer, perfServer)
	
	// Enable gRPC reflection for easier client testing
	reflection.Register(grpcServer)

	// Prepare gRPC listener
	grpcListener, err := net.Listen("tcp", ":50051")
	if err != nil {
		log.Fatalf("Failed to listen for gRPC: %v", err)
	}

	// HTTP/3 multiplexed handler
	mux := http.NewServeMux()
	
	// Add a simple HTTP handler
	mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		// Check if request was made using 0-RTT
		wasPotentiallyReplayed := r.TLS != nil && !r.TLS.HandshakeComplete
		if wasPotentiallyReplayed {
			log.Println("Warning: Request was made using 0-RTT")
		}
		
		w.Write([]byte("Performance Test Server Running"))
	})

	// Create custom TLS config
	tlsConfig := &tls.Config{
		MinVersion: tls.VersionTLS13,
	}
	
	// Configure HTTP/3 server
	http3Server := &http3.Server{
		Handler:    mux,
		Addr:       "localhost:443",
		Port:       443, // External port for Alt-Svc header
		TLSConfig:  http3.ConfigureTLSConfig(tlsConfig),
		QUICConfig: &quic.Config{
			Allow0RTT: true, // Enable 0-RTT support
		},
	}

	// Wrap handler to add Alt-Svc header for HTTP/1.1 and HTTP/2 requests
	wrappedHandler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.ProtoMajor < 3 {
			if err := http3Server.SetQUICHeaders(w.Header()); err != nil {
				log.Printf("Failed to set QUIC headers: %v", err)
			}
		}
		mux.ServeHTTP(w, r)
	})
	http3Server.Handler = wrappedHandler

	// Goroutine to start gRPC server
	go func() {
		log.Printf("Starting gRPC server on :50051")
		if err := grpcServer.Serve(grpcListener); err != nil {
			log.Printf("gRPC server error: %v", err)
			cancel() // Cancel the main context on error
		}
	}()

	// Goroutine to start HTTP/3 server
	go func() {
		log.Printf("Starting HTTP/3 server on %s", http3Server.Addr)
		if err := http3.ListenAndServeQUIC("localhost:443", "cert.crt", "cert.key", mux); err != nil {
			log.Printf("HTTP/3 server error: %v", err)
			cancel() // Cancel the main context on error
		}
	}()

	// Manual QUIC transport setup for protocol demultiplexing
	go func() {
		conn, err := net.ListenUDP("udp", &net.UDPAddr{Port: 444}) // Different port for demo
		if err != nil {
			log.Fatal(err)
		}
		
		tr := quic.Transport{Conn: conn}
		ln, err := tr.ListenEarly(http3Server.TLSConfig, http3Server.QUICConfig)
		if err != nil {
			log.Fatal(err)
		}

		for {
			conn, err := ln.Accept(context.Background())
			if err != nil {
				log.Printf("Failed to accept connection: %v", err)
				continue
			}

			go func(c quic.Connection) {
				switch c.ConnectionState().TLS.NegotiatedProtocol {
				case http3.NextProtoH3:
					if err := http3Server.ServeQUICConn(c); err != nil {
						log.Printf("Failed to serve HTTP/3: %v", err)
					}
				default:
					log.Printf("Unknown protocol: %s", 
						c.ConnectionState().TLS.NegotiatedProtocol)
				}
			}(conn)
		}
	}()

	// Test gRPC connection 
	go func() {
		// Wait a moment for servers to start
		time.Sleep(5 * time.Second)

		// Attempt to create a gRPC client connection
		conn, err := grpc.Dial("localhost:50051", 
			grpc.WithTransportCredentials(insecure.NewCredentials()),
			grpc.WithBlock(),
			grpc.WithTimeout(10*time.Second),
		)
		if err != nil {
			log.Printf("Failed to connect to gRPC server: %v", err)
			return
		}
		defer conn.Close()

		// Create a client
		client := pb.NewPerfTestServiceClient(conn)

		// Test PingPong method
		pingResp, err := client.PingPong(context.Background(), &pb.PingRequest{
			Message: "Hello from client test",
		})
		if err != nil {
			log.Printf("PingPong test failed: %v", err)
			return
		}
		log.Printf("PingPong test successful. Server echoed: %s, Timestamp: %d", 
			pingResp.Echo, pingResp.Timestamp)

		// Demonstrating streaming methods would require more complex client logic
		log.Println("Basic gRPC connectivity test completed")
	}()

	// Wait for shutdown signal or context cancellation
	<-ctx.Done()

	// Graceful shutdown
	log.Println("Starting graceful shutdown...")
	
	shutdownCtx, shutdownCancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer shutdownCancel()

	// Shutdown gRPC server
	grpcServer.GracefulStop()

	// Shutdown HTTP/3 server
	if err := http3Server.Shutdown(shutdownCtx); err != nil {
		log.Printf("HTTP/3 server shutdown error: %v", err)
	}

	log.Println("Server shutdown complete")
}