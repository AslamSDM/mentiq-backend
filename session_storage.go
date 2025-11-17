package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"log"
	"os"

	"github.com/aws/aws-sdk-go/aws"
	"github.com/aws/aws-sdk-go/aws/credentials"
	"github.com/aws/aws-sdk-go/aws/session"
	"github.com/aws/aws-sdk-go/service/s3"
	"github.com/google/uuid"
)

// SessionStorageService handles storing session recordings in S3/R2
type SessionStorageService struct {
	s3Client   *s3.S3
	bucketName string
}

// NewSessionStorageService creates a new session storage service
func NewSessionStorageService() (*SessionStorageService, error) {
	// Get credentials from environment
	accessKey := os.Getenv("CLOUDFLARE_R2_ACCESS_KEY")
	secretKey := os.Getenv("CLOUDFLARE_R2_SECRET_KEY")
	accountID := os.Getenv("CLOUDFLARE_ACCOUNT_ID")
	bucketName := os.Getenv("SESSION_RECORDING_BUCKET")

	if accessKey == "" || secretKey == "" || accountID == "" {
		log.Println("Warning: S3/R2 credentials not configured. Session recordings will be stored in database.")
		return nil, fmt.Errorf("S3/R2 credentials not configured")
	}

	if bucketName == "" {
		bucketName = "mentiq-session-recordings" // Default bucket name
	}

	// Configure S3-compatible client for Cloudflare R2
	endpoint := fmt.Sprintf("https://%s.r2.cloudflarestorage.com", accountID)

	sess, err := session.NewSession(&aws.Config{
		Region:           aws.String("auto"),
		Credentials:      credentials.NewStaticCredentials(accessKey, secretKey, ""),
		Endpoint:         aws.String(endpoint),
		S3ForcePathStyle: aws.Bool(true),
	})

	if err != nil {
		return nil, fmt.Errorf("failed to create S3 session: %w", err)
	}

	return &SessionStorageService{
		s3Client:   s3.New(sess),
		bucketName: bucketName,
	}, nil
}

// UploadRecording uploads recording data to S3/R2
func (sss *SessionStorageService) UploadRecording(sessionID, projectID, accountID string, events []map[string]interface{}) (string, error) {
	// Generate unique key for this recording
	recordingID := uuid.New().String()
	key := fmt.Sprintf("recordings/account_id=%s/project_id=%s/session_id=%s/%s.json", 
		accountID, projectID, sessionID, recordingID)

	// Marshal events to JSON
	eventsJSON, err := json.Marshal(events)
	if err != nil {
		return "", fmt.Errorf("failed to marshal events: %w", err)
	}

	// Upload to S3/R2
	_, err = sss.s3Client.PutObject(&s3.PutObjectInput{
		Bucket:      aws.String(sss.bucketName),
		Key:         aws.String(key),
		Body:        bytes.NewReader(eventsJSON),
		ContentType: aws.String("application/json"),
	})

	if err != nil {
		return "", fmt.Errorf("failed to upload recording: %w", err)
	}

	log.Printf("Uploaded recording to S3: %s", key)
	return key, nil
}

// DownloadRecording retrieves recording data from S3/R2
func (sss *SessionStorageService) DownloadRecording(storagePath string) ([]map[string]interface{}, error) {
	// Download from S3/R2
	result, err := sss.s3Client.GetObject(&s3.GetObjectInput{
		Bucket: aws.String(sss.bucketName),
		Key:    aws.String(storagePath),
	})

	if err != nil {
		return nil, fmt.Errorf("failed to download recording: %w", err)
	}
	defer result.Body.Close()

	// Parse JSON
	var events []map[string]interface{}
	decoder := json.NewDecoder(result.Body)
	if err := decoder.Decode(&events); err != nil {
		return nil, fmt.Errorf("failed to parse recording: %w", err)
	}

	return events, nil
}

// DeleteRecording removes a recording from S3/R2
func (sss *SessionStorageService) DeleteRecording(storagePath string) error {
	_, err := sss.s3Client.DeleteObject(&s3.DeleteObjectInput{
		Bucket: aws.String(sss.bucketName),
		Key:    aws.String(storagePath),
	})

	if err != nil {
		return fmt.Errorf("failed to delete recording: %w", err)
	}

	log.Printf("Deleted recording from S3: %s", storagePath)
	return nil
}
