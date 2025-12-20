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
	IsAdmin   bool      `gorm:"default:false" json:"is_admin"`
	CreatedAt time.Time `json:"created_at"`
	UpdatedAt time.Time `json:"updated_at"`

	// Stripe customer info for Mentiq subscriptions
	StripeCustomerID string `json:"stripe_customer_id" gorm:"index"`

	// Relations
	Users               []User               `gorm:"foreignKey:AccountID" json:"users,omitempty"`
	Projects            []Project            `gorm:"foreignKey:AccountID" json:"projects,omitempty"`
	AccountSubscription *AccountSubscription `gorm:"foreignKey:AccountID" json:"subscription,omitempty"`
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
	ID           string    `gorm:"primaryKey" json:"id"`
	Name         string    `json:"name"`
	StripeAPIKey string    `json:"-" gorm:"column:stripe_api_key"` // Don't expose in JSON for security
	CreatedAt    time.Time `json:"created_at"`
	UpdatedAt    time.Time `json:"updated_at"`

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

// SessionRecording represents a recorded user session
type SessionRecording struct {
	ID          string  `gorm:"primaryKey" json:"id"`
	SessionID   string  `gorm:"index" json:"session_id"`
	AccountID   string  `gorm:"index" json:"account_id"`
	ProjectID   string  `gorm:"index" json:"project_id"`
	UserID      *string `json:"user_id"`
	StoragePath string  `json:"-"` // S3/R2 key (not exposed to client)
	// RecordingData stores the raw recording JSON when we save recordings in DB
	RecordingData []byte    `gorm:"type:jsonb" json:"-"`
	Duration      int       `json:"duration"` // in seconds
	StartURL      string    `json:"start_url"`
	EventCount    int       `json:"event_count"`
	CreatedAt     time.Time `json:"created_at"`
	UpdatedAt     time.Time `json:"updated_at"`
}

func (SessionRecording) TableName() string {
	return "session_recording"
}

// RefreshToken represents a refresh token for an account
type RefreshToken struct {
	ID        string    `gorm:"primaryKey" json:"id"`
	Token     string    `gorm:"uniqueIndex;not null" json:"token"`
	AccountID string    `gorm:"index;not null" json:"account_id"`
	ExpiresAt time.Time `gorm:"not null;index" json:"expires_at"`
	CreatedAt time.Time `json:"created_at"`
	IsRevoked bool      `gorm:"default:false;index" json:"is_revoked"`

	// Relations
	Account Account `gorm:"foreignKey:AccountID;references:ID" json:"account,omitempty"`
}

func (RefreshToken) TableName() string {
	return "refresh_token"
}

// AccountSubscription represents a Mentiq subscription for an account
type AccountSubscription struct {
	ID        string `gorm:"primaryKey" json:"id"`
	AccountID string `gorm:"uniqueIndex;not null" json:"account_id"` // One subscription per account

	// Subscription details
	Tier         string `json:"tier" gorm:"index"`                    // launch, traction, momentum, scale, expansion, enterprise
	Status       string `json:"status" gorm:"index;default:'active'"` // active, trialing, past_due, canceled, paused
	UserCount    int    `json:"user_count"`                           // Selected user count
	MonthlyPrice int64  `json:"monthly_price"`                        // Price in cents

	// Stripe subscription details
	StripeSubscriptionID string `json:"stripe_subscription_id" gorm:"index"`
	StripePriceID        string `json:"stripe_price_id"`
	StripeProductID      string `json:"stripe_product_id"`

	// Billing cycle
	CurrentPeriodStart time.Time `json:"current_period_start"`
	CurrentPeriodEnd   time.Time `json:"current_period_end"`
	BillingCycleAnchor time.Time `json:"billing_cycle_anchor"`

	// Trial information
	TrialStart *time.Time `json:"trial_start"`
	TrialEnd   *time.Time `json:"trial_end"`

	// Cancellation
	CancelAtPeriodEnd  bool       `json:"cancel_at_period_end"`
	CanceledAt         *time.Time `json:"canceled_at"`
	CancellationReason *string    `json:"cancellation_reason"`

	// Metadata
	CreatedAt time.Time `json:"created_at"`
	UpdatedAt time.Time `json:"updated_at"`

	// Relations
	Account        Account          `gorm:"foreignKey:AccountID;references:ID" json:"account,omitempty"`
	PaymentHistory []PaymentHistory `gorm:"foreignKey:SubscriptionID" json:"payment_history,omitempty"`
}

