package main

import (
	"context"
	"crypto/tls"
	"fmt"
	"log"
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

func recordMetrics(duration time.Duration, method string, statusCode string, path string, responseSizeBytes int) {
	go func() {
		for {
			requestDuration.WithLabelValues(path, method, statusCode).Observe(duration.Seconds())
			requestsTotal.WithLabelValues(path, method, statusCode).Inc()
            responseSize.WithLabelValues(path, method, statusCode).Observe(float64(responseSizeBytes))

			time.Sleep(2 * time.Second)
		}
	}()
    
}

var (
    requestDuration = prometheus.NewHistogramVec(
        prometheus.HistogramOpts{
            Name:    "http3_request_duration_seconds",
            Help:    "Histogram of request latencies",
            Buckets: prometheus.DefBuckets,
        },
        []string{"path", "method", "status_code"},
    )

    requestsTotal = prometheus.NewCounterVec(
        prometheus.CounterOpts{
            Name: "http3_requests_total",
            Help: "Total number of HTTP requests made",
        },
        []string{"path", "method", "status_code"},
    )

    responseSize = prometheus.NewHistogramVec(
        prometheus.HistogramOpts{
            Name:    "http3_response_size_bytes",
            Help:    "Size of HTTP responses in bytes",
            Buckets: prometheus.ExponentialBuckets(100, 10, 8),
        },
        []string{"path", "method", "status_code"},
    )
)

func init() {
    prometheus.MustRegister(requestDuration)
	prometheus.MustRegister(requestsTotal)
	prometheus.MustRegister(responseSize)
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
		body := "Hello from HTTP/3 server!"
		responseSizeBytes := len(body)
    	recordMetrics(duration, r.Method, statusCode, r.URL.Path, responseSizeBytes)
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
