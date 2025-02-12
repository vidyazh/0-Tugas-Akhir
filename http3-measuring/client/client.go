package main

import (
	"crypto/tls"
	"fmt"
	"io"
	"log"
	"net/http"
	"sync"
	"time"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/quic-go/quic-go"
	"github.com/quic-go/quic-go/http3"
)

var (
    requestDuration = prometheus.NewHistogramVec(
        prometheus.HistogramOpts{
            Name:    "client_request_duration_seconds",
            Help:    "Histogram of client request durations.",
            Buckets: prometheus.DefBuckets,
        },
        []string{"request_id"},
    )
)

func init() {
    prometheus.MustRegister(requestDuration)
}

func recordClientMetrics(duration time.Duration, requestID int) {
    requestDuration.WithLabelValues(fmt.Sprintf("%d", requestID)).Observe(duration.Seconds())
}

func measureRequestResponse(client *http.Client, reqID int, latencies *[]time.Duration) {
	fmt.Printf("Starting request %d\n", reqID)

	startTime := time.Now()

	// performa request
	resp, err := client.Get("https://localhost:443")
	if err != nil {
		log.Printf("Error making request %d: %v", reqID, err)
		return
	}
	defer resp.Body.Close()

	// kalkulasi latensi
	latency := time.Since(startTime)
	*latencies = append(*latencies, latency)
	fmt.Printf("Request %d latency: %v\n", reqID, latency)

	recordClientMetrics(latency, reqID)

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		log.Printf("Error reading response %d: %v", reqID, err)
		return
	}

	fmt.Printf("Response %d (Status: %s, Protocol: %s): %s\n", reqID, resp.Status, resp.Proto, string(body))
}

func measureThroughput(client *http.Client, reqID int, totalBytes *int64, totalTime *time.Duration) {
	fmt.Printf("Starting throughput measurement for request %d\n", reqID)

	startTime := time.Now()

	// performa the request
	resp, err := client.Get("https://localhost:443")
	if err != nil {
		log.Printf("Error making request %d: %v", reqID, err)
		return
	}
	defer resp.Body.Close()

	// tracking banyak bytes
	bytesReceived := resp.ContentLength
	*totalBytes += bytesReceived
	*totalTime += time.Since(startTime)

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		log.Printf("Error reading response %d: %v", reqID, err)
		return
	}

	fmt.Printf("Response %d (Status: %s, Protocol: %s): %s\n", reqID, resp.Status, resp.Proto, string(body))
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
	req, err := http.NewRequest(http3.MethodGet0RTT, "https://localhost:443", nil)
	if err != nil {
		log.Printf("Failed to create 0-RTT request for %s: %v", "https://localhost:443", err)
		return
	}

	tr.RoundTrip(req)
	
    defer tr.Close()

	client := &http.Client{Transport: tr}

	numRequests := 1000

	totalBytes := int64(0)
	totalTime := time.Duration(0)
	latencies := []time.Duration{}

	var wg sync.WaitGroup
	for i := 0; i < numRequests; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			measureRequestResponse(client, i, &latencies)
			measureThroughput(client, i, &totalBytes, &totalTime)
		}(i)
	}

	wg.Wait()

	// // memuat requests secara concurrent
	// for i := 0; i < numRequests; i++ {
	// 	// ukur latensi
	// 	go measureRequestResponse(client, i, &latencies)

	// 	// ukur throughput
	// 	go measureThroughput(client, i, &totalBytes, &totalTime)
	// }

	// // tunggu semua request selesai
	// time.Sleep(5 * time.Second)

	// menghitung rata-rata latensi
	var totalLatency time.Duration
	for _, latency := range latencies {
		totalLatency += latency
	}
	averageLatency := totalLatency / time.Duration(len(latencies))

	// hasil rata-rata latensi
	fmt.Printf("Average Latency: %v\n", averageLatency)

	// hasil throughput
	fmt.Printf("Total Data Transferred: %d bytes\n", totalBytes)
	fmt.Printf("Total Time Taken: %v\n", totalTime)
	fmt.Printf("Throughput: %.2f bytes/sec\n", float64(totalBytes)/totalTime.Seconds())
}
