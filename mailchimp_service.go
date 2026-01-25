package main

import (
	"bytes"
	"crypto/aes"
	"crypto/cipher"
	"crypto/md5"
	"crypto/rand"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	"net/url"
	"os"
	"strings"
	"time"

	"github.com/google/uuid"
	"gorm.io/gorm"
)

// MailchimpService handles all Mailchimp API interactions
type MailchimpService struct {
	db            *gorm.DB
	clientID      string
	clientSecret  string
	redirectURI   string
	encryptionKey []byte
}

// NewMailchimpService creates a new Mailchimp service
func NewMailchimpService(db *gorm.DB) *MailchimpService {
	encryptionKey := os.Getenv("INTEGRATION_ENCRYPTION_KEY")
	if len(encryptionKey) < 32 {
		log.Println("Warning: INTEGRATION_ENCRYPTION_KEY not set or too short. Using default key (not secure for production)")
		encryptionKey = "default-encryption-key-32bytes!!"
	}

	return &MailchimpService{
		db:            db,
		clientID:      os.Getenv("MAILCHIMP_CLIENT_ID"),
		clientSecret:  os.Getenv("MAILCHIMP_CLIENT_SECRET"),
		redirectURI:   os.Getenv("MAILCHIMP_REDIRECT_URI"),
		encryptionKey: []byte(encryptionKey[:32]),
	}
}

// IsConfigured returns true if Mailchimp OAuth credentials are set
func (s *MailchimpService) IsConfigured() bool {
	return s.clientID != "" && s.clientSecret != "" && s.redirectURI != ""
}

// MailchimpTokenResponse represents Mailchimp OAuth token response
type MailchimpTokenResponse struct {
	AccessToken string `json:"access_token"`
	TokenType   string `json:"token_type"`
	ExpiresIn   int    `json:"expires_in"`
	Scope       string `json:"scope"`
}

// MailchimpMetadata represents Mailchimp API metadata response
type MailchimpMetadata struct {
	DC          string `json:"dc"`
	AccountID   string `json:"account_id"`
	AccountName string `json:"account_name"`
	Email       string `json:"email"`
}

// MailchimpAudience represents a Mailchimp list/audience
type MailchimpAudience struct {
	ID          string `json:"id"`
	Name        string `json:"name"`
	MemberCount int    `json:"stats.member_count"`
}

// MailchimpAudienceResponse represents Mailchimp lists API response
type MailchimpAudienceResponse struct {
	Lists []struct {
		ID    string `json:"id"`
		Name  string `json:"name"`
		Stats struct {
			MemberCount int `json:"member_count"`
		} `json:"stats"`
	} `json:"lists"`
}

// MailchimpContact represents a contact to sync to Mailchimp
type MailchimpContact struct {
	Email       string                 `json:"email_address"`
	Status      string                 `json:"status"` // "subscribed", "pending", "unsubscribed"
	MergeFields map[string]interface{} `json:"merge_fields,omitempty"`
	Tags        []string               `json:"tags,omitempty"`
}

// GetAuthURL returns the Mailchimp OAuth authorization URL
func (s *MailchimpService) GetAuthURL(projectID string) string {
	params := url.Values{}
	params.Set("response_type", "code")
	params.Set("client_id", s.clientID)
	params.Set("redirect_uri", s.redirectURI)
	params.Set("state", projectID) // Pass projectID in state for callback

	return "https://login.mailchimp.com/oauth2/authorize?" + params.Encode()
}

