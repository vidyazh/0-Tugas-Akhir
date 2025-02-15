package main

import (
	"crypto/tls"
	"fmt"
	"io"
	"log"
	"net/http"
	"sync"
	"time"

	"github.com/quic-go/quic-go"
	"github.com/quic-go/quic-go/http3"
)

type Metrics struct {
	latencies  []time.Duration
	ttfbs      []time.Duration
	totalBytes int64
	totalTime  time.Duration
	mu         sync.Mutex
}

func measureLatency(client *http.Client, reqID int, metrics *Metrics) error {
    fmt.Printf("Starting request %d for Latency\n", reqID)
    startTime := time.Now()

    resp, err := client.Get("https://localhost:443")
    if err != nil {
        return fmt.Errorf("error making request %d: %v", reqID, err)
    }
    defer resp.Body.Close()

    _, err = io.Copy(io.Discard, resp.Body)
    if err != nil {
        return fmt.Errorf("error reading response body %d: %v", reqID, err)
    }

    latency := time.Since(startTime)

    metrics.mu.Lock()
    metrics.latencies = append(metrics.latencies, latency)
    metrics.mu.Unlock()

    fmt.Printf("Response %d (Latency: %s) | %s | %s |\n", reqID, latency, resp.Status, resp.Proto)
    return nil
}

func measureTTFB(client *http.Client, reqID int, metrics *Metrics) error {
    fmt.Printf("Starting request %d for TTFB\n", reqID)
    startTime := time.Now()

    resp, err := client.Get("https://localhost:443")
    if err != nil {
        return fmt.Errorf("error making request %d: %v", reqID, err)
    }
    defer resp.Body.Close()

    _, err = io.ReadFull(resp.Body, make([]byte, 1))
    if err != nil {
        return fmt.Errorf("error reading first byte %d: %v", reqID, err)
    }

    ttfb := time.Since(startTime)

    metrics.mu.Lock()
    metrics.ttfbs = append(metrics.ttfbs, ttfb)
    metrics.mu.Unlock()

    fmt.Printf("Response %d (TTFB: %s) | %s | %s |\n", reqID, ttfb, resp.Status, resp.Proto)
    return nil
}

func measureThroughput(client *http.Client, reqID int, metrics *Metrics) error {
	fmt.Printf("Starting throughput measurement for request %d\n", reqID)
	startTime := time.Now()

	resp, err := client.Get("https://localhost:443")
	if err != nil {
		return fmt.Errorf("error making request %d: %v", reqID, err)
	}
	defer resp.Body.Close()

	bytesReceived, err := io.Copy(io.Discard, resp.Body)
	if err != nil {
		return fmt.Errorf("error reading response body %d: %v", reqID, err)
	}

	duration := time.Since(startTime)

	metrics.mu.Lock()
	metrics.totalBytes += bytesReceived
	metrics.totalTime += duration
	metrics.mu.Unlock()

	fmt.Printf("Response %d (Throughput: %.2f bytes/sec) | %s | %s |\n", 
		reqID, 
		float64(bytesReceived)/duration.Seconds(), 
		resp.Status, 
		resp.Proto)
	return nil
}

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

	req, err := http.NewRequest(http3.MethodGet0RTT, "https://localhost:443", nil)
	if err != nil {
		log.Printf("Failed to create 0-RTT request: %v", err)
		return
	}
	tr.RoundTrip(req)

	client := &http.Client{Transport: tr}
	numRequests := 5000
	
	metrics := &Metrics{
		latencies: make([]time.Duration, 0),
        ttfbs:     make([]time.Duration, 0),
	}

	var wg sync.WaitGroup
	errors := make(chan error, numRequests*3)

	for i := 0; i < numRequests; i++ {
		wg.Add(3) 
		go func(i int) {
			defer wg.Done()
			if err := measureLatency(client, i, metrics); err != nil {
				errors <- err
			}
		}(i)
		go func(i int) {
			defer wg.Done()
			if err := measureTTFB(client, i, metrics); err != nil {
				errors <- err
			}
		}(i)
		go func(i int) {
			defer wg.Done()
			if err := measureThroughput(client, i, metrics); err != nil {
				errors <- err
			}
		}(i)
	}

	wg.Wait()
	close(errors)

	for err := range errors {
		log.Printf("Error during measurement: %v", err)
	}

	metrics.mu.Lock()
	var totalLatency time.Duration
	for _, latency := range metrics.latencies {
		totalLatency += latency
	}
	averageLatency := totalLatency / time.Duration(len(metrics.latencies))

	var totalTTFB time.Duration
    for _, ttfb := range metrics.ttfbs {
        totalTTFB += ttfb
    }
    averageTTFB := totalTTFB / time.Duration(len(metrics.ttfbs))

	fmt.Printf("\nResults:\n")
	fmt.Printf("Average Latency: %v\n", averageLatency)
	fmt.Printf("Average TTFB: %v\n", averageTTFB)
	fmt.Printf("Total Data Transferred: %d bytes\n", metrics.totalBytes)
	fmt.Printf("Total Time Taken: %v\n", metrics.totalTime)
	fmt.Printf("Average Throughput: %.2f bytes/sec\n", 
		float64(metrics.totalBytes)/metrics.totalTime.Seconds())
	metrics.mu.Unlock()
}