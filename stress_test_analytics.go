package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"math/rand"
	"net/http"
	"sync"
	"time"
)

// StressTestConfig holds configuration for the stress test
type StressTestConfig struct {
	ServerURL           string
	NumEventIngestors   int
	NumDashboardCallers int
	TestDurationMinutes int
	EventsPerSecond     int
	DashboardCallsPerMinute int
	ApiKey              string
	ProjectID           string
	UseUniqueAccounts   bool
}

// UserAccount represents a unique user account with its own project
type UserAccount struct {
	AccountID string
	ProjectID string
	UserPool  []string // Pool of user IDs for this account
}

// EventIngesterStats tracks statistics for event ingestion
type EventIngesterStats struct {
	TotalEvents   int
	SuccessEvents int
	FailedEvents  int
	AvgLatency    time.Duration
	mutex         sync.Mutex
}

// DashboardCallerStats tracks statistics for dashboard calls
type DashboardCallerStats struct {
	TotalCalls     int
	SuccessfulCalls int
	FailedCalls    int
	AvgLatency     time.Duration
	CacheHits      int
	mutex          sync.Mutex
}

// StressTestEvent represents an event for testing
type StressTestEvent struct {
	EventID    string                 `json:"event_id"`
	EventType  string                 `json:"event_type"`
	UserID     string                 `json:"user_id"`
	SessionID  string                 `json:"session_id"`
	Timestamp  time.Time              `json:"timestamp"`
	Properties map[string]interface{} `json:"properties"`
}



func checkServerHealth(serverURL string) bool {
	resp, err := http.Get(serverURL + "/health")
	if err != nil {
		return false
	}
	defer resp.Body.Close()
	return resp.StatusCode == 200
}

func runEventIngester(workerID int, config StressTestConfig, stats *EventIngesterStats, stopChan <-chan bool) {
	fmt.Printf("🔄 Event Ingester %d started\n", workerID)
	
	// Increased timeout and optimized client settings for high load
	client := &http.Client{
		Timeout: 30 * time.Second,
		Transport: &http.Transport{
			MaxIdleConns:        100,
			MaxIdleConnsPerHost: 100,
			IdleConnTimeout:     90 * time.Second,
		},
	}
	ticker := time.NewTicker(time.Duration(1000/config.EventsPerSecond) * time.Millisecond)
	defer ticker.Stop()

	var userAccounts []UserAccount
	if config.UseUniqueAccounts {
		// Generate unique accounts for this worker (simulating different companies/users)
		numAccounts := 10 // Each worker simulates 10 different accounts
		userAccounts = generateUserAccounts(workerID, numAccounts)
		fmt.Printf("🏢 Worker %d simulating %d unique accounts\n", workerID, len(userAccounts))
	} else {
		// Use single account for all events but with many different users
		// This simulates a large company with many users under one account
		userAccounts = []UserAccount{{
			AccountID: config.ApiKey,
			ProjectID: config.ProjectID,
			UserPool:  generateUserIDs(1000 + workerID*100), // Different user pool per worker
		}}
		fmt.Printf("👥 Worker %d simulating %d users in account %s\n", workerID, len(userAccounts[0].UserPool), config.ApiKey[:12]+"...")
	}

	eventTypes := []string{"page_view", "click", "scroll", "form_submit", "video_play", "download", "signup", "purchase"}

	for {
		select {
		case <-stopChan:
			fmt.Printf("✅ Event Ingester %d stopped\n", workerID)
			return
		case <-ticker.C:
			// Select random account for this event
			account := userAccounts[rand.Intn(len(userAccounts))]
			
			// Generate random event
			event := StressTestEvent{
				EventID:   fmt.Sprintf("evt_%d_%d", workerID, time.Now().UnixNano()),
				EventType: eventTypes[rand.Intn(len(eventTypes))],
				UserID:    account.UserPool[rand.Intn(len(account.UserPool))],
				SessionID: fmt.Sprintf("sess_%d_%d", workerID, rand.Intn(50)), // More sessions
				Timestamp: time.Now(),
				Properties: map[string]interface{}{
					"page":        fmt.Sprintf("/page-%d", rand.Intn(100)), // More pages
					"browser":     []string{"chrome", "firefox", "safari", "edge"}[rand.Intn(4)],
					"device":      []string{"desktop", "mobile", "tablet"}[rand.Intn(3)],
					"worker_id":   workerID,
					"user_tier":   []string{"free", "premium", "enterprise"}[rand.Intn(3)],
					"region":      []string{"us-east", "us-west", "eu", "asia"}[rand.Intn(4)],
					"account_id":  account.AccountID,
				},
			}

			// Send event using the account's credentials
			start := time.Now()
			success := sendEvent(client, config.ServerURL, account.AccountID, account.ProjectID, event)
			latency := time.Since(start)

			// Update statistics
			stats.mutex.Lock()
			stats.TotalEvents++
			if success {
				stats.SuccessEvents++
			} else {
				stats.FailedEvents++
			}
			// Simple moving average for latency
			stats.AvgLatency = (stats.AvgLatency*time.Duration(stats.TotalEvents-1) + latency) / time.Duration(stats.TotalEvents)
			stats.mutex.Unlock()
		}
	}
}

