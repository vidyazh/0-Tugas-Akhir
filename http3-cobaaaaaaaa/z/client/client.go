package main

import (
	"crypto/tls"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	"os"
	"sync"
	"time"

	"github.com/quic-go/quic-go"
	"github.com/quic-go/quic-go/http3"
)

// Metrics struct untuk menyimpan hasil pengukuran
type ClientMetrics struct {
    SessionID           string
    ConnectionTime      time.Duration
    RTT                 time.Duration
    Is0RTT             bool
    SecurityOverhead    time.Duration
    ResourceConsumption struct {
        MemoryUsage uint64
        CPUUsage    float64
    }
}

func main() {
	results := make(chan *ClientMetrics, 1000)
    var wg sync.WaitGroup

	tr := &http3.Transport{
		TLSClientConfig: &tls.Config{
			InsecureSkipVerify: true,
			ClientSessionCache: tls.NewLRUClientSessionCache(100),
            SessionTicketKey: [32]byte{},
		},
		QUICConfig: &quic.Config{
			Allow0RTT: true,
            MaxIdleTimeout: 30 * time.Second,
            KeepAlivePeriod: 10,
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
	scenarios := []struct {
        name     string
        requests int
        delay    time.Duration
    }{
        {"Initial Connection", 1, 0},
        {"0-RTT Resumed", 10, 500 * time.Millisecond},
        {"Heavy Load", 50, 10 * time.Millisecond},
    }

    for _, scenario := range scenarios {
        log.Printf("Running scenario: %s", scenario.name)
        wg.Add(scenario.requests)

        for i := 0; i < scenario.requests; i++ {
            go func(reqID int) {
                defer wg.Done()
                metrics := runTest(client, reqID, scenario.name)
                results <- metrics
                time.Sleep(scenario.delay)
            }(i)
        }
    }

    // Collect and analyze results
    go func() {
        wg.Wait()
        close(results)
    }()

    analyzeResults(results)
}

func runTest(client *http.Client, reqID int, scenarioName string) *ClientMetrics {
    metrics := &ClientMetrics{
        SessionID: fmt.Sprintf("%s-%d", scenarioName, reqID),
    }

    startTime := time.Now()

    log.Printf("Attempting request %s", metrics.SessionID)

    // Create and send request
    req, err := http.NewRequest(http3.MethodGet0RTT, "https://localhost:443", nil)
    if err != nil {
        log.Printf("Error creating request: %v", err)
        return metrics
    }

    req.Header.Set("X-Session-ID", metrics.SessionID)

    resp, err := client.Do(req)
    if err != nil {
        log.Printf("Error making request %s: %v", metrics.SessionID, err)
        return metrics
    }
    defer resp.Body.Close()

    log.Printf("Response for %s: Proto=%s, 0-RTT=%s", 
    metrics.SessionID,
    resp.Proto,
    resp.Header.Get("X-Is-0RTT"))

    metrics.ConnectionTime = time.Since(startTime)
    metrics.Is0RTT = resp.Header.Get("X-Is-0RTT") == "true"

    // Read response
    _, err = io.Copy(io.Discard, resp.Body)
    if err != nil {
        log.Printf("Error reading response: %v", err)
    }

    metrics.RTT = time.Since(startTime)
    return metrics
}

func analyzeResults(results chan *ClientMetrics) {
    var metrics []*ClientMetrics
    var totalConn, total0RTT, totalNormal int
    var avg0RTTTime, avgNormalTime time.Duration

    for metric := range results {
        metrics = append(metrics, metric)
        totalConn++
        
        if metric.Is0RTT {
            total0RTT++
            avg0RTTTime += metric.ConnectionTime
        } else {
            totalNormal++
            avgNormalTime += metric.ConnectionTime
        }
    }

    if total0RTT > 0 {
        avg0RTTTime = avg0RTTTime / time.Duration(total0RTT)
    }
    if totalNormal > 0 {
        avgNormalTime = avgNormalTime / time.Duration(totalNormal)
    }

    // Print results
    fmt.Printf("\nTest Results:\n")
    fmt.Printf("Total Connections: %d\n", totalConn)
    fmt.Printf("0-RTT Connections: %d (%.2f%%)\n", total0RTT, float64(total0RTT)/float64(totalConn)*100)

    fmt.Printf("Average 0-RTT Connection Time: %v\n", avg0RTTTime)

    if totalNormal > 0 {
        fmt.Printf("Average Normal Connection Time: %v\n", avgNormalTime)
    } else {
        fmt.Println("No normal connections, skipping calculation.")
    }

    // Calculate Performance Improvement (if normal connections exist)
    if totalNormal > 0 && total0RTT > 0 {
        performanceImprovement := (float64(avgNormalTime-avg0RTTTime) / float64(avgNormalTime)) * 100
        fmt.Printf("Performance Improvement: %.2f%%\n", performanceImprovement)
    } else if totalNormal > 0 && total0RTT == 0 {
        fmt.Println("No 0-RTT connections to compare performance.")
    } else {
        // Handle cases where performance improvement isn't applicable
        fmt.Println("Performance Improvement calculation not applicable.")
    }
    // Save detailed results
    saveResults(metrics)
}

func saveResults(metrics []*ClientMetrics) {
    file, err := json.MarshalIndent(metrics, "", "  ")
    if err != nil {
        log.Printf("Error marshaling results: %v", err)
        return
    }

    // Save to file with timestamp
    filename := fmt.Sprintf("results_%s.json", time.Now().Format("20060102_150405"))
    if err := os.WriteFile(filename, file, 0644); err != nil {
        log.Printf("Error saving results: %v", err)
    }
}