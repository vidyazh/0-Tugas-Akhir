package main

import (
	"context"
	"crypto/tls"
	"fmt"
	"log"
	"net"
	"net/http"
	"time"

	"github.com/quic-go/quic-go"
	"github.com/quic-go/quic-go/http3"

)

func main() {
	mux := http.NewServeMux()
	mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		start := time.Now()  // Catat waktu saat permintaan diterima
	
		// Simulasikan pemrosesan server (misalnya, delay)
		time.Sleep(100 * time.Millisecond)
	
		// Catat waktu respons pertama
		ttfb := time.Since(start)
		log.Printf("TTFB: %v", ttfb)
	
		fmt.Fprintf(w, "Hello from HTTP/3 server!")
	})

	mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		wasPotentiallyReplayed := !r.TLS.HandshakeComplete
		if wasPotentiallyReplayed {
			log.Println("Warning: Request was made using 0-RTT")
		}
		
		fmt.Fprintf(w, "Hello from HTTP/3 server!")
	})

	tlsConfig := &tls.Config{
		MinVersion: tls.VersionTLS13,
	}
	
	server := http3.Server{
		Handler:    mux,
		Addr:       "localhost:443",
		Port:       443, 
		TLSConfig:  http3.ConfigureTLSConfig(tlsConfig),
		QUICConfig: &quic.Config{
			Allow0RTT: true, 
		},
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

	go func() {
		conn, err := net.ListenUDP("udp", &net.UDPAddr{Port: 444})
		if err != nil {
			log.Fatal(err)
		}
		
		tr := quic.Transport{Conn: conn}
		ln, err := tr.ListenEarly(server.TLSConfig, server.QUICConfig)
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
					if err := server.ServeQUICConn(c); err != nil {
						log.Printf("Failed to serve HTTP/3: %v", err)
					}
				default:
					log.Printf("Unknown protocol: %s", 
						c.ConnectionState().TLS.NegotiatedProtocol)
				}
			}(conn)
		}
	}()

	time.Sleep(time.Minute)
	log.Println("Starting graceful shutdown...")
	
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	
	if err := server.Shutdown(ctx); err != nil {
		log.Printf("Shutdown error: %v", err)
	}
	log.Println("Server shutdown complete")
}