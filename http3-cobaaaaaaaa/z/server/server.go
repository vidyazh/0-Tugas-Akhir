package main

import (
	"context"
	"crypto/tls"
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"runtime"
	"sync"
	"time"

	"github.com/quic-go/quic-go"
	"github.com/quic-go/quic-go/http3"
)

type ServerMetrics struct {
    ConnectionTime    time.Duration
    ProcessingTime   time.Duration
    Is0RTT          bool
    MemoryUsage     uint64
    CPUUsage        float64
    RequestCount    int64
    mu              sync.Mutex
}

type MetricsCollector struct {
    metrics map[string]*ServerMetrics
    mu      sync.Mutex
}

func NewMetricsCollector() *MetricsCollector {
    return &MetricsCollector{
        metrics: make(map[string]*ServerMetrics),
    }
}

func (mc *MetricsCollector) Record(sessionID string, metrics *ServerMetrics) {
    mc.mu.Lock()
    defer mc.mu.Unlock()
    mc.metrics[sessionID] = metrics
}

func (mc *MetricsCollector) GetMetrics() map[string]*ServerMetrics {
    mc.mu.Lock()
    defer mc.mu.Unlock()
    return mc.metrics
}

func main() {
	collector := NewMetricsCollector()

	mux := http.NewServeMux()

	mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
        handleRequest(w, r, collector)
    })
    
    // Endpoint untuk metrics
    mux.HandleFunc("/metrics", func(w http.ResponseWriter, r *http.Request) {
        w.Header().Set("Content-Type", "application/json")
        json.NewEncoder(w).Encode(collector.GetMetrics())
    })

	tlsConfig := &tls.Config{
		MinVersion: tls.VersionTLS13,
	}

	server := http3.Server{
		Handler:    mux,
		Addr:       "localhost:443",
		Port:       443,
		TLSConfig:  http3.ConfigureTLSConfig(tlsConfig),
		QUICConfig: &quic.Config{Allow0RTT: true, MaxIdleTimeout: 30 * time.Second,
            MaxIncomingStreams: 1000,}, 
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

	go func() {
		log.Printf("Starting HTTP/3 server on %s", server.Addr)
		if err := http3.ListenAndServeQUIC("localhost:443", "cert.crt", "cert.key", mux); err != nil {
			log.Printf("HTTP/3 server error: %v", err)
		}
	}()

	time.Sleep(time.Hour)
	log.Println("Starting graceful shutdown...")
	
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Hour)
	defer cancel()
	
	if err := server.Shutdown(ctx); err != nil {
		log.Printf("Shutdown error: %v", err)
	}
	log.Println("Server shutdown complete")
}

func handleRequest(w http.ResponseWriter, r *http.Request, collector *MetricsCollector) {
    startTime := time.Now()
    sessionID := r.Header.Get("X-Session-ID")
    log.Printf("New request: SessionID=%s, TLS=%v, Proto=%s", 
        sessionID, 
        r.TLS != nil,
        r.Proto)
    
    metrics := &ServerMetrics{
        Is0RTT: r.TLS.HandshakeComplete == true,
        RequestCount: 0,
    }

    log.Printf("Connection type: SessionID=%s, Is0RTT=%v", 
        sessionID, 
        metrics.Is0RTT)

    // Simulate some processing
    time.Sleep(10 * time.Millisecond)
    
    metrics.ConnectionTime = time.Since(startTime)
    metrics.ProcessingTime = time.Since(startTime)
    
    var m runtime.MemStats
    runtime.ReadMemStats(&m)
    metrics.MemoryUsage = m.Alloc
    
    collector.Record(sessionID, metrics)
    
    w.Header().Set("X-Is-0RTT", fmt.Sprintf("%v", metrics.Is0RTT))
    fmt.Fprintf(w, "Request processed. Session ID: %s, 0-RTT: %v", sessionID, metrics.Is0RTT)
}

func collectSystemMetrics(collector *MetricsCollector) {
    ticker := time.NewTicker(5 * time.Second)
    for range ticker.C {
        var m runtime.MemStats
        runtime.ReadMemStats(&m)
        
        metrics := &ServerMetrics{
            MemoryUsage: m.Alloc,
            CPUUsage:    getCPUUsage(),
        }
        
        collector.Record("system", metrics)
    }
}

func getCPUUsage() float64 {
    // Implement CPU usage measurement
    return 0.0
}