// ExchangeCodeForToken exchanges OAuth code for access token
func (s *MailchimpService) ExchangeCodeForToken(projectID, code string) (*ProjectIntegration, error) {
	// Exchange code for token
	data := url.Values{}
	data.Set("grant_type", "authorization_code")
	data.Set("client_id", s.clientID)
	data.Set("client_secret", s.clientSecret)
	data.Set("redirect_uri", s.redirectURI)
	data.Set("code", code)

	resp, err := http.PostForm("https://login.mailchimp.com/oauth2/token", data)
	if err != nil {
		return nil, fmt.Errorf("token exchange failed: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		return nil, fmt.Errorf("token exchange failed with status %d: %s", resp.StatusCode, string(body))
	}

	var tokenResp MailchimpTokenResponse
	if err := json.NewDecoder(resp.Body).Decode(&tokenResp); err != nil {
		return nil, fmt.Errorf("failed to decode token response: %w", err)
	}

	// Get Mailchimp metadata (server prefix and account info)
	metadata, err := s.getMetadata(tokenResp.AccessToken)
	if err != nil {
		return nil, fmt.Errorf("failed to get metadata: %w", err)
	}

	// Encrypt the access token before storing
	encryptedToken, err := s.encrypt(tokenResp.AccessToken)
	if err != nil {
		return nil, fmt.Errorf("failed to encrypt token: %w", err)
	}

	// Check if integration already exists
	var existing ProjectIntegration
	err = s.db.Where("project_id = ? AND provider = ?", projectID, "mailchimp").First(&existing).Error

	now := time.Now()

	if err == gorm.ErrRecordNotFound {
		// Create new integration
		integration := ProjectIntegration{
			ID:          uuid.New().String(),
			ProjectID:   projectID,
			Provider:    "mailchimp",
			AccessToken: encryptedToken,
			IsActive:    true,
			Settings: map[string]interface{}{
				"server_prefix":  metadata.DC,
				"account_name":   metadata.AccountName,
				"account_email":  metadata.Email,
				"sync_high_risk": true,
				"risk_threshold": 70,
				"add_tags":       true,
				"tag_name":       "churn_risk_high",
			},
			CreatedAt: now,
			UpdatedAt: now,
		}

		if err := s.db.Create(&integration).Error; err != nil {
			return nil, fmt.Errorf("failed to save integration: %w", err)
		}

		return &integration, nil
	} else if err != nil {
		return nil, fmt.Errorf("database error: %w", err)
	}

	// Update existing integration
	existing.AccessToken = encryptedToken
	existing.IsActive = true
	existing.UpdatedAt = now

	// Update server prefix in settings
	if existing.Settings == nil {
		existing.Settings = make(map[string]interface{})
	}
	existing.Settings["server_prefix"] = metadata.DC
	existing.Settings["account_name"] = metadata.AccountName
	existing.Settings["account_email"] = metadata.Email

	if err := s.db.Save(&existing).Error; err != nil {
		return nil, fmt.Errorf("failed to update integration: %w", err)
	}

	return &existing, nil
}

// getMetadata gets Mailchimp API metadata (server prefix, account info)
func (s *MailchimpService) getMetadata(accessToken string) (*MailchimpMetadata, error) {
	req, err := http.NewRequest("GET", "https://login.mailchimp.com/oauth2/metadata", nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Authorization", "OAuth "+accessToken)

	client := &http.Client{Timeout: 10 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	var metadata MailchimpMetadata
	if err := json.NewDecoder(resp.Body).Decode(&metadata); err != nil {
		return nil, err
	}

	return &metadata, nil
}

// GetAudiences returns all Mailchimp audiences/lists for a project
func (s *MailchimpService) GetAudiences(projectID string) ([]MailchimpAudience, error) {
	integration, err := s.getIntegration(projectID)
	if err != nil {
		return nil, err
	}

	accessToken, err := s.decrypt(integration.AccessToken)
	if err != nil {
		return nil, fmt.Errorf("failed to decrypt token: %w", err)
	}

	serverPrefix := ""
	if sp, ok := integration.Settings["server_prefix"].(string); ok {
		serverPrefix = sp
	}
	if serverPrefix == "" {
		return nil, fmt.Errorf("server prefix not found in settings")
	}

	apiURL := fmt.Sprintf("https://%s.api.mailchimp.com/3.0/lists?count=100", serverPrefix)

	req, err := http.NewRequest("GET", apiURL, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Authorization", "Bearer "+accessToken)

	client := &http.Client{Timeout: 15 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		return nil, fmt.Errorf("mailchimp API error: %s", string(body))
	}

	var listResp MailchimpAudienceResponse
	if err := json.NewDecoder(resp.Body).Decode(&listResp); err != nil {
		return nil, err
	}

	audiences := make([]MailchimpAudience, len(listResp.Lists))
	for i, list := range listResp.Lists {
		audiences[i] = MailchimpAudience{
			ID:          list.ID,
			Name:        list.Name,
			MemberCount: list.Stats.MemberCount,
		}
	}

	return audiences, nil
}

// SyncContact syncs a single contact to Mailchimp
func (s *MailchimpService) SyncContact(projectID string, contact MailchimpContact) error {
	integration, err := s.getIntegration(projectID)
	if err != nil {
		return err
	}

	audienceID := ""
	if aid, ok := integration.Settings["audience_id"].(string); ok {
		audienceID = aid
	}
	if audienceID == "" {
		return fmt.Errorf("no audience selected")
	}

	accessToken, err := s.decrypt(integration.AccessToken)
	if err != nil {
		return fmt.Errorf("failed to decrypt token: %w", err)
	}

	serverPrefix := ""
	if sp, ok := integration.Settings["server_prefix"].(string); ok {
		serverPrefix = sp
	}

	// Use upsert endpoint (PUT to add or update member)
	emailHash := s.md5Hash(strings.ToLower(contact.Email))
	apiURL := fmt.Sprintf("https://%s.api.mailchimp.com/3.0/lists/%s/members/%s",
		serverPrefix, audienceID, emailHash)

	payload := map[string]interface{}{
		"email_address": contact.Email,
		"status_if_new": "subscribed",
	}
	if len(contact.MergeFields) > 0 {
		payload["merge_fields"] = contact.MergeFields
	}

	jsonData, err := json.Marshal(payload)
	if err != nil {
		return err
	}

	req, err := http.NewRequest("PUT", apiURL, bytes.NewBuffer(jsonData))
	if err != nil {
		return err
	}
	req.Header.Set("Authorization", "Bearer "+accessToken)
	req.Header.Set("Content-Type", "application/json")

	client := &http.Client{Timeout: 15 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()

	if resp.StatusCode >= 400 {
		body, _ := io.ReadAll(resp.Body)
		return fmt.Errorf("mailchimp API error: %s", string(body))
	}

	// Add tags if configured
	if addTags, ok := integration.Settings["add_tags"].(bool); ok && addTags {
		tagName := "churn_risk_high"
		if tn, ok := integration.Settings["tag_name"].(string); ok && tn != "" {
			tagName = tn
		}
		_ = s.addTag(accessToken, serverPrefix, audienceID, contact.Email, tagName)
	}

	return nil
}

// SyncHighRiskUsers syncs all users above the risk threshold to Mailchimp
func (s *MailchimpService) SyncHighRiskUsers(projectID string) (*IntegrationSyncLog, error) {
	startTime := time.Now()

	integration, err := s.getIntegration(projectID)
	if err != nil {
		return nil, err
	}

	if !integration.IsActive {
		return nil, fmt.Errorf("integration is not active")
	}

	// Check if sync_high_risk is enabled
	if syncEnabled, ok := integration.Settings["sync_high_risk"].(bool); !ok || !syncEnabled {
		return nil, fmt.Errorf("high risk sync is not enabled")
	}

	audienceID := ""
	if aid, ok := integration.Settings["audience_id"].(string); ok {
		audienceID = aid
	}
	if audienceID == "" {
		return nil, fmt.Errorf("no audience selected")
	}

	// Get risk threshold
	threshold := 70
	if t, ok := integration.Settings["risk_threshold"].(float64); ok {
		threshold = int(t)
	}

	// Update integration status
	integration.SyncStatus = "syncing"
	s.db.Save(integration)

	// Get high-risk users from churn analytics
	var churnData []ChurnAnalytics
	err = s.db.Where("project_id = ? AND churn_risk_score >= ? AND is_churned = false",
		projectID, threshold).Find(&churnData).Error
	if err != nil {
		integration.SyncStatus = "error"
		integration.LastError = err.Error()
		s.db.Save(integration)
		return nil, err
	}

	// Get user emails from events
	userEmails := make(map[string]string)
	for _, c := range churnData {
		var event Event
		err := s.db.Where("project_id = ? AND user_id = ? AND properties->>'email' IS NOT NULL",
			projectID, c.UserID).Order("timestamp DESC").First(&event).Error
		if err == nil {
			// Properties is map[string]interface{}, access directly
			if email, ok := event.Properties["email"].(string); ok && email != "" {
				userEmails[c.UserID] = email
			}
		}
	}

	syncLog := IntegrationSyncLog{
		ID:            uuid.New().String(),
		IntegrationID: integration.ID,
		SyncType:      "auto",
		CreatedAt:     time.Now(),
	}

	synced := 0
	failed := 0
	skipped := 0

	for userID, email := range userEmails {
		// Find the churn data for this user
		var userData ChurnAnalytics
		for _, c := range churnData {
			if c.UserID == userID {
				userData = c
				break
			}
		}

		contact := MailchimpContact{
			Email:  email,
			Status: "subscribed",
			MergeFields: map[string]interface{}{
				"CHURN_RISK": userData.ChurnRiskScore,
				"USER_ID":    userID,
			},
		}

		err := s.SyncContact(projectID, contact)
		if err != nil {
			log.Printf("Failed to sync contact %s: %v", email, err)
			failed++
		} else {
			synced++
		}
	}

	// Update sync log
	syncLog.ContactsSynced = synced
	syncLog.ContactsFailed = failed
	syncLog.ContactsSkipped = skipped
	syncLog.Duration = int(time.Since(startTime).Milliseconds())

	if err := s.db.Create(&syncLog).Error; err != nil {
		log.Printf("Failed to create sync log: %v", err)
	}

	// Update integration status
	now := time.Now()
	integration.LastSyncAt = &now
	integration.SyncStatus = "idle"
	if failed > 0 && synced == 0 {
		integration.SyncStatus = "error"
		integration.LastError = fmt.Sprintf("Failed to sync %d contacts", failed)
	} else {
		integration.LastError = ""
	}
	s.db.Save(integration)

	return &syncLog, nil
}

// Disconnect removes the Mailchimp integration
func (s *MailchimpService) Disconnect(projectID string) error {
	return s.db.Where("project_id = ? AND provider = ?", projectID, "mailchimp").
		Delete(&ProjectIntegration{}).Error
}

// GetIntegration returns the Mailchimp integration for a project
func (s *MailchimpService) GetIntegration(projectID string) (*ProjectIntegration, error) {
	return s.getIntegration(projectID)
}

// UpdateSettings updates Mailchimp integration settings
func (s *MailchimpService) UpdateSettings(projectID string, settings map[string]interface{}) error {
	integration, err := s.getIntegration(projectID)
	if err != nil {
		return err
	}

	// Merge settings
	for k, v := range settings {
		integration.Settings[k] = v
	}

	integration.UpdatedAt = time.Now()
	return s.db.Save(integration).Error
}

// Helper methods

func (s *MailchimpService) getIntegration(projectID string) (*ProjectIntegration, error) {
	var integration ProjectIntegration
	err := s.db.Where("project_id = ? AND provider = ?", projectID, "mailchimp").
		First(&integration).Error
	if err != nil {
		if err == gorm.ErrRecordNotFound {
			return nil, fmt.Errorf("mailchimp integration not found")
		}
		return nil, err
	}
	return &integration, nil
}

func (s *MailchimpService) addTag(accessToken, serverPrefix, audienceID, email, tagName string) error {
	emailHash := s.md5Hash(strings.ToLower(email))
	apiURL := fmt.Sprintf("https://%s.api.mailchimp.com/3.0/lists/%s/members/%s/tags",
		serverPrefix, audienceID, emailHash)

	payload := map[string]interface{}{
		"tags": []map[string]interface{}{
			{"name": tagName, "status": "active"},
		},
	}

	jsonData, _ := json.Marshal(payload)

	req, err := http.NewRequest("POST", apiURL, bytes.NewBuffer(jsonData))
	if err != nil {
		return err
	}
	req.Header.Set("Authorization", "Bearer "+accessToken)
	req.Header.Set("Content-Type", "application/json")

	client := &http.Client{Timeout: 10 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()

	return nil
}

func (s *MailchimpService) md5Hash(text string) string {
	// Use crypto/md5 for Mailchimp subscriber hash
	h := md5.Sum([]byte(text))
	return fmt.Sprintf("%x", h)
}

// Encryption helpers

func (s *MailchimpService) encrypt(plaintext string) (string, error) {
	block, err := aes.NewCipher(s.encryptionKey)
	if err != nil {
		return "", err
	}

	gcm, err := cipher.NewGCM(block)
	if err != nil {
		return "", err
	}

	nonce := make([]byte, gcm.NonceSize())
	if _, err = io.ReadFull(rand.Reader, nonce); err != nil {
		return "", err
	}

	ciphertext := gcm.Seal(nonce, nonce, []byte(plaintext), nil)
	return base64.StdEncoding.EncodeToString(ciphertext), nil
}

func (s *MailchimpService) decrypt(ciphertext string) (string, error) {
	data, err := base64.StdEncoding.DecodeString(ciphertext)
	if err != nil {
		return "", err
	}

	block, err := aes.NewCipher(s.encryptionKey)
	if err != nil {
		return "", err
	}

	gcm, err := cipher.NewGCM(block)
	if err != nil {
		return "", err
	}

	nonceSize := gcm.NonceSize()
	if len(data) < nonceSize {
		return "", fmt.Errorf("ciphertext too short")
	}

	nonce, ciphertextBytes := data[:nonceSize], data[nonceSize:]
	plaintext, err := gcm.Open(nil, nonce, ciphertextBytes, nil)
	if err != nil {
		return "", err
	}

	return string(plaintext), nil
}
