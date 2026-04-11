package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	"os"
	"time"

	"github.com/resend/resend-go/v3"
	"gorm.io/gorm"
)

// AutomationEmailSender is the interface for sending automation emails
type AutomationEmailSender interface {
	SendEmail(req AutomationEmailRequest) (*AutomationEmailResult, error)
	Provider() string
}

// AutomationEmailRequest is a provider-agnostic email send request
type AutomationEmailRequest struct {
	To          string
	ToName      string
	From        string
	FromName    string
	ReplyTo     string
	Subject     string
	HTMLContent string
	PlainText   string
}

// AutomationEmailResult is the result of sending an email
type AutomationEmailResult struct {
	Provider  string `json:"provider"`
	MessageID string `json:"message_id,omitempty"`
	Status    string `json:"status"`
}

// =====================
// RESEND SENDER
// =====================

type ResendEmailSender struct {
	client *resend.Client
	apiKey string
}

func NewResendEmailSender(apiKey string) *ResendEmailSender {
	if apiKey == "" {
		apiKey = os.Getenv("RESEND_API_KEY")
	}
	var client *resend.Client
	if apiKey != "" {
		client = resend.NewClient(apiKey)
	}
	return &ResendEmailSender{client: client, apiKey: apiKey}
}

func (s *ResendEmailSender) Provider() string { return "resend" }

func (s *ResendEmailSender) SendEmail(req AutomationEmailRequest) (*AutomationEmailResult, error) {
	if s.client == nil {
		return nil, fmt.Errorf("resend client not configured (missing API key)")
	}

	fromAddress := req.From
	if req.FromName != "" {
		fromAddress = fmt.Sprintf("%s <%s>", req.FromName, req.From)
	}

	params := &resend.SendEmailRequest{
		From:    fromAddress,
		To:      []string{req.To},
		Subject: req.Subject,
		Html:    req.HTMLContent,
		Text:    req.PlainText,
	}
	if req.ReplyTo != "" {
		params.ReplyTo = req.ReplyTo
	}

	sent, err := s.client.Emails.Send(params)
	if err != nil {
		return nil, fmt.Errorf("resend send failed: %w", err)
	}

	log.Printf("Resend: email sent to %s, message_id=%s", req.To, sent.Id)
	return &AutomationEmailResult{
		Provider:  "resend",
		MessageID: sent.Id,
		Status:    "sent",
	}, nil
}

// =====================
// SENDGRID SENDER
// =====================

type SendGridEmailSender struct {
	apiKey     string
	httpClient *http.Client
}

func NewSendGridEmailSender(apiKey string) *SendGridEmailSender {
	if apiKey == "" {
		apiKey = os.Getenv("SENDGRID_API_KEY")
	}
	return &SendGridEmailSender{
		apiKey:     apiKey,
		httpClient: &http.Client{Timeout: 30 * time.Second},
	}
}

func (s *SendGridEmailSender) Provider() string { return "sendgrid" }

