package main

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"log"
	"math/rand"
	"net/http"
	"os"
	"time"

	"github.com/joho/godotenv"
	"mentiq-backend/prisma/db"
)

type TestAccount struct {
	ID    string `json:"id"`
	Name  string `json:"name"`
	Email string `json:"email"`
}

type TestProject struct {
	ID        string `json:"id"`
	Name      string `json:"name"`
	AccountID string `json:"account_id"`
}

type TestEvent struct {
	EventType  string                 `json:"event_type"`
	UserID     string                 `json:"user_id,omitempty"`
	SessionID  string                 `json:"session_id,omitempty"`
	Properties map[string]interface{} `json:"properties,omitempty"`
	Timestamp  time.Time              `json:"timestamp,omitempty"`
}

func main() {
	// Load environment variables
	if err := godotenv.Load(".env.local"); err != nil {
		if err := godotenv.Load(".env"); err != nil {
			log.Printf("Warning: Could not load environment files: %v", err)
		}
	}

	// Initialize database client
	dbClient := db.NewClient()
	if err := dbClient.Connect(); err != nil {
		log.Fatalf("Failed to connect to database: %v", err)
	}
	defer dbClient.Disconnect()

	fmt.Println("🚀 Starting Analytics Test Script")
	fmt.Println("================================")

	// Step 1: Create test account
	fmt.Println("\n📝 Step 1: Creating test account...")
	account, err := createTestAccount(dbClient)
	if err != nil {
		log.Fatalf("Failed to create test account: %v", err)
	}
	fmt.Printf("✅ Created account: %s (ID: %s)\n", account.Name, account.ID)

	// Step 2: Create test project
	fmt.Println("\n📁 Step 2: Creating test project...")
	project, err := createTestProject(dbClient, account.ID)
	if err != nil {
		log.Fatalf("Failed to create test project: %v", err)
	}
	fmt.Printf("✅ Created project: %s (ID: %s)\n", project.Name, project.ID)

	// Step 3: Generate and send test events
	fmt.Println("\n📊 Step 3: Generating test events...")
	if err := generateTestEvents(account.ID, project.ID); err != nil {
		log.Fatalf("Failed to generate test events: %v", err)
	}

	// Step 4: Test analytics endpoints
	fmt.Println("\n🔍 Step 4: Testing analytics endpoints...")
	if err := testAnalyticsEndpoints(account.ID, project.ID); err != nil {
		log.Fatalf("Failed to test analytics: %v", err)
	}

	// Step 5: Cleanup (optional)
	fmt.Println("\n🧹 Step 5: Cleanup...")
	if err := cleanup(dbClient, account.ID, project.ID); err != nil {
		log.Printf("Warning: Cleanup failed: %v", err)
	}

	fmt.Println("\n🎉 Analytics test completed successfully!")
}

func createTestAccount(dbClient *db.PrismaClient) (*TestAccount, error) {
	timestamp := time.Now().Unix()
	
	account, err := dbClient.Account.CreateOne(
		db.Account.Name.Set(fmt.Sprintf("Test Account %d", timestamp)),
		db.Account.Email.Set(fmt.Sprintf("test-%d@analytics.com", timestamp)),
		db.Account.Password.Set("test-password"),
	).Exec(context.Background())

	if err != nil {
		return nil, err
	}

	return &TestAccount{
		ID:    account.ID,
		Name:  account.Name,
		Email: account.Email,
	}, nil
}

func createTestProject(dbClient *db.PrismaClient, accountID string) (*TestProject, error) {
	timestamp := time.Now().Unix()
	
	project, err := dbClient.Project.CreateOne(
		db.Project.Name.Set(fmt.Sprintf("Test Project %d", timestamp)),
		db.Project.Account.Link(db.Account.ID.Equals(accountID)),
	).Exec(context.Background())

	if err != nil {
		return nil, err
	}

	return &TestProject{
		ID:        project.ID,
		Name:      project.Name,
		AccountID: accountID,
	}, nil
}

func generateTestEvents(accountID, projectID string) error {
	baseURL := getServerURL()
	
	// Define test users
	users := []string{"user1", "user2", "user3", "user4", "user5"}
	sessions := []string{"session1", "session2", "session3", "session4", "session5"}
	pages := []string{"/", "/home", "/about", "/products", "/contact", "/pricing", "/features"}
	
	// Generate events for the last 30 days
	now := time.Now()
	events := make([]TestEvent, 0)
	
	for days := 30; days >= 0; days-- {
		eventDate := now.AddDate(0, 0, -days)
		
		// Generate 10-50 events per day
		eventsPerDay := rand.Intn(40) + 10
		
		for i := 0; i < eventsPerDay; i++ {
			user := users[rand.Intn(len(users))]
			session := sessions[rand.Intn(len(sessions))]
			
			// Random time during the day
			eventTime := eventDate.Add(time.Duration(rand.Intn(24)) * time.Hour).
				Add(time.Duration(rand.Intn(60)) * time.Minute)
			
			// Generate different types of events
			eventTypes := []string{"page_view", "click", "form_submit", "purchase", "signup"}
			eventType := eventTypes[rand.Intn(len(eventTypes))]
			
			event := TestEvent{
				EventType: eventType,
				UserID:    user,
				SessionID: session,
				Timestamp: eventTime,
			}
			
			// Add properties based on event type
			switch eventType {
			case "page_view":
				page := pages[rand.Intn(len(pages))]
				event.Properties = map[string]interface{}{
					"path":     page,
					"title":    fmt.Sprintf("Page %s", page),
					"referrer": "https://google.com",
				}
			case "click":
				buttons := []string{"buy-now", "learn-more", "contact", "signup", "login"}
				event.Properties = map[string]interface{}{
					"button":   buttons[rand.Intn(len(buttons))],
					"page":     pages[rand.Intn(len(pages))],
					"position": rand.Intn(10),
				}
			case "form_submit":
				forms := []string{"contact", "newsletter", "demo-request"}
				event.Properties = map[string]interface{}{
					"form": forms[rand.Intn(len(forms))],
					"page": pages[rand.Intn(len(pages))],
				}
			case "purchase":
				products := []string{"pro-plan", "enterprise-plan", "basic-plan"}
				event.Properties = map[string]interface{}{
					"product": products[rand.Intn(len(products))],
					"amount":  rand.Intn(1000) + 10,
					"currency": "USD",
				}
			case "signup":
				event.Properties = map[string]interface{}{
					"plan":   "free",
					"source": "organic",
				}
			}
			
			events = append(events, event)
		}
	}
	
	fmt.Printf("Generated %d test events\n", len(events))
	
	// Send events in batches
	batchSize := 50
	for i := 0; i < len(events); i += batchSize {
		end := i + batchSize
		if end > len(events) {
			end = len(events)
		}
		
		batch := events[i:end]
		if err := sendEventBatch(baseURL, accountID, projectID, batch); err != nil {
			return fmt.Errorf("failed to send batch %d: %v", i/batchSize, err)
		}
		
		fmt.Printf("✅ Sent batch %d/%d (%d events)\n", 
			(i/batchSize)+1, 
			(len(events)+batchSize-1)/batchSize, 
			len(batch))
	}
	
	return nil
}