func runDashboardCaller(workerID int, config StressTestConfig, stats *DashboardCallerStats, stopChan <-chan bool) {
	fmt.Printf("📊 Dashboard Caller %d started\n", workerID)
	
	// Optimized client for dashboard calls
	client := &http.Client{
		Timeout: 60 * time.Second, // Longer timeout for dashboard calls
		Transport: &http.Transport{
			MaxIdleConns:        50,
			MaxIdleConnsPerHost: 50,
			IdleConnTimeout:     90 * time.Second,
		},
	}
	ticker := time.NewTicker(time.Duration(60/config.DashboardCallsPerMinute) * time.Second)
	defer ticker.Stop()

	var userAccounts []UserAccount
	if config.UseUniqueAccounts {
		// Generate unique accounts for this worker
		numAccounts := 5 // Dashboard callers use fewer accounts (they're more like admin users)
		userAccounts = generateUserAccounts(workerID+1000, numAccounts) // Offset to avoid collision with event ingestors
		fmt.Printf("📈 Dashboard Worker %d monitoring %d unique accounts\n", workerID, len(userAccounts))
	} else {
		// Use single account for all calls
		userAccounts = []UserAccount{{
			AccountID: config.ApiKey,
			ProjectID: config.ProjectID,
			UserPool:  []string{}, // Not needed for dashboard calls
		}}
	}

	endpoints := []string{"/api/v1/dashboard", "/api/v1/analytics", "/api/v1/user-metrics"}

	for {
		select {
		case <-stopChan:
			fmt.Printf("✅ Dashboard Caller %d stopped\n", workerID)
			return
		case <-ticker.C:
			// Select random account and endpoint
			account := userAccounts[rand.Intn(len(userAccounts))]
			endpoint := endpoints[rand.Intn(len(endpoints))]
			
			start := time.Now()
			success, isCacheHit := callDashboardEndpoint(client, config.ServerURL, account.AccountID, account.ProjectID, endpoint)
			latency := time.Since(start)

			// Update statistics
			stats.mutex.Lock()
			stats.TotalCalls++
			if success {
				stats.SuccessfulCalls++
				if isCacheHit {
					stats.CacheHits++
				}
			} else {
				stats.FailedCalls++
			}
			// Simple moving average for latency
			stats.AvgLatency = (stats.AvgLatency*time.Duration(stats.TotalCalls-1) + latency) / time.Duration(stats.TotalCalls)
			stats.mutex.Unlock()
		}
	}
}

func runStatsReporter(eventStats *EventIngesterStats, dashboardStats *DashboardCallerStats, stopChan <-chan bool) {
	ticker := time.NewTicker(10 * time.Second)
	defer ticker.Stop()

	for {
		select {
		case <-stopChan:
			return
		case <-ticker.C:
			printLiveStats(eventStats, dashboardStats)
		}
	}
}

func sendEvent(client *http.Client, serverURL, apiKey, projectID string, event StressTestEvent) bool {
	eventJSON, err := json.Marshal(event)
	if err != nil {
		return false
	}

	req, err := http.NewRequest("POST", serverURL+"/api/v1/events", bytes.NewBuffer(eventJSON))
	if err != nil {
		return false
	}

	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "ApiKey "+apiKey)
	req.Header.Set("X-Project-ID", projectID)

	resp, err := client.Do(req)
	if err != nil {
		return false
	}
	defer resp.Body.Close()

	return resp.StatusCode == 200
}