func (AccountSubscription) TableName() string {
	return "account_subscription"
}

// PaymentHistory represents payment transactions for account subscriptions
type PaymentHistory struct {
	ID             string `gorm:"primaryKey" json:"id"`
	SubscriptionID string `gorm:"index;not null" json:"subscription_id"`
	AccountID      string `gorm:"index;not null" json:"account_id"`

	// Payment details
	Amount   int64  `json:"amount"` // Amount in cents
	Currency string `json:"currency" gorm:"default:'usd'"`
	Status   string `json:"status" gorm:"index"` // succeeded, failed, pending, refunded

	// Stripe details
	StripeInvoiceID string `json:"stripe_invoice_id" gorm:"index"`
	StripeChargeID  string `json:"stripe_charge_id" gorm:"index"`
	StripePaymentID string `json:"stripe_payment_id"`

	// Payment metadata
	Description   string `json:"description"`
	InvoiceNumber string `json:"invoice_number"`
	InvoicePDF    string `json:"invoice_pdf"` // URL to invoice PDF

	// Dates
	PaidAt     *time.Time `json:"paid_at"`
	FailedAt   *time.Time `json:"failed_at"`
	RefundedAt *time.Time `json:"refunded_at"`

	// Refund details
	RefundAmount int64   `json:"refund_amount"` // Amount refunded in cents
	RefundReason *string `json:"refund_reason"`

	// Metadata
	CreatedAt time.Time `json:"created_at"`
	UpdatedAt time.Time `json:"updated_at"`

	// Relations
	Account      Account             `gorm:"foreignKey:AccountID;references:ID" json:"account,omitempty"`
	Subscription AccountSubscription `gorm:"foreignKey:SubscriptionID;references:ID" json:"subscription,omitempty"`
}

func (PaymentHistory) TableName() string {
	return "payment_history"
}

// StripeCustomer represents a Stripe customer record
type StripeCustomer struct {
	ID         string    `gorm:"primaryKey" json:"id"`    // Stripe customer ID
	ProjectID  string    `json:"project_id" gorm:"index"` // FK to Project
	Email      string    `json:"email"`
	Name       string    `json:"name"`
	Delinquent bool      `json:"delinquent"`
	Balance    int64     `json:"balance"` // In cents
	Currency   string    `json:"currency"`
	Created    time.Time `json:"created"` // Stripe creation time
	Deleted    bool      `json:"deleted" gorm:"default:false"`
	LastSyncAt time.Time `json:"last_sync_at"`
	CreatedAt  time.Time `json:"created_at"`
	UpdatedAt  time.Time `json:"updated_at"`

	// Relations
	Project       Project              `gorm:"foreignKey:ProjectID;references:ID" json:"project,omitempty"`
	Subscriptions []StripeSubscription `gorm:"foreignKey:CustomerID" json:"subscriptions,omitempty"`
}

// StripeSubscription represents a Stripe subscription record
type StripeSubscription struct {
	ID                 string     `gorm:"primaryKey" json:"id"`     // Stripe subscription ID
	CustomerID         string     `json:"customer_id" gorm:"index"` // FK to StripeCustomer
	ProjectID          string     `json:"project_id" gorm:"index"`  // FK to Project
	Status             string     `json:"status"`                   // active, canceled, etc.
	CurrentPeriodStart time.Time  `json:"current_period_start"`
	CurrentPeriodEnd   time.Time  `json:"current_period_end"`
	TrialStart         *time.Time `json:"trial_start"`
	TrialEnd           *time.Time `json:"trial_end"`
	CanceledAt         *time.Time `json:"canceled_at"`
	EndedAt            *time.Time `json:"ended_at"`
	StartDate          time.Time  `json:"start_date"`
	PriceID            string     `json:"price_id"`
	ProductID          string     `json:"product_id"`
	UnitAmount         int64      `json:"unit_amount"` // In cents
	Currency           string     `json:"currency"`
	Quantity           int64      `json:"quantity"`
	Interval           string     `json:"interval"` // month, year, etc.
	IntervalCount      int64      `json:"interval_count"`
	Created            time.Time  `json:"created"` // Stripe creation time
	LastSyncAt         time.Time  `json:"last_sync_at"`
	CreatedAt          time.Time  `json:"created_at"`
	UpdatedAt          time.Time  `json:"updated_at"`

	// Relations
	Customer StripeCustomer  `gorm:"foreignKey:CustomerID;references:ID" json:"customer,omitempty"`
	Project  Project         `gorm:"foreignKey:ProjectID;references:ID" json:"project,omitempty"`
	Invoices []StripeInvoice `gorm:"foreignKey:SubscriptionID" json:"invoices,omitempty"`
}

