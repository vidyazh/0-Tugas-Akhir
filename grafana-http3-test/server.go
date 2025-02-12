package main

import (
    "crypto/tls"
    "fmt"
    "log"
    "net/http"

    "github.com/prometheus/client_golang/prometheus"
    "github.com/prometheus/client_golang/prometheus/promauto"
    "github.com/prometheus/client_golang/prometheus/promhttp"
    "github.com/quic-go/quic-go"
    "github.com/quic-go/quic-go/http3"
)

// Metrics definitions
var (
    requestsTotal = promauto.NewCounterVec(
        prometheus.CounterOpts{
            Name: "http3_requests_total",
            Help: "Total number of HTTP/3 requests processed",
        },
        []string{"method", "path", "status"},
    )

    zeroRTTRequests = promauto.NewCounter(
        prometheus.CounterOpts{
            Name: "http3_zero_rtt_requests_total",
            Help: "Total number of 0-RTT requests received",
        },
    )

    activeConnections = promauto.NewGauge(
        prometheus.GaugeOpts{
            Name: "http3_active_connections",
            Help: "Number of active HTTP/3 connections",
        },
    )
)

func main() {
    // Setup metrics server
    metricsMux := http.NewServeMux()
    metricsMux.Handle("/metrics", promhttp.Handler())
    
    // Start metrics server in a separate goroutine
    go func() {
        log.Printf("Starting metrics server on :2112")
        if err := http.ListenAndServe(":2112", metricsMux); err != nil {
            log.Printf("Metrics server error: %v", err)
        }
    }()

    // Setup HTTP/3 server
    mux := http.NewServeMux()
    mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
        // Increment metrics
        activeConnections.Inc()
        defer activeConnections.Dec()

        if r.TLS != nil && !r.TLS.HandshakeComplete {
            zeroRTTRequests.Inc()
        }

        requestsTotal.WithLabelValues(r.Method, r.URL.Path, "200").Inc()
        
        fmt.Fprintf(w, "Hello from HTTP/3 server!")
    })

    // Configure TLS
    tlsConfig := &tls.Config{
        MinVersion: tls.VersionTLS13,
    }

    // Setup HTTP/3 server
    server := &http3.Server{
        Handler:    mux,
        Addr:       "localhost:443",
		TLSConfig: tlsConfig,
        QUICConfig: &quic.Config{
            Allow0RTT: true,
        },
    }

    log.Printf("Starting HTTP/3 server on %s", server.Addr)
    if err := http3.ListenAndServeQUIC("localhost:443", "cert.crt", "cert.key", mux); err != nil {
        log.Fatal(err)
    }
}