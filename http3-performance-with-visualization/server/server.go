package main

import (
	"context"
	"crypto/tls"
	"fmt"
	"log"
	"net/http"
	"runtime"
	"time"

	"github.com/quic-go/quic-go"
	"github.com/quic-go/quic-go/http3"

)


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
	mux := http.NewServeMux()
	mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		start := time.Now()

		wasPotentiallyReplayed := !r.TLS.HandshakeComplete
		if wasPotentiallyReplayed {
			log.Println("Warning: Request was made using 0-RTT")
		}
		fmt.Fprintf(w, "Hello from HTTP/3 server!")

		time.Since(start)
    	fmt.Sprintf("%d", http.StatusOK) 
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

	time.Sleep(time.Hour)
	log.Println("Starting graceful shutdown...")
	
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Hour)
	defer cancel()
	
	if err := server.Shutdown(ctx); err != nil {
		log.Printf("Shutdown error: %v", err)
	}
	log.Println("Server shutdown complete")
}