// StripeInvoice represents a Stripe invoice record
type StripeInvoice struct {
	ID             string     `gorm:"primaryKey" json:"id"`         // Stripe invoice ID
	CustomerID     string     `json:"customer_id" gorm:"index"`     // FK to StripeCustomer
	SubscriptionID *string    `json:"subscription_id" gorm:"index"` // FK to StripeSubscription (nullable)
	ProjectID      string     `json:"project_id" gorm:"index"`      // FK to Project
	Status         string     `json:"status"`                       // paid, open, draft, uncollectible, void
	AmountPaid     int64      `json:"amount_paid"`                  // In cents
	AmountDue      int64      `json:"amount_due"`                   // In cents
	Subtotal       int64      `json:"subtotal"`                     // In cents
	Total          int64      `json:"total"`                        // In cents
	Currency       string     `json:"currency"`
	PeriodStart    time.Time  `json:"period_start"`
	PeriodEnd      time.Time  `json:"period_end"`
	Created        time.Time  `json:"created"` // Stripe creation time
	DueDate        *time.Time `json:"due_date"`
	PaidAt         *time.Time `json:"paid_at"`
	LastSyncAt     time.Time  `json:"last_sync_at"`
	CreatedAt      time.Time  `json:"created_at"`
	UpdatedAt      time.Time  `json:"updated_at"`

	// Relations
	Customer     StripeCustomer      `gorm:"foreignKey:CustomerID;references:ID" json:"customer,omitempty"`
	Subscription *StripeSubscription `gorm:"foreignKey:SubscriptionID;references:ID" json:"subscription,omitempty"`
	Project      Project             `gorm:"foreignKey:ProjectID;references:ID" json:"project,omitempty"`
}

// StripeCharge represents a Stripe charge/payment record
type StripeCharge struct {
	ID             string    `gorm:"primaryKey" json:"id"`     // Stripe charge ID
	CustomerID     *string   `json:"customer_id" gorm:"index"` // FK to StripeCustomer (nullable)
	InvoiceID      *string   `json:"invoice_id" gorm:"index"`  // FK to StripeInvoice (nullable)
	ProjectID      string    `json:"project_id" gorm:"index"`  // FK to Project
	Amount         int64     `json:"amount"`                   // In cents
	AmountCaptured int64     `json:"amount_captured"`          // In cents
	AmountRefunded int64     `json:"amount_refunded"`          // In cents
	Currency       string    `json:"currency"`
	Status         string    `json:"status"` // succeeded, pending, failed
	Paid           bool      `json:"paid"`
	Refunded       bool      `json:"refunded"`
	Created        time.Time `json:"created"` // Stripe creation time
	LastSyncAt     time.Time `json:"last_sync_at"`
	CreatedAt      time.Time `json:"created_at"`
	UpdatedAt      time.Time `json:"updated_at"`

	// Relations
	Customer *StripeCustomer `gorm:"foreignKey:CustomerID;references:ID" json:"customer,omitempty"`
	Invoice  *StripeInvoice  `gorm:"foreignKey:InvoiceID;references:ID" json:"invoice,omitempty"`
	Project  Project         `gorm:"foreignKey:ProjectID;references:ID" json:"project,omitempty"`
}

