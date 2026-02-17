package main

import (
	"fmt"
	"log"
	"time"

	"gorm.io/driver/postgres"
	"gorm.io/gorm"
)

// InitDB initializes a GORM database connection with connection pooling
func InitDB(databaseURL string) (*gorm.DB, error) {
	db, err := gorm.Open(postgres.Open(databaseURL), &gorm.Config{})
	if err != nil {
		return nil, fmt.Errorf("failed to connect to database: %w", err)
	}

	sqlDB, err := db.DB()
	if err != nil {
		return nil, fmt.Errorf("failed to get underlying sql.DB: %w", err)
	}

	// Connection pool settings
	sqlDB.SetMaxOpenConns(50)                  // Max connections to PostgreSQL (keep below pg max_connections, typically 100)
	sqlDB.SetMaxIdleConns(10)                  // Keep idle connections ready to avoid reconnect overhead
	sqlDB.SetConnMaxLifetime(30 * time.Minute) // Recycle connections to pick up DNS/config changes
	sqlDB.SetConnMaxIdleTime(5 * time.Minute)  // Close idle connections after 5 min to free resources

	log.Println("Database connection pool configured: maxOpen=50, maxIdle=10, maxLifetime=30m, maxIdleTime=5m")

	return db, nil
}

// MigrateDB runs all database migrations
func MigrateDB(db *gorm.DB) error {
	err := db.AutoMigrate(
		&Account{},
		&User{},
		&UserInvitation{},
		&ProjectMember{},
		&Project{},
		&APIKey{},
		&Experiment{},
		&Variant{},
		&ExperimentAssignment{},
		&ConversionEvent{},
		&SessionRecording{},
		// Mentiq subscription models
		&AccountSubscription{},
		&PaymentHistory{},
		&OnboardingStatus{},
		// Stripe revenue models (for customer analytics)
		&StripeCustomer{},
		&StripeSubscription{},
		&StripeInvoice{},
		&StripeCharge{},
		&RevenueMetrics{},
		// Enhanced analytics models
		&UserSessionMetrics{},
		&UserCohortMetrics{},
		&DeviceAnalytics{},
		&LocationAnalytics{},
		&FeatureAdoption{},
		&ConversionFunnel{},
		&ChurnAnalytics{},
		// Events table for TimescaleDB
		&Event{},
		// Playbooks models
		&Playbook{},
		&PlaybookStep{},
		&PlaybookTrigger{},
		&PlaybookEnrollment{},
		&PlaybookStepExecution{},
		&PlaybookAnalytics{},
		&LLMPlaybookGeneration{},
		// Automation models
		&AutomationSettings{},
		&EmailTemplate{},
		&DiscountCode{},
		&AutomationExecution{},
		// Integration models
		&ProjectIntegration{},
		&IntegrationSyncLog{},
		// Waitlist
		&Waitlist{},
		// Support Tickets
		&SupportTicket{},
		&TicketComment{},
	)

	if err != nil {
		return err
	}

	// Ensure stripe_api_key column exists in projects table
	if !db.Migrator().HasColumn(&Project{}, "stripe_api_key") {
		if err := db.Migrator().AddColumn(&Project{}, "stripe_api_key"); err != nil {
			return fmt.Errorf("failed to add stripe_api_key column: %w", err)
		}
	}

	// Initialize TimescaleDB hypertable after migrations
	return InitTimescaleDB(db)
}