func callDashboardEndpoint(client *http.Client, serverURL, apiKey, projectID, endpoint string) (bool, bool) {
	req, err := http.NewRequest("GET", serverURL+endpoint, nil)
	if err != nil {
		return false, false
	}

	req.Header.Set("Authorization", "ApiKey "+apiKey)
	req.Header.Set("X-Project-ID", projectID)

	resp, err := client.Do(req)
	if err != nil {
		return false, false
	}
	defer resp.Body.Close()

	// Check if response came from cache (you might add a custom header for this)
	cacheHeader := resp.Header.Get("X-Cache-Status")
	isCacheHit := cacheHeader == "HIT"

	return resp.StatusCode == 200, isCacheHit
}

func generateUserIDs(count int) []string {
	userIDs := make([]string, count)
	for i := 0; i < count; i++ {
		userIDs[i] = fmt.Sprintf("user_%d_%d", rand.Intn(100000), time.Now().UnixNano()%1000000)
	}
	return userIDs
}

func generateUserAccounts(workerID, numAccounts int) []UserAccount {
	accounts := make([]UserAccount, numAccounts)
	for i := 0; i < numAccounts; i++ {
		// Generate realistic account and project IDs (similar to your format)
		accountID := fmt.Sprintf("cmfu%04x%04x%04x%04x%04x", 
			rand.Intn(65536), rand.Intn(65536), rand.Intn(65536), rand.Intn(65536), rand.Intn(65536))
		projectID := fmt.Sprintf("cmfu%04x%04x%04x%04x%04x", 
			rand.Intn(65536), rand.Intn(65536), rand.Intn(65536), rand.Intn(65536), rand.Intn(65536))
		
		accounts[i] = UserAccount{
			AccountID: accountID,
			ProjectID: projectID,
			UserPool:  generateUserIDs(100), // 100 users per account
		}
	}
	return accounts
}

func printLiveStats(eventStats *EventIngesterStats, dashboardStats *DashboardCallerStats) {
	eventStats.mutex.Lock()
	totalEvents := eventStats.TotalEvents
	successEvents := eventStats.SuccessEvents
	failedEvents := eventStats.FailedEvents
	avgEventLatency := eventStats.AvgLatency
	eventStats.mutex.Unlock()

	dashboardStats.mutex.Lock()
	totalCalls := dashboardStats.TotalCalls
	successfulCalls := dashboardStats.SuccessfulCalls
	failedCalls := dashboardStats.FailedCalls
	avgDashboardLatency := dashboardStats.AvgLatency
	cacheHits := dashboardStats.CacheHits
	dashboardStats.mutex.Unlock()

	fmt.Printf("\n📈 Live Statistics (Updated every 10s)\n")
	fmt.Printf("┌─────────────────────────────────────────────────────────────┐\n")
	fmt.Printf("│ Event Ingestion                                             │\n")
	fmt.Printf("│   Total Events: %d                                       │\n", totalEvents)
	fmt.Printf("│   Successful: %d (%.1f%%)                               │\n", 
		successEvents, float64(successEvents)/float64(max(totalEvents, 1))*100)
	fmt.Printf("│   Failed: %d (%.1f%%)                                   │\n", 
		failedEvents, float64(failedEvents)/float64(max(totalEvents, 1))*100)
	fmt.Printf("│   Avg Latency: %v                                          │\n", avgEventLatency)
	fmt.Printf("├─────────────────────────────────────────────────────────────┤\n")
	fmt.Printf("│ Dashboard Calls                                             │\n")
	fmt.Printf("│   Total Calls: %d                                        │\n", totalCalls)
	fmt.Printf("│   Successful: %d (%.1f%%)                               │\n", 
		successfulCalls, float64(successfulCalls)/float64(max(totalCalls, 1))*100)
	fmt.Printf("│   Failed: %d (%.1f%%)                                   │\n", 
		failedCalls, float64(failedCalls)/float64(max(totalCalls, 1))*100)
	fmt.Printf("│   Cache Hits: %d (%.1f%%)                               │\n", 
		cacheHits, float64(cacheHits)/float64(max(successfulCalls, 1))*100)
	fmt.Printf("│   Avg Latency: %v                                          │\n", avgDashboardLatency)
	fmt.Printf("└─────────────────────────────────────────────────────────────┘\n")
}

