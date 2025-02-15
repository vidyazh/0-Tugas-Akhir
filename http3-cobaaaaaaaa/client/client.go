package main

import (
    "context"
    "crypto/tls"
    "fmt"
    "io"
    "log"
    "net"
    "net/http"
    "time"

    "github.com/quic-go/quic-go"
    "github.com/quic-go/quic-go/http3"
)

func main() {
    tr := &http3.Transport{
        TLSClientConfig: &tls.Config{
            InsecureSkipVerify: true, 
            ClientSessionCache: tls.NewLRUClientSessionCache(100), 
        },
        QUICConfig: &quic.Config{
            Allow0RTT: true,
        },
    }
    defer tr.Close()

    client := &http.Client{
        Transport: tr,
    }

    // request biasa
    makeNormalRequest(client)

    // request dengan 0-RTT
    make0RTTRequest(client)

    // Contoh menggunakan custom QUIC transport (fixed version)
    makeCustomTransportRequest()
}

func makeNormalRequest(client *http.Client) {
    start := time.Now()

    resp, err := client.Get("https://localhost:443")
    if err != nil {
        log.Printf("Error making request: %v", err)
        return
    }
    defer resp.Body.Close()

    ttfb := time.Since(start)
    log.Printf("TTFB: %v", ttfb)

    body, err := io.ReadAll(resp.Body)
    if err != nil {
        log.Printf("Error reading response: %v", err)
        return
    }

    fmt.Printf("Response (Normal request):\nStatus: %s\nProtocol: %s\nBody: %s\n",
        resp.Status, resp.Proto, string(body))
}

func make0RTTRequest(client *http.Client) {
    // Buat request dengan method 0-RTT khusus
    req, err := http.NewRequest(http3.MethodGet0RTT, "https://localhost:443", nil)
    if err != nil {
        log.Printf("Error creating 0-RTT request: %v", err)
        return
    }

    resp, err := client.Do(req)
    if err != nil {
        log.Printf("Error making 0-RTT request: %v", err)
        return
    }
    defer resp.Body.Close()

    body, err := io.ReadAll(resp.Body)
    if err != nil {
        log.Printf("Error reading 0-RTT response: %v", err)
        return
    }

    fmt.Printf("Response (0-RTT request):\nStatus: %s\nProtocol: %s\nBody: %s\n",
        resp.Status, resp.Proto, string(body))
}

func makeCustomTransportRequest() {
    udpConn, err := net.ListenUDP("udp", &net.UDPAddr{IP: net.IPv4zero, Port: 0})
    if err != nil {
        log.Printf("Error creating UDP connection: %v", err)
        return
    }
    defer udpConn.Close()

    tr := &quic.Transport{
        Conn: udpConn,
    }
    defer tr.Close()

    tlsConfig := &tls.Config{
        InsecureSkipVerify: true,
        NextProtos:         []string{"h3"}, // Penting: set ALPN untuk HTTP/3
    }

    h3tr := &http3.Transport{
        TLSClientConfig: tlsConfig,
        QUICConfig: &quic.Config{
            Allow0RTT: true,
        },
        Dial: func(ctx context.Context, addr string, tlsConf *tls.Config, quicConf *quic.Config) (quic.EarlyConnection, error) {
            udpAddr, err := net.ResolveUDPAddr("udp", addr)
            if err != nil {
                return nil, fmt.Errorf("failed to resolve address: %v", err)
            }
            return tr.DialEarly(ctx, udpAddr, tlsConf, quicConf)
        },
    }
    defer h3tr.Close()

    client := &http.Client{
        Transport: h3tr,
        Timeout:   30 * time.Second,
    }

    // Buat request
    ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
    defer cancel()

    req, err := http.NewRequestWithContext(ctx, "GET", "https://localhost:443", nil)
    if err != nil {
        log.Printf("Error creating request: %v", err)
        return
    }

    resp, err := client.Do(req)
    if err != nil {
        log.Printf("Error making custom transport request: %v", err)
        return
    }
    defer resp.Body.Close()

    body, err := io.ReadAll(resp.Body)
    if err != nil {
        log.Printf("Error reading custom transport response: %v", err)
        return
    }

    fmt.Printf("Response (Custom transport):\nStatus: %s\nProtocol: %s\nBody: %s\n",
        resp.Status, resp.Proto, string(body))
}