func (s *SendGridEmailSender) SendEmail(req AutomationEmailRequest) (*AutomationEmailResult, error) {
	if s.apiKey == "" {
		return nil, fmt.Errorf("sendgrid not configured (missing API key)")
	}

	// Build SendGrid v3 API payload
	payload := map[string]interface{}{
		"personalizations": []map[string]interface{}{
			{
				"to": []map[string]string{
					{"email": req.To, "name": req.ToName},
				},
			},
		},
		"from": map[string]string{
			"email": req.From,
			"name":  req.FromName,
		},
		"subject": req.Subject,
		"content": []map[string]string{
			{"type": "text/plain", "value": req.PlainText},
			{"type": "text/html", "value": req.HTMLContent},
		},
	}

	if req.ReplyTo != "" {
		payload["reply_to"] = map[string]string{"email": req.ReplyTo}
	}

	body, err := json.Marshal(payload)
	if err != nil {
		return nil, fmt.Errorf("sendgrid: failed to marshal payload: %w", err)
	}

	httpReq, err := http.NewRequest("POST", "https://api.sendgrid.com/v3/mail/send", bytes.NewReader(body))
	if err != nil {
		return nil, fmt.Errorf("sendgrid: failed to create request: %w", err)
	}
	httpReq.Header.Set("Authorization", "Bearer "+s.apiKey)
	httpReq.Header.Set("Content-Type", "application/json")

	resp, err := s.httpClient.Do(httpReq)
	if err != nil {
		return nil, fmt.Errorf("sendgrid: request failed: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode >= 400 {
		respBody, _ := io.ReadAll(resp.Body)
		return nil, fmt.Errorf("sendgrid: API returned %d: %s", resp.StatusCode, string(respBody))
	}

	messageID := resp.Header.Get("X-Message-Id")
	log.Printf("SendGrid: email sent to %s, message_id=%s", req.To, messageID)

	return &AutomationEmailResult{
		Provider:  "sendgrid",
		MessageID: messageID,
		Status:    "sent",
	}, nil
}

// =====================
// MAILCHIMP SENDER (wraps existing MailchimpService)
// =====================

type MailchimpEmailSender struct {
	mailchimpService *MailchimpService
	projectID        string
	audienceID       string
}

func NewMailchimpEmailSender(mailchimpService *MailchimpService, projectID, audienceID string) *MailchimpEmailSender {
	return &MailchimpEmailSender{
		mailchimpService: mailchimpService,
		projectID:        projectID,
		audienceID:       audienceID,
	}
}

func (s *MailchimpEmailSender) Provider() string { return "mailchimp" }

func (s *MailchimpEmailSender) SendEmail(req AutomationEmailRequest) (*AutomationEmailResult, error) {
	campaignRequest := &MailchimpCampaignRequest{
		Type: "regular",
		Recipients: struct {
			ListID string `json:"list_id"`
		}{
			ListID: s.audienceID,
		},
		Settings: struct {
			SubjectLine string `json:"subject_line"`
			PreviewText string `json:"preview_text"`
			Title       string `json:"title"`
			FromName    string `json:"from_name"`
			ReplyTo     string `json:"reply_to"`
		}{
			SubjectLine: req.Subject,
			PreviewText: req.Subject,
			Title:       req.Subject,
			FromName:    req.FromName,
			ReplyTo:     req.ReplyTo,
		},
		Tracking: struct {
			Opens      bool `json:"opens"`
			HtmlClicks bool `json:"html_clicks"`
			TextClicks bool `json:"text_clicks"`
		}{
			Opens:      true,
			HtmlClicks: true,
			TextClicks: true,
		},
	}

	campaign, err := s.mailchimpService.CreateAndSendCampaign(s.projectID, campaignRequest, req.HTMLContent)
	if err != nil {
		return nil, fmt.Errorf("mailchimp send failed: %w", err)
	}

	campaignID := ""
	if campaign != nil {
		campaignID = campaign.ID
	}

	log.Printf("Mailchimp: campaign sent to %s, campaign_id=%s", req.To, campaignID)
	return &AutomationEmailResult{
		Provider:  "mailchimp",
		MessageID: campaignID,
		Status:    "sent",
	}, nil
}

// =====================
// EMAIL CHARACTER LIMIT
// =====================

// EnforceEmailCharLimit truncates HTML content to the configured max character limit for a project.
// Returns the (possibly truncated) content. If limit is 0, no truncation is applied.
func EnforceEmailCharLimit(db *gorm.DB, projectID string, htmlContent string) string {
	settings, err := GetOrCreateProjectSettings(db, projectID)
	if err != nil || settings.MaxEmailCharacters <= 0 {
		return htmlContent
	}

	if len(htmlContent) > settings.MaxEmailCharacters {
		return htmlContent[:settings.MaxEmailCharacters]
	}
	return htmlContent
}

// =====================
// FACTORY
// =====================

// GetEmailSenderForAutomation returns the appropriate email sender based on automation config
func GetEmailSenderForAutomation(automation *AutomationSettings, mailchimpService *MailchimpService) AutomationEmailSender {
	provider := getString(automation.Config, "email_provider")
	providerConfig, _ := automation.Config["email_provider_config"].(map[string]interface{})

	switch provider {
	case "resend":
		apiKey := ""
		if providerConfig != nil {
			apiKey, _ = providerConfig["api_key"].(string)
		}
		return NewResendEmailSender(apiKey)

	case "sendgrid":
		apiKey := ""
		if providerConfig != nil {
			apiKey, _ = providerConfig["api_key"].(string)
		}
		return NewSendGridEmailSender(apiKey)

	case "mailchimp":
		audienceID := getString(automation.Config, "audience_id")
		return NewMailchimpEmailSender(mailchimpService, automation.ProjectID, audienceID)

	default:
		// Default to mailchimp for backward compatibility
		audienceID := getString(automation.Config, "audience_id")
		return NewMailchimpEmailSender(mailchimpService, automation.ProjectID, audienceID)
	}
}