// RevenueMetrics represents calculated revenue metrics for a project
type RevenueMetrics struct {
	ID                       uint      `gorm:"primaryKey" json:"id"`
	ProjectID                string    `json:"project_id" gorm:"index"`
	Date                     time.Time `json:"date" gorm:"index"` // Date for the metrics
	MRR                      int64     `json:"mrr"`               // Monthly Recurring Revenue in cents
	ARR                      int64     `json:"arr"`               // Annual Recurring Revenue in cents
	TotalRevenue             int64     `json:"total_revenue"`     // Total revenue in cents
	ActiveSubscriptions      int       `json:"active_subscriptions"`
	CanceledSubscriptions    int       `json:"canceled_subscriptions"`
	NewSubscriptions         int       `json:"new_subscriptions"`
	ChurnedSubscriptions     int       `json:"churned_subscriptions"`
	ExpansionRevenue         int64     `json:"expansion_revenue"`            // In cents
	ContractionRevenue       int64     `json:"contraction_revenue"`          // In cents
	NetRevenue               int64     `json:"net_revenue"`                  // In cents
	ChurnRate                float64   `json:"churn_rate"`                   // Percentage
	GrowthRate               float64   `json:"growth_rate"`                  // Percentage
	ARPU                     int64     `json:"arpu"`                         // Average Revenue Per User in cents
	CustomerLifetimeValue    int64     `json:"customer_lifetime_value"`      // In cents
	TrialToPayConversionRate float64   `json:"trial_to_pay_conversion_rate"` // Percentage
	CreatedAt                time.Time `json:"created_at"`
	UpdatedAt                time.Time `json:"updated_at"`

	// Relations
	Project Project `gorm:"foreignKey:ProjectID;references:ID" json:"project,omitempty"`
}

// UserSessionMetrics represents user session analytics
type UserSessionMetrics struct {
	ID                 uint      `gorm:"primaryKey" json:"id"`
	ProjectID          string    `json:"project_id" gorm:"index"`
	Date               time.Time `json:"date" gorm:"index"`
	DAU                int       `json:"dau"`                   // Daily Active Users
	WAU                int       `json:"wau"`                   // Weekly Active Users
	MAU                int       `json:"mau"`                   // Monthly Active Users
	StickinessRatio    float64   `json:"stickiness_ratio"`      // DAU/MAU
	AvgSessionDuration int       `json:"avg_session_duration"`  // In seconds
	AvgSessionsPerUser float64   `json:"avg_sessions_per_user"` // Sessions per user
	TotalSessions      int       `json:"total_sessions"`
	BounceRate         float64   `json:"bounce_rate"`      // Percentage
	ReturnUserRate     float64   `json:"return_user_rate"` // Percentage
	CreatedAt          time.Time `json:"created_at"`
	UpdatedAt          time.Time `json:"updated_at"`

	// Relations
	Project Project `gorm:"foreignKey:ProjectID;references:ID" json:"project,omitempty"`
}

// UserCohortMetrics represents retention cohort analysis
type UserCohortMetrics struct {
	ID              uint      `gorm:"primaryKey" json:"id"`
	ProjectID       string    `json:"project_id" gorm:"index"`
	CohortMonth     time.Time `json:"cohort_month" gorm:"index"` // Month users signed up
	PeriodNumber    int       `json:"period_number"`             // 0, 1, 2, 3... months after signup
	UsersInCohort   int       `json:"users_in_cohort"`           // Total users who signed up in cohort_month
	ActiveUsers     int       `json:"active_users"`              // Users still active in this period
	RetentionRate   float64   `json:"retention_rate"`            // Percentage
	RevenueRetained float64   `json:"revenue_retained"`          // Revenue retained from this cohort
	CreatedAt       time.Time `json:"created_at"`
	UpdatedAt       time.Time `json:"updated_at"`

	// Relations
	Project Project `gorm:"foreignKey:ProjectID;references:ID" json:"project,omitempty"`
}