// CreateIndices creates necessary database indices for better query performance
func CreateIndices(db *gorm.DB) error {
	// Create indices for faster queries
	indices := []struct {
		model  interface{}
		index  string
		column string
	}{
		// Account indices
		{&Account{}, "idx_account_email", "email"},
		// Project indices
		{&Project{}, "idx_project_account_id", "account_id"},
		// APIKey indices
		{&APIKey{}, "idx_api_key_project_id", "project_id"},
		{&APIKey{}, "idx_api_key_key", "key"},
		// Experiment indices
		{&Experiment{}, "idx_experiment_project_id", "project_id"},
		{&Experiment{}, "idx_experiment_key_project_id", "key, project_id"},
		{&Experiment{}, "idx_experiment_status", "status"},
		// Variant indices
		{&Variant{}, "idx_variant_experiment_id", "experiment_id"},
		{&Variant{}, "idx_variant_key", "key"},
		// ExperimentAssignment indices
		{&ExperimentAssignment{}, "idx_assignment_experiment_id", "experiment_id"},
		{&ExperimentAssignment{}, "idx_assignment_variant_id", "variant_id"},
		{&ExperimentAssignment{}, "idx_assignment_user_id", "user_id"},
		{&ExperimentAssignment{}, "idx_assignment_anonymous_id", "anonymous_id"},
		// ConversionEvent indices
		{&ConversionEvent{}, "idx_conversion_experiment_id", "experiment_id"},
		{&ConversionEvent{}, "idx_conversion_variant_id", "variant_id"},
		{&ConversionEvent{}, "idx_conversion_user_id", "user_id"},
		// SessionRecording indices
		{&SessionRecording{}, "idx_recording_session_id", "session_id"},
		{&SessionRecording{}, "idx_recording_account_id", "account_id"},
		{&SessionRecording{}, "idx_recording_project_id", "project_id"},
		{&SessionRecording{}, "idx_recording_created_at", "created_at"},
		// AccountSubscription indices
		{&AccountSubscription{}, "idx_account_subscription_account_id", "account_id"},
		{&AccountSubscription{}, "idx_account_subscription_tier", "tier"},
		{&AccountSubscription{}, "idx_account_subscription_status", "status"},
		{&AccountSubscription{}, "idx_account_subscription_stripe_id", "stripe_subscription_id"},
		// PaymentHistory indices
		{&PaymentHistory{}, "idx_payment_history_subscription_id", "subscription_id"},
		{&PaymentHistory{}, "idx_payment_history_account_id", "account_id"},
		{&PaymentHistory{}, "idx_payment_history_status", "status"},
		{&PaymentHistory{}, "idx_payment_history_stripe_invoice_id", "stripe_invoice_id"},
		{&PaymentHistory{}, "idx_payment_history_created_at", "created_at"},
		// Stripe indices
		{&StripeCustomer{}, "idx_stripe_customer_project_id", "project_id"},
		{&StripeCustomer{}, "idx_stripe_customer_email", "email"},
		{&StripeSubscription{}, "idx_stripe_subscription_customer_id", "customer_id"},
		{&StripeSubscription{}, "idx_stripe_subscription_project_id", "project_id"},
		{&StripeSubscription{}, "idx_stripe_subscription_status", "status"},
		{&StripeInvoice{}, "idx_stripe_invoice_customer_id", "customer_id"},
		{&StripeInvoice{}, "idx_stripe_invoice_project_id", "project_id"},
		{&StripeInvoice{}, "idx_stripe_invoice_subscription_id", "subscription_id"},
		{&StripeInvoice{}, "idx_stripe_invoice_status", "status"},
		{&StripeCharge{}, "idx_stripe_charge_customer_id", "customer_id"},
		{&StripeCharge{}, "idx_stripe_charge_project_id", "project_id"},
		{&StripeCharge{}, "idx_stripe_charge_status", "status"},
		{&RevenueMetrics{}, "idx_revenue_metrics_project_id", "project_id"},
		{&RevenueMetrics{}, "idx_revenue_metrics_date", "date"},
		// Enhanced analytics indices
		{&UserSessionMetrics{}, "idx_session_metrics_project_id", "project_id"},
		{&UserSessionMetrics{}, "idx_session_metrics_date", "date"},
		{&UserCohortMetrics{}, "idx_cohort_metrics_project_id", "project_id"},
		{&UserCohortMetrics{}, "idx_cohort_metrics_cohort_month", "cohort_month"},
		{&DeviceAnalytics{}, "idx_device_analytics_project_id", "project_id"},
		{&DeviceAnalytics{}, "idx_device_analytics_date", "date"},
		{&DeviceAnalytics{}, "idx_device_analytics_device", "device"},
		{&LocationAnalytics{}, "idx_location_analytics_project_id", "project_id"},
		{&LocationAnalytics{}, "idx_location_analytics_date", "date"},
		{&LocationAnalytics{}, "idx_location_analytics_country", "country"},
		{&FeatureAdoption{}, "idx_feature_adoption_project_id", "project_id"},
		{&FeatureAdoption{}, "idx_feature_adoption_date", "date"},
		{&FeatureAdoption{}, "idx_feature_adoption_feature_name", "feature_name"},
		{&ConversionFunnel{}, "idx_conversion_funnel_project_id", "project_id"},
		{&ConversionFunnel{}, "idx_conversion_funnel_date", "date"},
		{&ConversionFunnel{}, "idx_conversion_funnel_name", "funnel_name"},
		{&ChurnAnalytics{}, "idx_churn_analytics_project_id", "project_id"},
		{&ChurnAnalytics{}, "idx_churn_analytics_date", "date"},
		{&ChurnAnalytics{}, "idx_churn_analytics_user_id", "user_id"},
		{&ChurnAnalytics{}, "idx_churn_analytics_risk_score", "churn_risk_score"},
		// Playbook indices
		{&Playbook{}, "idx_playbook_project_id", "project_id"},
		{&Playbook{}, "idx_playbook_status", "status"},
		{&Playbook{}, "idx_playbook_type", "type"},
		{&PlaybookStep{}, "idx_playbook_step_playbook_id", "playbook_id"},
		{&PlaybookStep{}, "idx_playbook_step_order", "step_order"},
		{&PlaybookTrigger{}, "idx_playbook_trigger_playbook_id", "playbook_id"},
		{&PlaybookTrigger{}, "idx_playbook_trigger_enabled", "is_enabled"},
		{&PlaybookEnrollment{}, "idx_playbook_enrollment_playbook_id", "playbook_id"},
		{&PlaybookEnrollment{}, "idx_playbook_enrollment_project_id", "project_id"},
		{&PlaybookEnrollment{}, "idx_playbook_enrollment_user_id", "user_id"},
		{&PlaybookEnrollment{}, "idx_playbook_enrollment_status", "status"},
		{&PlaybookStepExecution{}, "idx_playbook_step_execution_enrollment_id", "enrollment_id"},
		{&PlaybookStepExecution{}, "idx_playbook_step_execution_step_id", "step_id"},
		{&PlaybookStepExecution{}, "idx_playbook_step_execution_status", "status"},
		{&PlaybookAnalytics{}, "idx_playbook_analytics_playbook_id", "playbook_id"},
		{&PlaybookAnalytics{}, "idx_playbook_analytics_date", "date"},
		{&LLMPlaybookGeneration{}, "idx_llm_playbook_generation_project_id", "project_id"},
		{&LLMPlaybookGeneration{}, "idx_llm_playbook_generation_status", "status"},
	}

	for _, idx := range indices {
		if !db.Migrator().HasIndex(idx.model, idx.index) {
			log.Printf("Creating index: %s on column(s): %s", idx.index, idx.column)
			stmt := &gorm.Statement{DB: db}
			if err := stmt.Parse(idx.model); err != nil {
				log.Printf("Warning: Could not parse model for index %s: %v", idx.index, err)
				continue
			}
			tableName := stmt.Schema.Table
			if err := db.Exec(fmt.Sprintf("CREATE INDEX IF NOT EXISTS %s ON %s (%s)", idx.index, tableName, idx.column)).Error; err != nil {
				log.Printf("Warning: Could not create index %s: %v", idx.index, err)
			}
		}
	}

	return nil
}

