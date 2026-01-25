package main

import (
	"log"
	"time"

	"gorm.io/gorm"
)

// ChurnSyncWorker handles background syncing of at-risk users to email platforms
type ChurnSyncWorker struct {
	db               *gorm.DB
	mailchimpService *MailchimpService
	interval         time.Duration
	stopChan         chan struct{}
}

// NewChurnSyncWorker creates a new churn sync worker
func NewChurnSyncWorker(db *gorm.DB) *ChurnSyncWorker {
	return &ChurnSyncWorker{
		db:               db,
		mailchimpService: NewMailchimpService(db),
		interval:         15 * time.Minute, // Sync every 15 minutes
		stopChan:         make(chan struct{}),
	}
}

// Start begins the background sync loop
func (w *ChurnSyncWorker) Start() {
	log.Println("Starting churn sync worker (interval: 15 minutes)")
	go w.syncLoop()
}

// Stop stops the background sync loop
func (w *ChurnSyncWorker) Stop() {
	log.Println("Stopping churn sync worker...")
	close(w.stopChan)
}

func (w *ChurnSyncWorker) syncLoop() {
	// Run immediately on start
	w.runSync()

	ticker := time.NewTicker(w.interval)
	defer ticker.Stop()

	for {
		select {
		case <-ticker.C:
			w.runSync()
		case <-w.stopChan:
			log.Println("Churn sync worker stopped")
			return
		}
	}
}

func (w *ChurnSyncWorker) runSync() {
	log.Println("Running churn sync for all active Mailchimp integrations...")

	// Get all active Mailchimp integrations with sync_high_risk enabled
	var integrations []ProjectIntegration
	err := w.db.Where("provider = ? AND is_active = ?", "mailchimp", true).
		Find(&integrations).Error
	if err != nil {
		log.Printf("Error fetching integrations: %v", err)
		return
	}

	syncCount := 0
	for _, integration := range integrations {
		// Check if sync_high_risk is enabled
		syncEnabled := false
		if enabled, ok := integration.Settings["sync_high_risk"].(bool); ok {
			syncEnabled = enabled
		}

		// Check if audience_id is configured
		audienceConfigured := false
		if aid, ok := integration.Settings["audience_id"].(string); ok && aid != "" {
			audienceConfigured = true
		}

		if !syncEnabled || !audienceConfigured {
			continue
		}

		// Run sync for this project
		syncLog, err := w.mailchimpService.SyncHighRiskUsers(integration.ProjectID)
		if err != nil {
			log.Printf("Error syncing project %s: %v", integration.ProjectID, err)
			continue
		}

		if syncLog.ContactsSynced > 0 || syncLog.ContactsFailed > 0 {
			log.Printf("Project %s: synced %d contacts, failed %d, duration %dms",
				integration.ProjectID, syncLog.ContactsSynced, syncLog.ContactsFailed, syncLog.Duration)
		}
		syncCount++
	}

	if syncCount > 0 {
		log.Printf("Churn sync completed for %d projects", syncCount)
	}
}

// SyncProject manually syncs a specific project (for on-demand syncing)
func (w *ChurnSyncWorker) SyncProject(projectID string) (*IntegrationSyncLog, error) {
	return w.mailchimpService.SyncHighRiskUsers(projectID)
}
