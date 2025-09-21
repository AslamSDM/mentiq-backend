package main

import (
	"encoding/json"
	"fmt"
	"net/http"
	"time"
)

// ViewAnalyticsData fetches and displays current analytics data
func ViewAnalyticsData() {
	baseURL := "http://localhost:8080"
	
	// Test credentials - these should match what your server expects
	accountID := "cmfu4slqu00009kc5am5gcns9"
	projectID := "cmfu4sm1y00019kc5j74httyf"
	apiKey := accountID
	
	client := &http.Client{
		Timeout: 1000 * time.Second,
	}
	
	fmt.Println("📊 Analytics Dashboard Data")
	fmt.Println("==========================")
	
	// Get Dashboard Data
	req, _ := http.NewRequest("GET", baseURL+"/api/v1/dashboard", nil)
	req.Header.Set("Authorization", "ApiKey "+apiKey)
	req.Header.Set("X-Project-ID", projectID)
	
	resp, err := client.Do(req)
	if err != nil {
		fmt.Printf("❌ Failed to fetch dashboard: %v\n", err)
		return
	}
	defer resp.Body.Close()
	
	var dashboardData map[string]interface{}
	if err := json.NewDecoder(resp.Body).Decode(&dashboardData); err != nil {
		fmt.Printf("❌ Failed to decode dashboard data: %v\n", err)
		return
	}
	
	// Display Overview
	if overview, ok := dashboardData["overview"].(map[string]interface{}); ok {
		fmt.Println("\n📈 Overview:")
		fmt.Printf("   Events Today: %.0f\n", overview["total_events_today"])
		fmt.Printf("   Events Yesterday: %.0f\n", overview["total_events_yesterday"])
		fmt.Printf("   Users Today: %.0f\n", overview["unique_users_today"])
		fmt.Printf("   Users Yesterday: %.0f\n", overview["unique_users_yesterday"])
		fmt.Printf("   Event Growth Rate: %s\n", overview["event_growth_rate"])
		fmt.Printf("   User Growth Rate: %s\n", overview["user_growth_rate"])
	}
	
	// Display User Metrics
	if userMetrics, ok := dashboardData["user_metrics"].(map[string]interface{}); ok {
		fmt.Println("\n👥 User Metrics:")
		fmt.Printf("   DAU (Daily Active Users): %.0f\n", userMetrics["dau"])
		fmt.Printf("   WAU (Weekly Active Users): %.0f\n", userMetrics["wau"])
		fmt.Printf("   MAU (Monthly Active Users): %.0f\n", userMetrics["mau"])
	}
	
	// Display Page Metrics
	if pageMetrics, ok := dashboardData["page_metrics"].(map[string]interface{}); ok {
		fmt.Println("\n📄 Page Metrics:")
		fmt.Printf("   Page Views Today: %.0f\n", pageMetrics["page_views_today"])
		fmt.Printf("   Page Views Yesterday: %.0f\n", pageMetrics["page_views_yesterday"])
		fmt.Printf("   Total Page Views: %.0f\n", pageMetrics["total_page_views"])
	}
	
	fmt.Println("\n📊 Detailed Analytics")
	fmt.Println("=====================")
	
	// Get Detailed Analytics
	req, _ = http.NewRequest("GET", baseURL+"/api/v1/analytics?metrics=page_views,top_events,unique_users,total_events", nil)
	req.Header.Set("Authorization", "ApiKey "+apiKey)
	req.Header.Set("X-Project-ID", projectID)
	
	resp, err = client.Do(req)
	if err != nil {
		fmt.Printf("❌ Failed to fetch analytics: %v\n", err)
		return
	}
	defer resp.Body.Close()
	
	var analyticsData map[string]interface{}
	if err := json.NewDecoder(resp.Body).Decode(&analyticsData); err != nil {
		fmt.Printf("❌ Failed to decode analytics data: %v\n", err)
		return
	}
	
	if results, ok := analyticsData["results"].([]interface{}); ok {
		for _, result := range results {
			if r, ok := result.(map[string]interface{}); ok {
				metric := r["metric"].(string)
				value := r["value"]
				
				switch metric {
				case "total_events":
					fmt.Printf("\n🎯 Total Events: %v\n", value)
				case "unique_users":
					fmt.Printf("👤 Unique Users: %v\n", value)
				case "page_views":
					fmt.Printf("📄 Page Views: %v\n", value)
					if breakdown, ok := r["breakdown"].(map[string]interface{}); ok && len(breakdown) > 0 {
						fmt.Println("   Page Breakdown:")
						for path, count := range breakdown {
							fmt.Printf("     • %s: %v views\n", path, count)
						}
					}
				case "top_events":
					fmt.Printf("🔥 Top Events: %v\n", value)
					if breakdown, ok := r["breakdown"].(map[string]interface{}); ok && len(breakdown) > 0 {
						fmt.Println("   Event Breakdown:")
						for eventType, count := range breakdown {
							fmt.Printf("     • %s: %v occurrences\n", eventType, count)
						}
					}
				}
			}
		}
	}
	
	// Show metadata
	if meta, ok := analyticsData["meta"].(map[string]interface{}); ok {
		fmt.Println("\n📋 Query Info:")
		fmt.Printf("   Total Events Processed: %.0f\n", meta["total_events"])
		fmt.Printf("   Processing Time: %v\n", meta["processing_time_ms"])
		fmt.Printf("   Date Range: %s\n", meta["date_range"])
	}
	
	fmt.Println("\n🔄 Cache Status")
	fmt.Println("===============")
	
	// Check cache status via flush endpoint (just to see current cache size)
	req, _ = http.NewRequest("POST", baseURL+"/api/v1/flush-cache", nil)
	req.Header.Set("Authorization", "ApiKey "+apiKey)
	req.Header.Set("X-Project-ID", projectID)
	
	resp, err = client.Do(req)
	if err != nil {
		fmt.Printf("❌ Failed to check cache status: %v\n", err)
	} else {
		defer resp.Body.Close()
		var cacheData map[string]interface{}
		if err := json.NewDecoder(resp.Body).Decode(&cacheData); err == nil {
			fmt.Printf("📦 Cache Status: %s\n", cacheData["message"])
			if eventsProcessed, ok := cacheData["events_processed"]; ok {
				fmt.Printf("📊 Events in Cache: %v\n", eventsProcessed)
			}
		}
	}
}

func main() {
	ViewAnalyticsData()
}