func printFinalResults(eventStats *EventIngesterStats, dashboardStats *DashboardCallerStats, config StressTestConfig) {
	eventStats.mutex.Lock()
	totalEvents := eventStats.TotalEvents
	successEvents := eventStats.SuccessEvents
	failedEvents := eventStats.FailedEvents
	avgEventLatency := eventStats.AvgLatency
	eventStats.mutex.Unlock()

	dashboardStats.mutex.Lock()
	totalCalls := dashboardStats.TotalCalls
	successfulCalls := dashboardStats.SuccessfulCalls
	failedCalls := dashboardStats.FailedCalls
	avgDashboardLatency := dashboardStats.AvgLatency
	cacheHits := dashboardStats.CacheHits
	dashboardStats.mutex.Unlock()

	fmt.Printf("\n🎯 Final Stress Test Results\n")
	fmt.Printf("══════════════════════════════════════════════════════════════\n")
	fmt.Printf("Test Duration: %d minutes\n", config.TestDurationMinutes)
	fmt.Printf("Workers: %d event ingestors, %d dashboard callers\n", 
		config.NumEventIngestors, config.NumDashboardCallers)
	fmt.Printf("\n📊 Event Ingestion Performance:\n")
	fmt.Printf("   Total Events Sent: %d\n", totalEvents)
	fmt.Printf("   Successful: %d (%.2f%%)\n", successEvents, 
		float64(successEvents)/float64(max(totalEvents, 1))*100)
	fmt.Printf("   Failed: %d (%.2f%%)\n", failedEvents, 
		float64(failedEvents)/float64(max(totalEvents, 1))*100)
	fmt.Printf("   Average Latency: %v\n", avgEventLatency)
	fmt.Printf("   Throughput: %.2f events/second\n", 
		float64(totalEvents)/float64(config.TestDurationMinutes*60))

	fmt.Printf("\n📈 Dashboard Performance:\n")
	fmt.Printf("   Total Dashboard Calls: %d\n", totalCalls)
	fmt.Printf("   Successful: %d (%.2f%%)\n", successfulCalls, 
		float64(successfulCalls)/float64(max(totalCalls, 1))*100)
	fmt.Printf("   Failed: %d (%.2f%%)\n", failedCalls, 
		float64(failedCalls)/float64(max(totalCalls, 1))*100)
	fmt.Printf("   Cache Hit Rate: %.2f%%\n", 
		float64(cacheHits)/float64(max(successfulCalls, 1))*100)
	fmt.Printf("   Average Latency: %v\n", avgDashboardLatency)

	// Performance analysis
	fmt.Printf("\n📋 Performance Analysis:\n")
	if avgEventLatency < 100*time.Millisecond {
		fmt.Printf("   ✅ Event ingestion latency is excellent (< 100ms)\n")
	} else if avgEventLatency < 500*time.Millisecond {
		fmt.Printf("   ⚠️  Event ingestion latency is acceptable (< 500ms)\n")
	} else {
		fmt.Printf("   ❌ Event ingestion latency is poor (> 500ms)\n")
	}

	if avgDashboardLatency < 1*time.Second {
		fmt.Printf("   ✅ Dashboard response time is excellent (< 1s)\n")
	} else if avgDashboardLatency < 5*time.Second {
		fmt.Printf("   ⚠️  Dashboard response time is acceptable (< 5s)\n")
	} else {
		fmt.Printf("   ❌ Dashboard response time is poor (> 5s)\n")
	}

	cacheHitRate := float64(cacheHits)/float64(max(successfulCalls, 1))*100
	if cacheHitRate > 80 {
		fmt.Printf("   ✅ Cache hit rate is excellent (%.1f%%)\n", cacheHitRate)
	} else if cacheHitRate > 50 {
		fmt.Printf("   ⚠️  Cache hit rate is good (%.1f%%)\n", cacheHitRate)
	} else {
		fmt.Printf("   ❌ Cache hit rate is low (%.1f%%) - consider increasing cache TTL\n", cacheHitRate)
	}

	successRate := float64(successEvents)/float64(max(totalEvents, 1))*100
	if successRate > 99 {
		fmt.Printf("   ✅ Event success rate is excellent (%.2f%%)\n", successRate)
	} else if successRate > 95 {
		fmt.Printf("   ⚠️  Event success rate is good (%.2f%%)\n", successRate)
	} else {
		fmt.Printf("   ❌ Event success rate is concerning (%.2f%%)\n", successRate)
	}

	fmt.Printf("══════════════════════════════════════════════════════════════\n")
}

func max(a, b int) int {
	if a > b {
		return a
	}
	return b
}