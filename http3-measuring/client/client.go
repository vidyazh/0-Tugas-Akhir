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

func latencyMetric(experimentID string, latency time.Duration) {
	go func() {
		for {
			latencyHist.WithLabelValues(experimentID).Observe(latency.Seconds())
			time.Sleep(2 * time.Second)
		}
	}()
}

func throughputMetric(experimentID string, bytesReceived int64) {
	go func() {
		for {
			throughput.WithLabelValues(experimentID).Add(float64(bytesReceived))
			time.Sleep(2 * time.Second)
		}
	}()
}

var (
    throughput = prometheus.NewCounterVec(
		prometheus.CounterOpts{
			Name: "client_throughput_bytes_total",
			Help: "Total number of bytes received by the client",
		},
		[]string{"experiment_id"},
	)
	latencyHist = prometheus.NewHistogramVec(
		prometheus.HistogramOpts{
			Name:    "client_average_latency_seconds",
			Help:    "Histogram of client latency",
			Buckets: prometheus.DefBuckets,
		},
		[]string{"experiment_id"},
	)
)

func init() {
    prometheus.MustRegister(throughput)
	prometheus.MustRegister(latencyHist)
}

type Metrics struct {
	latencies    []time.Duration
	totalBytes   int64
	totalTime    time.Duration
	stopChannels []chan bool
	mu           sync.Mutex
}

func NewMetrics() *Metrics {
	return &Metrics{
		latencies:    make([]time.Duration, 0),
		stopChannels: make([]chan bool, 0),
	}
}

func (m *Metrics) Stop() {
	for _, ch := range m.stopChannels {
		ch <- true
	}
}

func (m *Metrics) startMetricCollection(experimentID string, metricType string, value float64) {
	stopCh := make(chan bool)
	m.mu.Lock()
	m.stopChannels = append(m.stopChannels, stopCh)
	m.mu.Unlock()

	go func() {
		ticker := time.NewTicker(2 * time.Second)
		defer ticker.Stop()

		for {
			select {
			case <-stopCh:
				return
			case <-ticker.C:
				switch metricType {
				case "latency":
					latencyHist.WithLabelValues(experimentID).Observe(value)
				case "throughput":
					throughput.WithLabelValues(experimentID).Add(value)
				}
			}
		}
	}()
}

func measureLatency(client *http.Client, reqID int, metrics *Metrics) error {
	fmt.Printf("Starting request %d\n", reqID)
	startTime := time.Now()

	resp, err := client.Get("https://localhost:443")
	if err != nil {
		return fmt.Errorf("error making request %d: %v", reqID, err)
	}
	defer resp.Body.Close()

	// Read entire body to ensure complete transfer
	_, err = io.Copy(io.Discard, resp.Body)
	if err != nil {
		return fmt.Errorf("error reading response body %d: %v", reqID, err)
	}

	latency := time.Since(startTime)
	
	metrics.mu.Lock()
	metrics.latencies = append(metrics.latencies, latency)
	metrics.mu.Unlock()

	experimentID := "experiment_1"
	metrics.startMetricCollection(experimentID, "latency", latency.Seconds())

	fmt.Printf("Response %d (Latency: %s) | %s | %s |\n", reqID, latency, resp.Status, resp.Proto)
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

	// Actually read the body and count bytes
	bytesReceived, err := io.Copy(io.Discard, resp.Body)
	if err != nil {
		return fmt.Errorf("error reading response body %d: %v", reqID, err)
	}

	duration := time.Since(startTime)
	
	metrics.mu.Lock()
	metrics.totalBytes += bytesReceived
	metrics.totalTime += duration
	metrics.mu.Unlock()

	experimentID := "experiment_1"
	metrics.startMetricCollection(experimentID, "throughput", float64(bytesReceived))

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

	// Try 0-RTT request
	req, err := http.NewRequest(http3.MethodGet0RTT, "https://localhost:443", nil)
	if err != nil {
		log.Printf("Failed to create 0-RTT request: %v", err)
		return
	}
	tr.RoundTrip(req)

	client := &http.Client{Transport: tr}
	numRequests := 3
	metrics := NewMetrics()
	defer metrics.Stop() // Clean up metric collection goroutines

	var wg sync.WaitGroup
	errors := make(chan error, numRequests*2) // Buffer for both latency and throughput errors

	for i := 0; i < numRequests; i++ {
		wg.Add(2) // One for latency, one for throughput
		go func(i int) {
			defer wg.Done()
			if err := measureLatency(client, i, metrics); err != nil {
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

	// Check for any errors that occurred
	for err := range errors {
		log.Printf("Error during measurement: %v", err)
	}

	// Calculate and display results
	metrics.mu.Lock()
	var totalLatency time.Duration
	for _, latency := range metrics.latencies {
		totalLatency += latency
	}
	averageLatency := totalLatency / time.Duration(len(metrics.latencies))

	fmt.Printf("\nResults:\n")
	fmt.Printf("Average Latency: %v\n", averageLatency)
	fmt.Printf("Total Data Transferred: %d bytes\n", metrics.totalBytes)
	fmt.Printf("Total Time Taken: %v\n", metrics.totalTime)
	fmt.Printf("Average Throughput: %.2f bytes/sec\n", 
		float64(metrics.totalBytes)/metrics.totalTime.Seconds())
	metrics.mu.Unlock()
}