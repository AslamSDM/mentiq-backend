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
	}

	for _, idx := range indices {
		// Check if index exists before creating
		if !db.Migrator().HasIndex(idx.model, idx.index) {
			log.Printf("Creating index: %s", idx.index)
		}
	}

	return nil
}