// DeviceAnalytics represents device and platform analytics
type DeviceAnalytics struct {
	ID             uint      `gorm:"primaryKey" json:"id"`
	ProjectID      string    `json:"project_id" gorm:"index"`
	Date           time.Time `json:"date" gorm:"index"`
	Device         string    `json:"device" gorm:"index"`  // desktop, mobile, tablet
	OS             string    `json:"os" gorm:"index"`      // iOS, Android, Windows, macOS, Linux
	Browser        string    `json:"browser" gorm:"index"` // Chrome, Safari, Firefox, etc.
	Sessions       int       `json:"sessions"`             // Number of sessions
	Users          int       `json:"users"`                // Unique users
	PageViews      int       `json:"page_views"`           // Total page views
	BounceRate     float64   `json:"bounce_rate"`          // Percentage
	AvgSessionTime int       `json:"avg_session_time"`     // In seconds
	ConversionRate float64   `json:"conversion_rate"`      // Percentage
	CreatedAt      time.Time `json:"created_at"`
	UpdatedAt      time.Time `json:"updated_at"`

	// Relations
	Project Project `gorm:"foreignKey:ProjectID;references:ID" json:"project,omitempty"`
}

// LocationAnalytics represents geographical analytics
type LocationAnalytics struct {
	ID             uint      `gorm:"primaryKey" json:"id"`
	ProjectID      string    `json:"project_id" gorm:"index"`
	Date           time.Time `json:"date" gorm:"index"`
	Country        string    `json:"country" gorm:"index"`
	CountryCode    string    `json:"country_code" gorm:"index"` // ISO 3166-1 alpha-2
	City           string    `json:"city" gorm:"index"`
	Sessions       int       `json:"sessions"`
	Users          int       `json:"users"`
	PageViews      int       `json:"page_views"`
	BounceRate     float64   `json:"bounce_rate"`     // Percentage
	ConversionRate float64   `json:"conversion_rate"` // Percentage
	Revenue        int64     `json:"revenue"`         // In cents
	CreatedAt      time.Time `json:"created_at"`
	UpdatedAt      time.Time `json:"updated_at"`

	// Relations
	Project Project `gorm:"foreignKey:ProjectID;references:ID" json:"project,omitempty"`
}

// FeatureAdoption represents feature adoption analytics
type FeatureAdoption struct {
	ID                   uint      `gorm:"primaryKey" json:"id"`
	ProjectID            string    `json:"project_id" gorm:"index"`
	Date                 time.Time `json:"date" gorm:"index"`
	FeatureName          string    `json:"feature_name" gorm:"index"`
	TotalUsers           int       `json:"total_users"`             // Total active users in period
	UsersWhoTriedFeature int       `json:"users_who_tried_feature"` // Users who used feature at least once
	AdoptionRate         float64   `json:"adoption_rate"`           // Percentage
	DailyActiveFeature   int       `json:"daily_active_feature"`    // Daily users of this feature
	WeeklyActiveFeature  int       `json:"weekly_active_feature"`   // Weekly users of this feature
	MonthlyActiveFeature int       `json:"monthly_active_feature"`  // Monthly users of this feature
	FeatureStickiness    float64   `json:"feature_stickiness"`      // DAF/MAF ratio
	TimeToFirstUse       int       `json:"time_to_first_use"`       // Avg days from signup to first use
	DropoffAfterFirstUse float64   `json:"dropoff_after_first_use"` // Percentage who don't return
	CreatedAt            time.Time `json:"created_at"`
	UpdatedAt            time.Time `json:"updated_at"`

	// Relations
	Project Project `gorm:"foreignKey:ProjectID;references:ID" json:"project,omitempty"`
}

// ConversionFunnel represents conversion funnel analytics
type ConversionFunnel struct {
	ID             uint      `gorm:"primaryKey" json:"id"`
	ProjectID      string    `json:"project_id" gorm:"index"`
	Date           time.Time `json:"date" gorm:"index"`
	FunnelName     string    `json:"funnel_name" gorm:"index"` // e.g., "signup_to_paid"
	StepNumber     int       `json:"step_number"`              // 1, 2, 3, etc.
	StepName       string    `json:"step_name"`                // e.g., "signup", "onboarding", "first_purchase"
	Users          int       `json:"users"`                    // Users who reached this step
	ConversionRate float64   `json:"conversion_rate"`          // % who converted from previous step
	DropoffRate    float64   `json:"dropoff_rate"`             // % who dropped off at this step
	AvgTimeInStep  int       `json:"avg_time_in_step"`         // Avg time spent in this step (seconds)
	Revenue        int64     `json:"revenue"`                  // Revenue generated at this step (cents)
	CreatedAt      time.Time `json:"created_at"`
	UpdatedAt      time.Time `json:"updated_at"`

	// Relations
	Project Project `gorm:"foreignKey:ProjectID;references:ID" json:"project,omitempty"`
}