// InitTimescaleDB sets up TimescaleDB hypertable and related configurations
func InitTimescaleDB(db *gorm.DB) error {
	log.Println("Initializing TimescaleDB hypertable for events...")

	// Check if TimescaleDB extension is available
	var extensionExists bool
	err := db.Raw("SELECT EXISTS(SELECT 1 FROM pg_extension WHERE extname = 'timescaledb')").Scan(&extensionExists).Error
	if err != nil {
		log.Printf("Warning: Could not check for TimescaleDB extension: %v", err)
		return nil // Don't fail if TimescaleDB is not available
	}

	if !extensionExists {
		log.Println("TimescaleDB extension not found. Attempting to create...")
		// Try to create the extension
		if err := db.Exec("CREATE EXTENSION IF NOT EXISTS timescaledb CASCADE").Error; err != nil {
			log.Printf("Warning: Could not create TimescaleDB extension: %v", err)
			log.Println("Continuing without TimescaleDB - events will be stored in regular PostgreSQL table")
			return nil
		}
		log.Println("TimescaleDB extension created successfully")
	}

	// Check if the events table is already a hypertable
	var isHypertable bool
	err = db.Raw(`
		SELECT EXISTS(
			SELECT 1 FROM timescaledb_information.hypertables 
			WHERE hypertable_name = 'events'
		)
	`).Scan(&isHypertable).Error

	if err != nil {
		log.Printf("Warning: Could not check hypertable status: %v", err)
		return nil
	}

	if isHypertable {
		log.Println("Events table is already a TimescaleDB hypertable")
	} else {
		// Convert events table to hypertable
		log.Println("Converting events table to TimescaleDB hypertable...")
		err = db.Exec(`
			SELECT create_hypertable(
				'events',
				'timestamp',
				if_not_exists => TRUE,
				migrate_data => TRUE
			)
		`).Error

		if err != nil {
			log.Printf("Warning: Could not create hypertable: %v", err)
			log.Println("Continuing with regular PostgreSQL table")
			return nil
		}

		log.Println("Successfully created TimescaleDB hypertable for events")
	}

	// Create additional indexes for better query performance
	log.Println("Creating additional indexes for events...")

	indexes := []string{
		// Composite index for common query patterns
		"CREATE INDEX IF NOT EXISTS idx_events_account_project_time ON events (account_id, project_id, timestamp DESC)",
		"CREATE INDEX IF NOT EXISTS idx_events_user_time ON events (user_id, timestamp DESC) WHERE user_id IS NOT NULL AND user_id != ''",
		"CREATE INDEX IF NOT EXISTS idx_events_session_time ON events (session_id, timestamp DESC) WHERE session_id IS NOT NULL AND session_id != ''",
		"CREATE INDEX IF NOT EXISTS idx_events_type_time ON events (event_type, timestamp DESC)",
	}

	for _, indexSQL := range indexes {
		if err := db.Exec(indexSQL).Error; err != nil {
			log.Printf("Warning: Could not create index: %v", err)
		}
	}

	log.Println("Indexes created successfully")

	// Set up compression policy (compress data older than 7 days)
	log.Println("Setting up compression policy...")
	err = db.Exec(`
		ALTER TABLE events SET (
			timescaledb.compress,
			timescaledb.compress_segmentby = 'account_id, project_id'
		)
	`).Error
	if err != nil {
		log.Printf("Warning: Could not set compression settings: %v", err)
	} else {
		// Add compression policy
		err = db.Exec(`
			SELECT add_compression_policy('events', INTERVAL '7 days', if_not_exists => TRUE)
		`).Error
		if err != nil {
			log.Printf("Warning: Could not add compression policy: %v", err)
		} else {
			log.Println("Compression policy added: data older than 7 days will be compressed")
		}
	}

	// Set up retention policy (keep data for 365 days)
	log.Println("Setting up retention policy...")
	err = db.Exec(`
		SELECT add_retention_policy('events', INTERVAL '365 days', if_not_exists => TRUE)
	`).Error
	if err != nil {
		log.Printf("Warning: Could not add retention policy: %v", err)
	} else {
		log.Println("Retention policy added: data older than 365 days will be automatically deleted")
	}

	// Create continuous aggregates for common analytics queries
	log.Println("Creating continuous aggregates...")

	// Daily aggregates
	err = db.Exec(`
		CREATE MATERIALIZED VIEW IF NOT EXISTS events_daily
		WITH (timescaledb.continuous) AS
		SELECT 
			time_bucket('1 day', timestamp) AS bucket,
			account_id,
			project_id,
			event_type,
			COUNT(*) as event_count,
			COUNT(DISTINCT user_id) as unique_users,
			COUNT(DISTINCT session_id) as unique_sessions
		FROM events
		GROUP BY bucket, account_id, project_id, event_type
		WITH NO DATA
	`).Error
	if err != nil {
		log.Printf("Warning: Could not create daily aggregate view: %v", err)
	} else {
		// Add refresh policy for daily aggregates
		db.Exec(`
			SELECT add_continuous_aggregate_policy('events_daily',
				start_offset => INTERVAL '3 days',
				end_offset => INTERVAL '1 hour',
				schedule_interval => INTERVAL '1 hour',
				if_not_exists => TRUE
			)
		`)
		log.Println("Daily continuous aggregate created")
	}

	// Hourly aggregates for real-time analytics
	err = db.Exec(`
		CREATE MATERIALIZED VIEW IF NOT EXISTS events_hourly
		WITH (timescaledb.continuous) AS
		SELECT 
			time_bucket('1 hour', timestamp) AS bucket,
			account_id,
			project_id,
			event_type,
			COUNT(*) as event_count,
			COUNT(DISTINCT user_id) as unique_users,
			COUNT(DISTINCT session_id) as unique_sessions
		FROM events
		GROUP BY bucket, account_id, project_id, event_type
		WITH NO DATA
	`).Error
	if err != nil {
		log.Printf("Warning: Could not create hourly aggregate view: %v", err)
	} else {
		// Add refresh policy for hourly aggregates (more frequent)
		db.Exec(`
			SELECT add_continuous_aggregate_policy('events_hourly',
				start_offset => INTERVAL '1 day',
				end_offset => INTERVAL '10 minutes',
				schedule_interval => INTERVAL '10 minutes',
				if_not_exists => TRUE
			)
		`)
		log.Println("Hourly continuous aggregate created")
	}

	// Initialize analytics tables as hypertables
	if err := InitAnalyticsHypertables(db); err != nil {
		log.Printf("Warning: Could not initialize analytics hypertables: %v", err)
	}

	log.Println("TimescaleDB initialization completed successfully")
	return nil
}

