package main

import (
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"os"
	"strings"
	"time"

	"github.com/aws/aws-sdk-go/aws"
	"github.com/aws/aws-sdk-go/aws/session"
	"github.com/aws/aws-sdk-go/service/s3"
	"github.com/google/uuid"
	"github.com/gorilla/mux"
	"github.com/rs/cors"
)

type Event struct {
	EventID    string                 `json:"event_id"`
	EventType  string                 `json:"event_type"`
	UserID     string                 `json:"user_id,omitempty"`
	SessionID  string                 `json:"session_id,omitempty"`
	Timestamp  time.Time              `json:"timestamp"`
	Properties map[string]interface{} `json:"properties,omitempty"`
	UserAgent  string                 `json:"user_agent,omitempty"`
	IPAddress  string                 `json:"ip_address,omitempty"`
}

type AnalyticsService struct {
	s3Client   *s3.S3
	bucketName string
}

func NewAnalyticsService(bucketName string) (*AnalyticsService, error) {
	sess, err := session.NewSession(&aws.Config{
		Region: aws.String(os.Getenv("AWS_REGION")),
	})
	if err != nil {
		return nil, fmt.Errorf("failed to create AWS session: %v", err)
	}

	return &AnalyticsService{
		s3Client:   s3.New(sess),
		bucketName: bucketName,
	}, nil
}

func (as *AnalyticsService) storeEventToS3(event Event) error {
	eventJSON, err := json.Marshal(event)
	if err != nil {
		return fmt.Errorf("failed to marshal event: %v", err)
	}

	// Create S3 key with date partitioning
	key := fmt.Sprintf("events/year=%d/month=%02d/day=%02d/%s.json",
		event.Timestamp.Year(),
		event.Timestamp.Month(),
		event.Timestamp.Day(),
		event.EventID,
	)

	_, err = as.s3Client.PutObject(&s3.PutObjectInput{
		Bucket:      aws.String(as.bucketName),
		Key:         aws.String(key),
		Body:        aws.ReadSeekCloser(strings.NewReader(string(eventJSON))),
		ContentType: aws.String("application/json"),
	})

	if err != nil {
		return fmt.Errorf("failed to store event to S3: %v", err)
	}

	log.Printf("Event %s stored to S3 with key: %s", event.EventID, key)
	return nil
}

func (as *AnalyticsService) ingestEventHandler(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}

	var event Event
	if err := json.NewDecoder(r.Body).Decode(&event); err != nil {
		log.Printf("Failed to decode event: %v", err)
		http.Error(w, "Invalid JSON payload", http.StatusBadRequest)
		return
	}

	// Generate event ID if not provided
	if event.EventID == "" {
		event.EventID = uuid.New().String()
	}

	// Set timestamp if not provided
	if event.Timestamp.IsZero() {
		event.Timestamp = time.Now().UTC()
	}

	// Extract client info
	event.UserAgent = r.Header.Get("User-Agent")
	event.IPAddress = getClientIP(r)

	// Validate required fields
	if event.EventType == "" {
		http.Error(w, "event_type is required", http.StatusBadRequest)
		return
	}

	// Store event to S3
	if err := as.storeEventToS3(event); err != nil {
		log.Printf("Failed to store event: %v", err)
		http.Error(w, "Internal server error", http.StatusInternalServerError)
		return
	}

	// Return success response
	response := map[string]interface{}{
		"status":   "success",
		"event_id": event.EventID,
		"message":  "Event ingested successfully",
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(response)
}

func (as *AnalyticsService) batchIngestHandler(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}

	var events []Event
	if err := json.NewDecoder(r.Body).Decode(&events); err != nil {
		log.Printf("Failed to decode events: %v", err)
		http.Error(w, "Invalid JSON payload", http.StatusBadRequest)
		return
	}

	var successCount int
	var errors []string

	for i, event := range events {
		// Generate event ID if not provided
		if event.EventID == "" {
			event.EventID = uuid.New().String()
		}

		// Set timestamp if not provided
		if event.Timestamp.IsZero() {
			event.Timestamp = time.Now().UTC()
		}

		// Extract client info
		event.UserAgent = r.Header.Get("User-Agent")
		event.IPAddress = getClientIP(r)

		// Validate required fields
		if event.EventType == "" {
			errors = append(errors, fmt.Sprintf("Event %d: event_type is required", i))
			continue
		}

		// Store event to S3
		if err := as.storeEventToS3(event); err != nil {
			log.Printf("Failed to store event %d: %v", i, err)
			errors = append(errors, fmt.Sprintf("Event %d: failed to store", i))
			continue
		}

		successCount++
	}

	// Return response
	response := map[string]interface{}{
		"status":        "completed",
		"total_events":  len(events),
		"success_count": successCount,
		"error_count":   len(errors),
	}

	if len(errors) > 0 {
		response["errors"] = errors
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(response)
}

func healthCheckHandler(w http.ResponseWriter, r *http.Request) {
	response := map[string]interface{}{
		"status":    "healthy",
		"timestamp": time.Now().UTC(),
		"service":   "analytics-platform",
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(response)
}

func getClientIP(r *http.Request) string {
	// Check for X-Forwarded-For header (common in load balancers)
	if xff := r.Header.Get("X-Forwarded-For"); xff != "" {
		// Take the first IP from the comma-separated list
		if idx := strings.Index(xff, ","); idx != -1 {
			return strings.TrimSpace(xff[:idx])
		}
		return strings.TrimSpace(xff)
	}

	// Check for X-Real-IP header
	if xri := r.Header.Get("X-Real-IP"); xri != "" {
		return strings.TrimSpace(xri)
	}

	// Fall back to RemoteAddr
	if idx := strings.LastIndex(r.RemoteAddr, ":"); idx != -1 {
		return r.RemoteAddr[:idx]
	}
	return r.RemoteAddr
}

func main() {
	// Get configuration from environment variables
	bucketName := os.Getenv("S3_BUCKET_NAME")
	if bucketName == "" {
		log.Fatal("S3_BUCKET_NAME environment variable is required")
	}

	port := os.Getenv("PORT")
	if port == "" {
		port = "8080"
	}

	// Initialize analytics service
	analyticsService, err := NewAnalyticsService(bucketName)
	if err != nil {
		log.Fatalf("Failed to initialize analytics service: %v", err)
	}

	// Setup router
	router := mux.NewRouter()

	// API routes
	router.HandleFunc("/health", healthCheckHandler).Methods("GET")
	router.HandleFunc("/api/v1/events", analyticsService.ingestEventHandler).Methods("POST")
	router.HandleFunc("/api/v1/events/batch", analyticsService.batchIngestHandler).Methods("POST")

	// Setup CORS
	c := cors.New(cors.Options{
		AllowedOrigins:   []string{"*"}, // Configure this for production
		AllowedMethods:   []string{"GET", "POST", "PUT", "DELETE", "OPTIONS"},
		AllowedHeaders:   []string{"*"},
		AllowCredentials: true,
		Debug:            os.Getenv("CORS_DEBUG") == "true",
	})

	// Wrap router with CORS middleware
	handler := c.Handler(router)

	// Start server
	log.Printf("Analytics platform server starting on port %s", port)
	log.Printf("S3 bucket: %s", bucketName)
	log.Fatal(http.ListenAndServe(":"+port, handler))
}