func sendEventBatch(baseURL, accountID, projectID string, events []TestEvent) error {
	url := fmt.Sprintf("%s/api/v1/events/batch", baseURL)
	
	jsonData, err := json.Marshal(events)
	if err != nil {
		return err
	}
	
	req, err := http.NewRequest("POST", url, bytes.NewBuffer(jsonData))
	if err != nil {
		return err
	}
	
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", fmt.Sprintf("ApiKey %s", accountID))
	req.Header.Set("X-Project-ID", projectID)
	
	client := &http.Client{Timeout: 30 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("batch request failed with status: %d", resp.StatusCode)
	}
	
	return nil
}

func testAnalyticsEndpoints(accountID, projectID string) error {
	baseURL := getServerURL()
	
	tests := []struct {
		name     string
		endpoint string
		method   string
	}{
		{"Health Check", "/health", "GET"},
		{"Dashboard", "/api/v1/dashboard", "GET"},
		{"Analytics - All Metrics", "/api/v1/analytics", "GET"},
		{"Analytics - User Metrics", "/api/v1/analytics?metrics=dau,wau,mau", "GET"},
		{"Analytics - Page Views", "/api/v1/analytics?metrics=page_views", "GET"},
		{"User Metrics", "/api/v1/user-metrics", "GET"},
		{"User Metrics - Weekly", "/api/v1/user-metrics?group_by=week", "GET"},
		{"Real-time", "/api/v1/realtime", "GET"},
		{"Flush Cache", "/api/v1/flush-cache", "POST"},
	}
	
	for _, test := range tests {
		fmt.Printf("🔍 Testing %s...\n", test.name)
		
		url := fmt.Sprintf("%s%s", baseURL, test.endpoint)
		req, err := http.NewRequest(test.method, url, nil)
		if err != nil {
			return fmt.Errorf("failed to create request for %s: %v", test.name, err)
		}
		
		// Add auth headers for protected endpoints
		if test.endpoint != "/health" {
			req.Header.Set("Authorization", fmt.Sprintf("ApiKey %s", accountID))
			req.Header.Set("X-Project-ID", projectID)
		}
		
		client := &http.Client{Timeout: 30 * time.Second}
		resp, err := client.Do(req)
		if err != nil {
			return fmt.Errorf("request failed for %s: %v", test.name, err)
		}
		defer resp.Body.Close()
		
		if resp.StatusCode != http.StatusOK {
			return fmt.Errorf("%s failed with status: %d", test.name, resp.StatusCode)
		}
		
		// Parse and display some results
		var result map[string]interface{}
		if err := json.NewDecoder(resp.Body).Decode(&result); err == nil {
			fmt.Printf("   ✅ %s - Status: %d\n", test.name, resp.StatusCode)
			
			// Show some key metrics
			if test.name == "Dashboard" {
				if overview, ok := result["overview"].(map[string]interface{}); ok {
					fmt.Printf("      📊 Events today: %.0f\n", overview["total_events_today"])
					fmt.Printf("      👥 Users today: %.0f\n", overview["unique_users_today"])
				}
				if userMetrics, ok := result["user_metrics"].(map[string]interface{}); ok {
					fmt.Printf("      📈 DAU: %.0f, WAU: %.0f, MAU: %.0f\n", 
						userMetrics["dau"], userMetrics["wau"], userMetrics["mau"])
				}
			}
		} else {
			fmt.Printf("   ✅ %s - Status: %d\n", test.name, resp.StatusCode)
		}
	}
	
	return nil
}

func cleanup(dbClient *db.PrismaClient, accountID, projectID string) error {
	// Delete project
	_, err := dbClient.Project.FindMany(
		db.Project.ID.Equals(projectID),
	).Delete().Exec(context.Background())
	if err != nil {
		return fmt.Errorf("failed to delete project: %v", err)
	}
	
	// Delete account
	_, err = dbClient.Account.FindMany(
		db.Account.ID.Equals(accountID),
	).Delete().Exec(context.Background())
	if err != nil {
		return fmt.Errorf("failed to delete account: %v", err)
	}
	
	fmt.Printf("✅ Cleaned up test account and project\n")
	return nil
}

func getServerURL() string {
	port := os.Getenv("PORT")
	if port == "" {
		port = "8080"
	}
	return fmt.Sprintf("http://localhost:%s", port)
}