// InitAnalyticsHypertables converts analytics tables to TimescaleDB hypertables
func InitAnalyticsHypertables(db *gorm.DB) error {
	log.Println("Initializing TimescaleDB hypertables for analytics tables...")

	// List of tables to convert to hypertables with their time column
	analyticsHypertables := []struct {
		tableName     string
		timeColumn    string
		chunkInterval string
	}{
		{"revenue_metrics", "date", "1 month"},
		{"user_session_metrics", "date", "1 month"},
		{"user_cohort_metrics", "cohort_month", "1 year"}, // Longer interval for cohorts
		{"device_analytics", "date", "1 month"},
		{"location_analytics", "date", "1 month"},
		{"feature_adoption", "date", "1 month"},
		{"conversion_funnel", "date", "1 month"},
		{"churn_analytics", "date", "1 month"},
	}

	for _, ht := range analyticsHypertables {
		// Check if already a hypertable
		var isHypertable bool
		err := db.Raw(`
			SELECT EXISTS(
				SELECT 1 FROM timescaledb_information.hypertables 
				WHERE hypertable_name = ?
			)
		`, ht.tableName).Scan(&isHypertable).Error

		if err != nil {
			log.Printf("Warning: Could not check hypertable status for %s: %v", ht.tableName, err)
			continue
		}

		if isHypertable {
			log.Printf("%s is already a hypertable", ht.tableName)
			continue
		}

		// Convert to hypertable
		log.Printf("Converting %s to hypertable...", ht.tableName)
		err = db.Exec(fmt.Sprintf(`
			SELECT create_hypertable(
				'%s',
				'%s',
				chunk_time_interval => INTERVAL '%s',
				if_not_exists => TRUE,
				migrate_data => TRUE
			)
		`, ht.tableName, ht.timeColumn, ht.chunkInterval)).Error

		if err != nil {
			log.Printf("Warning: Could not create hypertable for %s: %v", ht.tableName, err)
			continue
		}

		log.Printf("Successfully created hypertable for %s", ht.tableName)

		// Set up compression for each table (compress data older than 30 days)
		err = db.Exec(fmt.Sprintf(`
			ALTER TABLE %s SET (
				timescaledb.compress,
				timescaledb.compress_segmentby = 'project_id'
			)
		`, ht.tableName)).Error
		if err != nil {
			log.Printf("Warning: Could not set compression for %s: %v", ht.tableName, err)
		} else {
			// Add compression policy
			err = db.Exec(fmt.Sprintf(`
				SELECT add_compression_policy('%s', INTERVAL '30 days', if_not_exists => TRUE)
			`, ht.tableName)).Error
			if err != nil {
				log.Printf("Warning: Could not add compression policy for %s: %v", ht.tableName, err)
			} else {
				log.Printf("Compression policy added for %s", ht.tableName)
			}
		}

		// Set up retention policy (keep analytics data for 2 years)
		err = db.Exec(fmt.Sprintf(`
			SELECT add_retention_policy('%s', INTERVAL '2 years', if_not_exists => TRUE)
		`, ht.tableName)).Error
		if err != nil {
			log.Printf("Warning: Could not add retention policy for %s: %v", ht.tableName, err)
		} else {
			log.Printf("Retention policy added for %s (2 years)", ht.tableName)
		}
	}

	// Create composite indexes for analytics tables
	log.Println("Creating composite indexes for analytics tables...")

	analyticsIndexes := []string{
		// RevenueMetrics
		"CREATE INDEX IF NOT EXISTS idx_revenue_project_date ON revenue_metrics (project_id, date DESC)",

		// UserSessionMetrics
		"CREATE INDEX IF NOT EXISTS idx_session_project_date ON user_session_metrics (project_id, date DESC)",

		// UserCohortMetrics
		"CREATE INDEX IF NOT EXISTS idx_cohort_project_month ON user_cohort_metrics (project_id, cohort_month DESC, period_number)",

		// DeviceAnalytics
		"CREATE INDEX IF NOT EXISTS idx_device_project_date ON device_analytics (project_id, date DESC, device)",
		"CREATE INDEX IF NOT EXISTS idx_device_os ON device_analytics (os, date DESC)",
		"CREATE INDEX IF NOT EXISTS idx_device_browser ON device_analytics (browser, date DESC)",

		// LocationAnalytics
		"CREATE INDEX IF NOT EXISTS idx_location_project_date ON location_analytics (project_id, date DESC, country)",
		"CREATE INDEX IF NOT EXISTS idx_location_country ON location_analytics (country, date DESC)",
		"CREATE INDEX IF NOT EXISTS idx_location_city ON location_analytics (city, date DESC)",

		// FeatureAdoption
		"CREATE INDEX IF NOT EXISTS idx_feature_project_date ON feature_adoption (project_id, date DESC, feature_name)",
		"CREATE INDEX IF NOT EXISTS idx_feature_name ON feature_adoption (feature_name, date DESC)",

		// ConversionFunnel
		"CREATE INDEX IF NOT EXISTS idx_funnel_project_date ON conversion_funnel (project_id, date DESC, funnel_name, step_number)",
		"CREATE INDEX IF NOT EXISTS idx_funnel_name ON conversion_funnel (funnel_name, step_number, date DESC)",

		// ChurnAnalytics
		"CREATE INDEX IF NOT EXISTS idx_churn_project_date ON churn_analytics (project_id, date DESC)",
		"CREATE INDEX IF NOT EXISTS idx_churn_user_date ON churn_analytics (user_id, date DESC)",
		"CREATE INDEX IF NOT EXISTS idx_churn_risk ON churn_analytics (project_id, churn_risk_category, date DESC)",
		"CREATE INDEX IF NOT EXISTS idx_churn_score ON churn_analytics (churn_risk_score DESC, date DESC)",
	}

	for _, indexSQL := range analyticsIndexes {
		if err := db.Exec(indexSQL).Error; err != nil {
			log.Printf("Warning: Could not create analytics index: %v", err)
		}
	}

	log.Println("Analytics hypertables initialization completed")
	return nil
}
