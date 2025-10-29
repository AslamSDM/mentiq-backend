package main

import (
	"time"
)

// Account represents a user account
type Account struct {
	ID        string    `gorm:"primaryKey" json:"id"`
	Name      string    `json:"name"`
	Email     string    `gorm:"uniqueIndex" json:"email"`
	Password  string    `json:"password"`
	CreatedAt time.Time `json:"created_at"`
	UpdatedAt time.Time `json:"updated_at"`

	// Relations
	Users    []User    `gorm:"foreignKey:AccountID" json:"users,omitempty"`
	Projects []Project `gorm:"foreignKey:AccountID" json:"projects,omitempty"`
}

// User represents a user in an account
type User struct {
	ID        string    `gorm:"primaryKey" json:"id"`
	Email     string    `gorm:"uniqueIndex" json:"email"`
	Password  string    `json:"-"` // Don't expose password in JSON
	CreatedAt time.Time `json:"created_at"`
	UpdatedAt time.Time `json:"updated_at"`

	// Foreign keys
	AccountID string  `json:"account_id"`
	Account   Account `gorm:"foreignKey:AccountID;references:ID" json:"account,omitempty"`
}

// Project represents a project
type Project struct {
	ID        string    `gorm:"primaryKey" json:"id"`
	Name      string    `json:"name"`
	CreatedAt time.Time `json:"created_at"`
	UpdatedAt time.Time `json:"updated_at"`

	// Foreign keys
	AccountID string  `json:"account_id"`
	Account   Account `gorm:"foreignKey:AccountID;references:ID" json:"account,omitempty"`

	// Relations
	APIKeys     []APIKey     `gorm:"foreignKey:ProjectID" json:"api_keys,omitempty"`
	Experiments []Experiment `gorm:"foreignKey:ProjectID" json:"experiments,omitempty"`
}

// APIKey represents an API key for a project
type APIKey struct {
	ID        string    `gorm:"primaryKey" json:"id"`
	Name      string    `json:"name"`
	Key       string    `gorm:"uniqueIndex" json:"key"`
	IsActive  bool      `gorm:"default:true" json:"is_active"`
	CreatedAt time.Time `json:"created_at"`
	UpdatedAt time.Time `json:"updated_at"`

	// Foreign keys
	ProjectID string  `json:"project_id"`
	Project   Project `gorm:"foreignKey:ProjectID;references:ID" json:"project,omitempty"`

	// JSONB field for permissions
	Permissions []string `gorm:"type:jsonb;serializer:json" json:"permissions"`
}

// Experiment represents an A/B test experiment
type Experiment struct {
	ID           string     `gorm:"primaryKey" json:"id"`
	Name         string     `json:"name"`
	Description  *string    `json:"description"`
	Key          string     `json:"key"`
	Status       string     `json:"status"` // DRAFT, RUNNING, PAUSED, COMPLETED, ARCHIVED
	TrafficSplit float64    `json:"traffic_split"`
	StartDate    *time.Time `json:"start_date"`
	EndDate      *time.Time `json:"end_date"`
	CreatedAt    time.Time  `json:"created_at"`
	UpdatedAt    time.Time  `json:"updated_at"`

	// Foreign keys
	ProjectID string  `json:"project_id"`
	Project   Project `gorm:"foreignKey:ProjectID;references:ID" json:"project,omitempty"`

	// Relations
	Variants         []Variant              `gorm:"foreignKey:ExperimentID" json:"variants,omitempty"`
	Assignments      []ExperimentAssignment `gorm:"foreignKey:ExperimentID" json:"assignments,omitempty"`
	ConversionEvents []ConversionEvent      `gorm:"foreignKey:ExperimentID" json:"conversion_events,omitempty"`
}

// Variant represents a variant in an experiment
type Variant struct {
	ID           string    `gorm:"primaryKey" json:"id"`
	Name         string    `json:"name"`
	Key          string    `json:"key"`
	Description  *string   `json:"description"`
	IsControl    bool      `json:"is_control"`
	TrafficSplit float64   `json:"traffic_split"`
	CreatedAt    time.Time `json:"created_at"`
	UpdatedAt    time.Time `json:"updated_at"`

	// Foreign keys
	ExperimentID string     `json:"experiment_id"`
	Experiment   Experiment `gorm:"foreignKey:ExperimentID;references:ID" json:"experiment,omitempty"`

	// Relations
	Assignments      []ExperimentAssignment `gorm:"foreignKey:VariantID" json:"assignments,omitempty"`
	ConversionEvents []ConversionEvent      `gorm:"foreignKey:VariantID" json:"conversion_events,omitempty"`
}

// ExperimentAssignment represents a user's assignment to a variant
type ExperimentAssignment struct {
	ID          string    `gorm:"primaryKey" json:"id"`
	UserID      *string   `json:"user_id"`
	AnonymousID *string   `json:"anonymous_id"`
	CreatedAt   time.Time `json:"created_at"`
	UpdatedAt   time.Time `json:"updated_at"`

	// Foreign keys
	ExperimentID string     `json:"experiment_id"`
	Experiment   Experiment `gorm:"foreignKey:ExperimentID;references:ID" json:"experiment,omitempty"`

	VariantID string  `json:"variant_id"`
	Variant   Variant `gorm:"foreignKey:VariantID;references:ID" json:"variant,omitempty"`
}

// ConversionEvent represents a conversion event in an experiment
type ConversionEvent struct {
	ID          string    `gorm:"primaryKey" json:"id"`
	EventName   string    `json:"event_name"`
	EventValue  *float64  `json:"event_value"`
	UserID      *string   `json:"user_id"`
	AnonymousID *string   `json:"anonymous_id"`
	CreatedAt   time.Time `json:"created_at"`
	UpdatedAt   time.Time `json:"updated_at"`

	// JSONB field for properties
	Properties []byte `gorm:"type:jsonb" json:"properties"`

	// Foreign keys
	ExperimentID string     `json:"experiment_id"`
	Experiment   Experiment `gorm:"foreignKey:ExperimentID;references:ID" json:"experiment,omitempty"`

	VariantID string  `json:"variant_id"`
	Variant   Variant `gorm:"foreignKey:VariantID;references:ID" json:"variant,omitempty"`
}

// TableName specifies the table name for GORM
func (Account) TableName() string {
	return "account"
}

func (User) TableName() string {
	return "user"
}

func (Project) TableName() string {
	return "project"
}

func (APIKey) TableName() string {
	return "api_key"
}

func (Experiment) TableName() string {
	return "experiment"
}

func (Variant) TableName() string {
	return "variant"
}

func (ExperimentAssignment) TableName() string {
	return "experiment_assignment"
}

func (ConversionEvent) TableName() string {
	return "conversion_event"
}