// ChurnAnalytics represents churn prediction and analysis
/* Lines 452-476 omitted */
type ChurnAnalytics struct {
	ID                  uint       `gorm:"primaryKey" json:"id"`
	ProjectID           string     `json:"project_id" gorm:"index"`
	Date                time.Time  `json:"date" gorm:"index"`
	UserID              string     `json:"user_id" gorm:"index"`
	ChurnRiskScore      float64    `json:"churn_risk_score"`    // 0-100 risk score
	ChurnRiskCategory   string     `json:"churn_risk_category"` // low, medium, high, critical
	LastActiveDate      time.Time  `json:"last_active_date"`
	DaysSinceLastActive int        `json:"days_since_last_active"`
	LoginFrequency      float64    `json:"login_frequency"`     // Logins per week
	FeatureUsageScore   float64    `json:"feature_usage_score"` // 0-100 based on feature adoption
	SupportTickets      int        `json:"support_tickets"`     // Number of support tickets
	NegativeFeedback    int        `json:"negative_feedback"`   // Count of negative feedback
	SubscriptionValue   int64      `json:"subscription_value"`  // Monthly value in cents
	IsChurned           bool       `json:"is_churned"`
	ChurnedAt           *time.Time `json:"churned_at"`
	ChurnReason         string     `json:"churn_reason"`
	PredictedChurnDate  *time.Time `json:"predicted_churn_date"`
	InterventionSent    bool       `json:"intervention_sent"` // Whether retention campaign was sent
	CreatedAt           time.Time  `json:"created_at"`
	UpdatedAt           time.Time  `json:"updated_at"`

	// Relations
	Project Project `gorm:"foreignKey:ProjectID;references:ID" json:"project,omitempty"`
}

// OnboardingStatus tracks the onboarding progress for an account
type OnboardingStatus struct {
	ID        string    `gorm:"primaryKey" json:"id"`
	AccountID string    `gorm:"uniqueIndex" json:"account_id"`
	CreatedAt time.Time `json:"created_at"`
	UpdatedAt time.Time `json:"updated_at"`

	// Onboarding steps
	DataConnectionShown bool       `gorm:"default:false" json:"data_connection_shown"` // Ready to connect? screen shown
	DataConnected       bool       `gorm:"default:false" json:"data_connected"`        // User chose "Yes" or uploaded data
	PlatformSelected    string     `json:"platform_selected"`                          // web, mobile, etc.
	SDKInstalled        bool       `gorm:"default:false" json:"sdk_installed"`         // SDK integration completed
	FirstEventTracked   bool       `gorm:"default:false" json:"first_event_tracked"`   // First user event received
	StripeConnected     bool       `gorm:"default:false" json:"stripe_connected"`      // Stripe API key added
	TeamMembersInvited  bool       `gorm:"default:false" json:"team_members_invited"`  // At least one team member invited
	OnboardingComplete  bool       `gorm:"default:false" json:"onboarding_complete"`   // All tasks completed
	CompletedAt         *time.Time `json:"completed_at"`

	// Timestamps for each step
	DataConnectedAt      *time.Time `json:"data_connected_at"`
	SDKInstalledAt       *time.Time `json:"sdk_installed_at"`
	FirstEventTrackedAt  *time.Time `json:"first_event_tracked_at"`
	StripeConnectedAt    *time.Time `json:"stripe_connected_at"`
	TeamMembersInvitedAt *time.Time `json:"team_members_invited_at"`

	// Relations
	Account Account `gorm:"foreignKey:AccountID;references:ID" json:"account,omitempty"`
}
