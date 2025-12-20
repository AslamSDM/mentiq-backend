package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"log"
	"os"
	"time"

	"github.com/aws/aws-sdk-go/aws"
	"github.com/aws/aws-sdk-go/aws/credentials"
	"github.com/aws/aws-sdk-go/aws/session"
	"github.com/aws/aws-sdk-go/service/s3"
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
	bucketName := os.Getenv("S3_BUCKET_NAME")

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
// Returns the base path (without chunk suffix) for the recording
func (sss *SessionStorageService) UploadRecording(sessionID, projectID, accountID string, events json.RawMessage) (string, error) {
	// Use timestamp for chunk ordering
	timestamp := time.Now().UnixNano()
	basePath := fmt.Sprintf("recordings/account_id=%s/project_id=%s/session_id=%s",
		accountID, projectID, sessionID)
	key := fmt.Sprintf("%s/chunk_%d.json", basePath, timestamp)

	// Upload to S3/R2
	_, err := sss.s3Client.PutObject(&s3.PutObjectInput{
		Bucket:      aws.String(sss.bucketName),
		Key:         aws.String(key),
		Body:        bytes.NewReader(events),
		ContentType: aws.String("application/json"),
	})

	if err != nil {
		return "", fmt.Errorf("failed to upload recording: %w", err)
	}

	log.Printf("Uploaded recording chunk to S3: %s", key)
	// Return the base path so we can find all chunks later
	return basePath, nil
} // DownloadRecording retrieves all recording chunks from S3/R2 and combines them
func (sss *SessionStorageService) DownloadRecording(storagePath string) ([]map[string]interface{}, error) {
	// List all chunks in this recording path
	listResult, err := sss.s3Client.ListObjectsV2(&s3.ListObjectsV2Input{
		Bucket: aws.String(sss.bucketName),
		Prefix: aws.String(storagePath + "/"),
	})

	if err != nil {
		return nil, fmt.Errorf("failed to list recording chunks: %w", err)
	}

	if len(listResult.Contents) == 0 {
		return []map[string]interface{}{}, nil
	}

	// Combine all chunks
	var allEvents []map[string]interface{}

	for _, obj := range listResult.Contents {
		// Download chunk
		result, err := sss.s3Client.GetObject(&s3.GetObjectInput{
			Bucket: aws.String(sss.bucketName),
			Key:    obj.Key,
		})

		if err != nil {
			log.Printf("Warning: Failed to download chunk %s: %v", *obj.Key, err)
			continue
		}

		// Parse JSON
		var chunkEvents []map[string]interface{}
		decoder := json.NewDecoder(result.Body)
		if err := decoder.Decode(&chunkEvents); err != nil {
			log.Printf("Warning: Failed to parse chunk %s: %v", *obj.Key, err)
			result.Body.Close()
			continue
		}
		result.Body.Close()

		// Append to all events
		allEvents = append(allEvents, chunkEvents...)
	}

	return allEvents, nil
}

// DownloadRecordingData retrieves all recording chunks from S3/R2 and combines them into raw JSON
func (sss *SessionStorageService) DownloadRecordingData(storagePath string) ([]byte, error) {
	// Use DownloadRecording to get all events
	events, err := sss.DownloadRecording(storagePath)
	if err != nil {
		return nil, err
	}

	// Marshal back to JSON
	data, err := json.Marshal(events)
	if err != nil {
		return nil, fmt.Errorf("failed to marshal combined events: %w", err)
	}

	return data, nil
}

// DeleteRecording removes all recording chunks from S3/R2
func (sss *SessionStorageService) DeleteRecording(storagePath string) error {
	// List all chunks
	listResult, err := sss.s3Client.ListObjectsV2(&s3.ListObjectsV2Input{
		Bucket: aws.String(sss.bucketName),
		Prefix: aws.String(storagePath + "/"),
	})

	if err != nil {
		return fmt.Errorf("failed to list recording chunks for deletion: %w", err)
	}

	// Delete each chunk
	for _, obj := range listResult.Contents {
		_, err := sss.s3Client.DeleteObject(&s3.DeleteObjectInput{
			Bucket: aws.String(sss.bucketName),
			Key:    obj.Key,
		})

		if err != nil {
			log.Printf("Warning: Failed to delete chunk %s: %v", *obj.Key, err)
		}
	}

	log.Printf("Deleted recording chunks from S3: %s", storagePath)
	return nil
}
