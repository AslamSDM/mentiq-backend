package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"net/http"
	"time"
)

// TestAnalyticsEndpoints tests all analytics endpoints and shows the data
func TestAnalyticsEndpoints() {
	baseURL := "http://localhost:8080"
	
	// Test credentials
	accountID := "test-account-123"
	projectID := "test-project-456"
	apiKey := accountID
	
	client := &http.Client{
		Timeout: 10 * time.Second,
	}
	
	fmt.Println("🔍 Step 1: Creating test events...")
	
	// Create some test events first
	testEvents := []map[string]interface{}{
		{
			"event_type": "page_view",
			"user_id":    "user1",
			"session_id": "session1",
			"properties": map[string]string{
				"path": "/home",
				"title": "Home Page",
			},
		},
		{
			"event_type": "page_view", 
			"user_id":    "user2",
			"session_id": "session2",
			"properties": map[string]string{
				"path": "/about",
				"title": "About Page",
			},
		},
		{
			"event_type": "click",
			"user_id":    "user1",
			"session_id": "session1",
			"properties": map[string]string{
				"button": "signup",
				"location": "header",
			},
		},
		{
			"event_type": "page_view",
			"user_id":    "user3", 
			"session_id": "session3",
			"properties": map[string]string{
				"path": "/pricing",
				"title": "Pricing Page",
			},
		},
		{
			"event_type": "conversion",
			"user_id":    "user1",
			"session_id": "session1",
			"properties": map[string]string{
				"type": "signup",
				"plan": "pro",
			},
		},
	}
	
	// Send test events
	for i, event := range testEvents {
		eventJSON, _ := json.Marshal(event)
		req, _ := http.NewRequest("POST", baseURL+"/api/v1/events", bytes.NewBuffer(eventJSON))
		req.Header.Set("Content-Type", "application/json")
		req.Header.Set("Authorization", "ApiKey "+apiKey)
		req.Header.Set("X-Project-ID", projectID)
		
		resp, err := client.Do(req)
		if err != nil {
			fmt.Printf("   ❌ Failed to send event %d: %v\n", i+1, err)
			continue
		}
		fmt.Printf("   ✅ Event %d sent - Status: %d\n", i+1, resp.StatusCode)
		resp.Body.Close()
	}
	
	fmt.Println("\n🔍 Step 2: Waiting for events to be processed...")
	time.Sleep(2 * time.Second)
	
	fmt.Println("\n🔍 Step 3: Testing Analytics Endpoints...")
	
	// Test Health Check
	fmt.Println("🔍 Testing Health Check...")
	resp, err := client.Get(baseURL + "/health")
	if err != nil {
		fmt.Printf("   ❌ Health Check failed: %v\n", err)
	} else {
		fmt.Printf("   ✅ Health Check - Status: %d\n", resp.StatusCode)
		resp.Body.Close()
	}
	
	// Test Dashboard
	fmt.Println("🔍 Testing Dashboard...")
	req, _ := http.NewRequest("GET", baseURL+"/api/v1/dashboard", nil)
	req.Header.Set("Authorization", "ApiKey "+apiKey)
	req.Header.Set("X-Project-ID", projectID)
	
	resp, err = client.Do(req)
	if err != nil {
		fmt.Printf("   ❌ Dashboard failed: %v\n", err)
	} else {
		fmt.Printf("   ✅ Dashboard - Status: %d\n", resp.StatusCode)
		
		var dashboardData map[string]interface{}
		json.NewDecoder(resp.Body).Decode(&dashboardData)
		resp.Body.Close()
		
		if overview, ok := dashboardData["overview"].(map[string]interface{}); ok {
			fmt.Printf("      📊 Events today: %.0f\n", overview["total_events_today"])
			fmt.Printf("      👥 Users today: %.0f\n", overview["unique_users_today"])
		}
		
		if userMetrics, ok := dashboardData["user_metrics"].(map[string]interface{}); ok {
			fmt.Printf("      📈 DAU: %.0f, WAU: %.0f, MAU: %.0f\n", 
				userMetrics["dau"], userMetrics["wau"], userMetrics["mau"])
		}
		
		if pageMetrics, ok := dashboardData["page_metrics"].(map[string]interface{}); ok {
			fmt.Printf("      📄 Page views today: %.0f\n", pageMetrics["page_views_today"])
		}
	}
	
	// Test Analytics - All Metrics
	fmt.Println("🔍 Testing Analytics - All Metrics...")
	req, _ = http.NewRequest("GET", baseURL+"/api/v1/analytics", nil)
	req.Header.Set("Authorization", "ApiKey "+apiKey)
	req.Header.Set("X-Project-ID", projectID)
	
	resp, err = client.Do(req)
	if err != nil {
		fmt.Printf("   ❌ Analytics failed: %v\n", err)
	} else {
		fmt.Printf("   ✅ Analytics - All Metrics - Status: %d\n", resp.StatusCode)
		
		var analyticsData map[string]interface{}
		json.NewDecoder(resp.Body).Decode(&analyticsData)
		resp.Body.Close()
		
		if results, ok := analyticsData["results"].([]interface{}); ok {
			fmt.Printf("      📊 Metrics calculated: %d\n", len(results))
			for _, result := range results {
				if r, ok := result.(map[string]interface{}); ok {
					fmt.Printf("         • %s: %v\n", r["metric"], r["value"])
				}
			}
		}
	}
	
	// Test Analytics - User Metrics Only
	fmt.Println("🔍 Testing Analytics - User Metrics...")
	req, _ = http.NewRequest("GET", baseURL+"/api/v1/analytics?metrics=dau,wau,mau", nil)
	req.Header.Set("Authorization", "ApiKey "+apiKey)
	req.Header.Set("X-Project-ID", projectID)
	
	resp, err = client.Do(req)
	if err != nil {
		fmt.Printf("   ❌ Analytics User Metrics failed: %v\n", err)
	} else {
		fmt.Printf("   ✅ Analytics - User Metrics - Status: %d\n", resp.StatusCode)
		resp.Body.Close()
	}
	
	// Test Analytics - Page Views
	fmt.Println("🔍 Testing Analytics - Page Views...")
	req, _ = http.NewRequest("GET", baseURL+"/api/v1/analytics?metrics=page_views", nil)
	req.Header.Set("Authorization", "ApiKey "+apiKey)
	req.Header.Set("X-Project-ID", projectID)
	
	resp, err = client.Do(req)
	if err != nil {
		fmt.Printf("   ❌ Analytics Page Views failed: %v\n", err)
	} else {
		fmt.Printf("   ✅ Analytics - Page Views - Status: %d\n", resp.StatusCode)
		
		var analyticsData map[string]interface{}
		json.NewDecoder(resp.Body).Decode(&analyticsData)
		resp.Body.Close()
		
		if results, ok := analyticsData["results"].([]interface{}); ok {
			for _, result := range results {
				if r, ok := result.(map[string]interface{}); ok {
					if r["metric"] == "page_views" {
						fmt.Printf("      📄 Total page views: %v\n", r["value"])
						if breakdown, ok := r["breakdown"].(map[string]interface{}); ok {
							fmt.Printf("      📄 Page breakdown:\n")
							for path, count := range breakdown {
								fmt.Printf("         • %s: %v views\n", path, count)
							}
						}
					}
				}
			}
		}
	}
	
	// Test User Metrics Endpoint
	fmt.Println("🔍 Testing User Metrics...")
	req, _ = http.NewRequest("GET", baseURL+"/api/v1/user-metrics", nil)
	req.Header.Set("Authorization", "ApiKey "+apiKey)
	req.Header.Set("X-Project-ID", projectID)
	
	resp, err = client.Do(req)
	if err != nil {
		fmt.Printf("   ❌ User Metrics failed: %v\n", err)
	} else {
		fmt.Printf("   ✅ User Metrics - Status: %d\n", resp.StatusCode)
		resp.Body.Close()
	}
	
	// Test User Metrics with Weekly Grouping
	fmt.Println("🔍 Testing User Metrics - Weekly...")
	req, _ = http.NewRequest("GET", baseURL+"/api/v1/user-metrics?group_by=week", nil)
	req.Header.Set("Authorization", "ApiKey "+apiKey)
	req.Header.Set("X-Project-ID", projectID)
	
	resp, err = client.Do(req)
	if err != nil {
		fmt.Printf("   ❌ User Metrics Weekly failed: %v\n", err)
	} else {
		fmt.Printf("   ✅ User Metrics - Weekly - Status: %d\n", resp.StatusCode)
		resp.Body.Close()
	}
	
	// Test Real-time
	fmt.Println("🔍 Testing Real-time...")
	req, _ = http.NewRequest("GET", baseURL+"/api/v1/realtime", nil)
	req.Header.Set("Authorization", "ApiKey "+apiKey)
	req.Header.Set("X-Project-ID", projectID)
	
	resp, err = client.Do(req)
	if err != nil {
		fmt.Printf("   ❌ Real-time failed: %v\n", err)
	} else {
		fmt.Printf("   ✅ Real-time - Status: %d\n", resp.StatusCode)
		resp.Body.Close()
	}
	
	// Test Flush Cache with shorter timeout
	fmt.Println("🔍 Testing Flush Cache...")
	req, _ = http.NewRequest("POST", baseURL+"/api/v1/flush-cache", nil)
	req.Header.Set("Authorization", "ApiKey "+apiKey)
	req.Header.Set("X-Project-ID", projectID)
	
	// Use a shorter timeout client for flush cache
	shortClient := &http.Client{
		Timeout: 5 * time.Second,
	}
	
	resp, err = shortClient.Do(req)
	if err != nil {
		fmt.Printf("   ❌ Flush Cache failed: %v\n", err)
	} else {
		fmt.Printf("   ✅ Flush Cache - Status: %d\n", resp.StatusCode)
		
		var flushData map[string]interface{}
		json.NewDecoder(resp.Body).Decode(&flushData)
		resp.Body.Close()
		
		fmt.Printf("      🗂️  Cache flush result: %s\n", flushData["message"])
	}
	
	fmt.Println("\n✅ Analytics testing completed!")
}
