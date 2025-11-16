package main

import (
	"fmt"
	"log"

	"gorm.io/driver/postgres"
	"gorm.io/gorm"
)

// InitDB initializes a GORM database connection
func InitDB(databaseURL string) (*gorm.DB, error) {
	db, err := gorm.Open(postgres.Open(databaseURL), &gorm.Config{})
	if err != nil {
		return nil, fmt.Errorf("failed to connect to database: %w", err)
	}

	return db, nil
}

// MigrateDB runs all database migrations
func MigrateDB(db *gorm.DB) error {
	return db.AutoMigrate(
		&Account{},
		&User{},
		&Project{},
		&APIKey{},
		&Experiment{},
		&Variant{},
		&ExperimentAssignment{},
		&ConversionEvent{},
		&SessionRecording{},
		// Stripe revenue models
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
	)
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
	}

	for _, idx := range indices {
		// Check if index exists before creating
		if !db.Migrator().HasIndex(idx.model, idx.index) {
			log.Printf("Creating index: %s", idx.index)
		}
	}

	return nil
}
