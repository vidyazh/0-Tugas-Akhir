package main

import (
	"context"
	"crypto/tls"
	"fmt"
	"log"
	// "net"
	"net/http"
	"os"
	"os/signal"
	"runtime"
	"syscall"
	"time"

	"github.com/quic-go/quic-go"
	"github.com/quic-go/quic-go/http3"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promhttp"
)

func recordMetrics(duration time.Duration, method string, statusCode string) {
	go func() {
		for {
			requestLatency.WithLabelValues(method, statusCode).Observe(duration.Seconds())
			time.Sleep(2 * time.Second)
		}
	}()
    
}

var (
    requestLatency = prometheus.NewHistogramVec(
        prometheus.HistogramOpts{
            Name:    "http_request_duration_seconds",
            Help:    "Histogram of HTTP request durations.",
            Buckets: prometheus.DefBuckets,
        },
        []string{"method", "status_code"},
    )
)

func init() {
    prometheus.MustRegister(requestLatency)
}


func logSystemStats() {
	var m runtime.MemStats
	runtime.ReadMemStats(&m)

	log.Printf("Alloc: %v MB", m.Alloc/1024/1024)
	log.Printf("TotalAlloc: %v MB", m.TotalAlloc/1024/1024)
	log.Printf("Sys: %v MB", m.Sys/1024/1024)
	log.Printf("NumGC: %v", m.NumGC)
}

func startStatsLogging() {
	go func() {
		for {
			logSystemStats()
			time.Sleep(5 * time.Second) 
		}
	}()
}

func main() {
	http.Handle("/metrics", promhttp.Handler())
	go func() {
		if err := http.ListenAndServe(":2112", nil); err != nil {
			log.Printf("Error starting Prometheus server: %v", err)
		}
	}()

	mux := http.NewServeMux()
	mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		start := time.Now()

		wasPotentiallyReplayed := !r.TLS.HandshakeComplete
		if wasPotentiallyReplayed {
			log.Println("Warning: Request was made using 0-RTT")
		}
		fmt.Fprintf(w, "Hello from HTTP/3 server!")

		duration := time.Since(start)
    	statusCode := fmt.Sprintf("%d", http.StatusOK) 
    	recordMetrics(duration, r.Method, statusCode)
	})

	tlsConfig := &tls.Config{
		MinVersion: tls.VersionTLS13,
	}

	server := http3.Server{
		Handler:    mux,
		Addr:       "localhost:443",
		Port:       443,
		TLSConfig:  http3.ConfigureTLSConfig(tlsConfig),
		QUICConfig: &quic.Config{Allow0RTT: true}, 
	}

	wrappedHandler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.ProtoMajor < 3 {
			if err := server.SetQUICHeaders(w.Header()); err != nil {
				log.Printf("Failed to set QUIC headers: %v", err)
			}
		}
		mux.ServeHTTP(w, r)
	})
	server.Handler = wrappedHandler

	startStatsLogging()

	go func() {
		log.Printf("Starting HTTP/3 server on %s", server.Addr)
		if err := http3.ListenAndServeQUIC("localhost:443", "cert.crt", "cert.key", mux); err != nil {
			log.Printf("HTTP/3 server error: %v", err)
		}
	}()

	// go func() {
	// 	conn, err := net.ListenUDP("udp", &net.UDPAddr{Port: 444})
	// 	if err != nil {
	// 		log.Fatal(err)
	// 	}

	// 	tr := quic.Transport{Conn: conn}
	// 	ln, err := tr.ListenEarly(server.TLSConfig, server.QUICConfig)
	// 	if err != nil {
	// 		log.Fatal(err)
	// 	}

	// 	for {
	// 		conn, err := ln.Accept(context.Background())
	// 		if err != nil {
	// 			log.Printf("Failed to accept connection: %v", err)
	// 			continue
	// 		}

	// 		go func(c quic.Connection) {
	// 			switch c.ConnectionState().TLS.NegotiatedProtocol {
	// 			case http3.NextProtoH3:
	// 				if err := server.ServeQUICConn(c); err != nil {
	// 					log.Printf("Failed to serve HTTP/3: %v", err)
	// 				}
	// 			default:
	// 				log.Printf("Unknown protocol: %s", c.ConnectionState().TLS.NegotiatedProtocol)
	// 			}
	// 		}(conn)
	// 	}
	// }()

	// time.Sleep(time.Minute)

	sigs := make(chan os.Signal, 1)
	signal.Notify(sigs, syscall.SIGINT, syscall.SIGTERM)
	<-sigs

	log.Println("Starting graceful shutdown...")

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	if err := server.Shutdown(ctx); err != nil {
		log.Printf("Shutdown error: %v", err)
	}
	log.Println("Server shutdown complete")
}
