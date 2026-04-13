package main

import (
	"time"
)

// Account represents an organization/company account (main billing entity)
type Account struct {
	ID        string    `gorm:"primaryKey" json:"id"`
	Name      string    `json:"name"`                     // Company/Organization name
	Email     string    `gorm:"uniqueIndex" json:"email"` // Primary contact email
	Password  string    `json:"-"`                        // Keep for backward compatibility during migration
	AvatarURL string    `json:"avatar_url,omitempty"`     // Profile picture URL
	IsAdmin   bool      `gorm:"default:false" json:"is_admin"`
	CreatedAt time.Time `json:"created_at"`
	UpdatedAt time.Time `json:"updated_at"`

	// Email verification
	EmailVerified       bool       `gorm:"default:false" json:"email_verified"`
	VerificationToken   string     `json:"-"` // Not exposed in JSON
	VerificationSentAt  *time.Time `json:"verification_sent_at,omitempty"`
	VerificationExpires *time.Time `json:"-"` // Token expiry time

	// Password reset
	ResetPasswordToken   string     `json:"-"` // Not exposed in JSON
	ResetPasswordSentAt  *time.Time `json:"-"`
	ResetPasswordExpires *time.Time `json:"-"` // Token expiry time (1 hour)

	// Google OAuth
	GoogleID string `gorm:"index" json:"-"` // Google user ID for OAuth

	// Stripe customer info for Mentiq subscriptions
	StripeCustomerID string `json:"stripe_customer_id" gorm:"index"`
	HasPaymentMethod bool   `gorm:"default:false" json:"has_payment_method"`

	// Relations
	Users               []User               `gorm:"foreignKey:AccountID" json:"users,omitempty"`
	Projects            []Project            `gorm:"foreignKey:AccountID" json:"projects,omitempty"`
	AccountSubscription *AccountSubscription `gorm:"foreignKey:AccountID" json:"subscription,omitempty"`
}

// User represents an individual team member within an account
type User struct {
	ID        string    `gorm:"primaryKey" json:"id"`
	Email     string    `gorm:"uniqueIndex" json:"email"`
	Password  string    `json:"-"` // Don't expose password in JSON
	FullName  string    `json:"full_name"`
	Role      string    `json:"role" gorm:"default:'member'"` // owner, admin, member, viewer
	IsActive  bool      `gorm:"default:true" json:"is_active"`
	CreatedAt time.Time `json:"created_at"`
	UpdatedAt time.Time `json:"updated_at"`

	// Password reset (for team members whose password lives in the User table)
	ResetPasswordToken   string     `json:"-"`
	ResetPasswordSentAt  *time.Time `json:"-"`
	ResetPasswordExpires *time.Time `json:"-"`

	// Foreign keys
	AccountID string  `json:"account_id" gorm:"index"`
	Account   Account `gorm:"foreignKey:AccountID;references:ID" json:"account,omitempty"`

	// Relations
	ProjectMemberships []ProjectMember `gorm:"foreignKey:UserID" json:"project_memberships,omitempty"`
}

// UserInvitation represents a pending team member invitation
type UserInvitation struct {
	ID          string     `gorm:"primaryKey" json:"id"`
	Email       string     `gorm:"index;not null" json:"email"`
	Token       string     `gorm:"uniqueIndex;not null" json:"token"`
	Role        string     `json:"role" gorm:"default:'member'"`    // owner, admin, member, viewer
	Status      string     `json:"status" gorm:"default:'pending'"` // pending, accepted, expired, canceled
	AccountID   string     `gorm:"index;not null" json:"account_id"`
	InvitedByID string     `gorm:"index;not null" json:"invited_by_id"` // User ID who sent invite
	ExpiresAt   time.Time  `gorm:"not null;index" json:"expires_at"`
	AcceptedAt  *time.Time `json:"accepted_at,omitempty"`
	CreatedAt   time.Time  `json:"created_at"`
	UpdatedAt   time.Time  `json:"updated_at"`

	// Relations
	Account   Account `gorm:"foreignKey:AccountID;references:ID" json:"account,omitempty"`
	InvitedBy User    `gorm:"foreignKey:InvitedByID;references:ID" json:"invited_by,omitempty"`
}

func (UserInvitation) TableName() string {
	return "user_invitations"
}

// ProjectMember represents a user's membership and permissions in a specific project
type ProjectMember struct {
	ID        string    `gorm:"primaryKey" json:"id"`
	ProjectID string    `json:"project_id" gorm:"index"`
	UserID    string    `json:"user_id" gorm:"index"`
	Role      string    `json:"role" gorm:"default:'member'"` // owner, admin, member, viewer
	CreatedAt time.Time `json:"created_at"`
	UpdatedAt time.Time `json:"updated_at"`

	// Relations
	Project Project `gorm:"foreignKey:ProjectID;references:ID" json:"project,omitempty"`
	User    User    `gorm:"foreignKey:UserID;references:ID" json:"user,omitempty"`
}

func (ProjectMember) TableName() string {
	return "project_members"
}

// Project represents a project within an account
type Project struct {
	ID           string    `gorm:"primaryKey" json:"id"`
	Name         string    `json:"name"`
	Description  string    `json:"description" gorm:"default:''"`
	StripeAPIKey string    `json:"-" gorm:"column:stripe_api_key"` // Don't expose in JSON for security
	DodoAPIKey   string    `json:"-" gorm:"column:dodo_api_key"`   // Don't expose in JSON for security
	PolarAPIKey        string `json:"-" gorm:"column:polar_api_key"`         // Don't expose in JSON for security
	LemonSqueezyAPIKey string `json:"-" gorm:"column:lemonsqueezy_api_key"` // Don't expose in JSON for security
	CreatedAt    time.Time `json:"created_at"`
	UpdatedAt    time.Time `json:"updated_at"`

	// Foreign keys
	AccountID string  `json:"account_id" gorm:"index"`
	Account   Account `gorm:"foreignKey:AccountID;references:ID" json:"account,omitempty"`

	// Relations
	APIKeys     []APIKey        `gorm:"foreignKey:ProjectID" json:"api_keys,omitempty"`
	Experiments []Experiment    `gorm:"foreignKey:ProjectID" json:"experiments,omitempty"`
	Members     []ProjectMember `gorm:"foreignKey:ProjectID" json:"members,omitempty"`
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
	Tier         string `json:"tier" gorm:"index"`                    // starter, growth, scale
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

	// Lifetime plan: usage paused when limit hit and no payment method on file
	UsagePaused       bool       `gorm:"default:false" json:"usage_paused"`
	UsagePausedAt     *time.Time `json:"usage_paused_at,omitempty"`
	UsagePausedReason *string    `json:"usage_paused_reason,omitempty"`

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

// =====================
// PLAYBOOKS
// =====================

// Playbook represents a reusable automation workflow template
type Playbook struct {
	ID              string     `gorm:"primaryKey" json:"id"`
	ProjectID       string     `gorm:"index;not null" json:"project_id"`
	Name            string     `gorm:"not null" json:"name"`
	Description     string     `json:"description"`
	Type            string     `gorm:"index;not null" json:"type"`          // "churn_prevention", "growth_expansion", "onboarding", "engagement"
	Status          string     `gorm:"index;default:'draft'" json:"status"` // "draft", "active", "paused", "archived"
	Source          string     `gorm:"default:'manual'" json:"source"`      // "manual", "llm_generated"
	LLMPromptUsed   string     `json:"llm_prompt_used,omitempty"`           // Prompt used if LLM-generated
	LLMModelVersion string     `json:"llm_model_version,omitempty"`         // Model version if LLM-generated
	CreatedBy       string     `json:"created_by"`                          // User or Account ID who created
	ActivatedAt     *time.Time `json:"activated_at,omitempty"`
	CreatedAt       time.Time  `json:"created_at"`
	UpdatedAt       time.Time  `json:"updated_at"`

	// Relations
	Project     Project              `gorm:"foreignKey:ProjectID;references:ID" json:"project,omitempty"`
	Steps       []PlaybookStep       `gorm:"foreignKey:PlaybookID" json:"steps,omitempty"`
	Triggers    []PlaybookTrigger    `gorm:"foreignKey:PlaybookID" json:"triggers,omitempty"`
	Enrollments []PlaybookEnrollment `gorm:"foreignKey:PlaybookID" json:"enrollments,omitempty"`
}

func (Playbook) TableName() string {
	return "playbook"
}

// PlaybookStep represents a single action/step in a playbook workflow
type PlaybookStep struct {
	ID           string                 `gorm:"primaryKey" json:"id"`
	PlaybookID   string                 `gorm:"index;not null" json:"playbook_id"`
	StepOrder    int                    `gorm:"not null" json:"step_order"`
	Name         string                 `gorm:"not null" json:"name"`
	Description  string                 `json:"description"`
	ActionType   string                 `gorm:"not null" json:"action_type"` // "email", "in_app_message", "webhook", "wait", "condition", "feature_flag"
	ActionConfig map[string]interface{} `gorm:"type:jsonb;serializer:json" json:"action_config"`
	DelayMinutes int                    `json:"delay_minutes"`                                          // Wait time before this step executes
	Conditions   map[string]interface{} `gorm:"type:jsonb;serializer:json" json:"conditions,omitempty"` // Exit/skip conditions
	IsRequired   bool                   `gorm:"default:true" json:"is_required"`
	CreatedAt    time.Time              `json:"created_at"`
	UpdatedAt    time.Time              `json:"updated_at"`

	// Relations
	Playbook Playbook `gorm:"foreignKey:PlaybookID;references:ID" json:"playbook,omitempty"`
}

func (PlaybookStep) TableName() string {
	return "playbook_step"
}

// PlaybookTrigger defines when a playbook should automatically activate for users
type PlaybookTrigger struct {
	ID              string                 `gorm:"primaryKey" json:"id"`
	PlaybookID      string                 `gorm:"index;not null" json:"playbook_id"`
	Name            string                 `json:"name"`
	TriggerType     string                 `gorm:"not null" json:"trigger_type"` // "event", "metric_threshold", "segment", "schedule"
	Conditions      map[string]interface{} `gorm:"type:jsonb;serializer:json;not null" json:"conditions"`
	IsEnabled       bool                   `gorm:"default:true" json:"is_enabled"`
	Priority        int                    `gorm:"default:0" json:"priority"` // Higher = runs first
	CooldownMinutes int                    `json:"cooldown_minutes"`          // Prevent re-triggering
	MaxEnrollments  int                    `json:"max_enrollments"`           // 0 = unlimited
	CreatedAt       time.Time              `json:"created_at"`
	UpdatedAt       time.Time              `json:"updated_at"`

	// Relations
	Playbook Playbook `gorm:"foreignKey:PlaybookID;references:ID" json:"playbook,omitempty"`
}

func (PlaybookTrigger) TableName() string {
	return "playbook_trigger"
}

// PlaybookEnrollment tracks a user's progress through a playbook
type PlaybookEnrollment struct {
	ID               string                 `gorm:"primaryKey" json:"id"`
	PlaybookID       string                 `gorm:"index;not null" json:"playbook_id"`
	ProjectID        string                 `gorm:"index;not null" json:"project_id"`
	UserID           string                 `gorm:"index;not null" json:"user_id"`
	TriggerID        *string                `json:"trigger_id,omitempty"`                 // Which trigger enrolled the user (null if manual)
	Status           string                 `gorm:"index;default:'active'" json:"status"` // "active", "completed", "exited", "paused"
	CurrentStepOrder int                    `gorm:"default:1" json:"current_step_order"`
	EnrolledAt       time.Time              `json:"enrolled_at"`
	CompletedAt      *time.Time             `json:"completed_at,omitempty"`
	ExitedAt         *time.Time             `json:"exited_at,omitempty"`
	ExitReason       string                 `json:"exit_reason,omitempty"`                              // "completed", "manual_exit", "condition_met", "goal_achieved"
	MetricsAtStart   map[string]interface{} `gorm:"type:jsonb;serializer:json" json:"metrics_at_start"` // Health score, etc at enrollment
	MetricsAtEnd     map[string]interface{} `gorm:"type:jsonb;serializer:json" json:"metrics_at_end,omitempty"`
	CreatedAt        time.Time              `json:"created_at"`
	UpdatedAt        time.Time              `json:"updated_at"`

	// Relations
	Playbook       Playbook                `gorm:"foreignKey:PlaybookID;references:ID" json:"playbook,omitempty"`
	StepExecutions []PlaybookStepExecution `gorm:"foreignKey:EnrollmentID" json:"step_executions,omitempty"`
}

func (PlaybookEnrollment) TableName() string {
	return "playbook_enrollment"
}

// PlaybookStepExecution tracks individual step execution for an enrollment
type PlaybookStepExecution struct {
	ID              string                 `gorm:"primaryKey" json:"id"`
	EnrollmentID    string                 `gorm:"index;not null" json:"enrollment_id"`
	StepID          string                 `gorm:"index;not null" json:"step_id"`
	Status          string                 `gorm:"default:'pending'" json:"status"` // "pending", "scheduled", "executing", "completed", "failed", "skipped"
	ScheduledAt     *time.Time             `json:"scheduled_at,omitempty"`
	StartedAt       *time.Time             `json:"started_at,omitempty"`
	CompletedAt     *time.Time             `json:"completed_at,omitempty"`
	FailedAt        *time.Time             `json:"failed_at,omitempty"`
	FailureReason   string                 `json:"failure_reason,omitempty"`
	RetryCount      int                    `gorm:"default:0" json:"retry_count"`
	ExecutionResult map[string]interface{} `gorm:"type:jsonb;serializer:json" json:"execution_result,omitempty"`
	CreatedAt       time.Time              `json:"created_at"`
	UpdatedAt       time.Time              `json:"updated_at"`

	// Relations
	Enrollment PlaybookEnrollment `gorm:"foreignKey:EnrollmentID;references:ID" json:"enrollment,omitempty"`
	Step       PlaybookStep       `gorm:"foreignKey:StepID;references:ID" json:"step,omitempty"`
}

func (PlaybookStepExecution) TableName() string {
	return "playbook_step_execution"
}

// PlaybookAnalytics stores aggregated metrics for playbook performance
type PlaybookAnalytics struct {
	ID                     uint      `gorm:"primaryKey" json:"id"`
	PlaybookID             string    `gorm:"index;not null" json:"playbook_id"`
	Date                   time.Time `gorm:"index" json:"date"`
	TotalEnrollments       int       `json:"total_enrollments"`
	ActiveEnrollments      int       `json:"active_enrollments"`
	CompletedEnrollments   int       `json:"completed_enrollments"`
	ExitedEnrollments      int       `json:"exited_enrollments"`
	CompletionRate         float64   `json:"completion_rate"`
	AvgTimeToComplete      int       `json:"avg_time_to_complete"`     // In minutes
	ChurnPrevented         int       `json:"churn_prevented"`          // Users who improved from at-risk
	RevenueImpact          int64     `json:"revenue_impact"`           // In cents
	HealthScoreImprovement float64   `json:"health_score_improvement"` // Average improvement
	CreatedAt              time.Time `json:"created_at"`
	UpdatedAt              time.Time `json:"updated_at"`

	// Relations
	Playbook Playbook `gorm:"foreignKey:PlaybookID;references:ID" json:"playbook,omitempty"`
}

func (PlaybookAnalytics) TableName() string {
	return "playbook_analytics"
}

// LLMPlaybookGeneration tracks LLM generation requests for playbooks
type LLMPlaybookGeneration struct {
	ID             string                 `gorm:"primaryKey" json:"id"`
	ProjectID      string                 `gorm:"index;not null" json:"project_id"`
	PlaybookType   string                 `json:"playbook_type"` // "churn_prevention", "growth_expansion"
	InputContext   map[string]interface{} `gorm:"type:jsonb;serializer:json" json:"input_context"`
	PromptUsed     string                 `json:"prompt_used"`
	LLMResponse    string                 `json:"llm_response"`
	ParsedPlaybook map[string]interface{} `gorm:"type:jsonb;serializer:json" json:"parsed_playbook"`
	Status         string                 `json:"status"` // "pending", "generating", "completed", "failed", "applied"
	ErrorMessage   string                 `json:"error_message,omitempty"`
	PlaybookID     *string                `json:"playbook_id,omitempty"` // Set when converted to actual playbook
	ModelUsed      string                 `json:"model_used"`            // e.g., "claude-3-5-sonnet"
	TokensUsed     int                    `json:"tokens_used"`
	CostCents      int                    `json:"cost_cents"`
	CreatedAt      time.Time              `json:"created_at"`
	CompletedAt    *time.Time             `json:"completed_at,omitempty"`

	// Relations
	Project  Project   `gorm:"foreignKey:ProjectID;references:ID" json:"project,omitempty"`
	Playbook *Playbook `gorm:"foreignKey:PlaybookID;references:ID" json:"playbook,omitempty"`
}

func (LLMPlaybookGeneration) TableName() string {
	return "llm_playbook_generation"
}

// Waitlist represents a user who signed up for the waitlist
type Waitlist struct {
	ID               string     `gorm:"primaryKey" json:"id"`
	Email            string     `gorm:"uniqueIndex;not null" json:"email"`
	FullName         string     `gorm:"not null" json:"full_name"`
	Company          string     `json:"company"`
	UserCount        int        `json:"user_count"` // Optional: expected number of users
	Source           string     `json:"source"`     // e.g., "landing_page", "pricing", "blog"
	EmailSent        bool       `gorm:"default:false" json:"email_sent"`
	PromoEmailsOptIn bool       `gorm:"default:true" json:"promo_emails_opt_in"` // User consented to promotional emails
	UnsubscribeToken string     `gorm:"uniqueIndex" json:"-"`                    // Token for secure unsubscribe links
	UnsubscribedAt   *time.Time `json:"unsubscribed_at,omitempty"`               // When user unsubscribed
	AccessGranted    bool       `gorm:"default:false" json:"access_granted"`     // Whether access was granted
	AccessGrantedAt  *time.Time `json:"access_granted_at,omitempty"`             // When access was granted
	AccessGrantedBy  string     `json:"access_granted_by,omitempty"`             // Admin account ID who granted access
	CreatedAt        time.Time  `json:"created_at"`
	UpdatedAt        time.Time  `json:"updated_at"`
}

func (Waitlist) TableName() string {
	return "waitlist"
}

// =====================
// INTEGRATIONS
// =====================

// ProjectIntegration stores OAuth credentials for third-party services (Mailchimp, SendGrid, etc.)
type ProjectIntegration struct {
	ID           string                 `gorm:"primaryKey" json:"id"`
	ProjectID    string                 `gorm:"index;not null" json:"project_id"`
	Provider     string                 `gorm:"index;not null" json:"provider"` // "mailchimp", "sendgrid", "customer_io"
	AccessToken  string                 `json:"-"`                              // Encrypted, not exposed in JSON
	RefreshToken string                 `json:"-"`                              // Encrypted, not exposed in JSON
	ExpiresAt    *time.Time             `json:"expires_at,omitempty"`
	Settings     map[string]interface{} `gorm:"type:jsonb;serializer:json" json:"settings"` // Provider-specific config
	IsActive     bool                   `gorm:"default:true" json:"is_active"`
	LastSyncAt   *time.Time             `json:"last_sync_at,omitempty"`
	SyncStatus   string                 `gorm:"default:'idle'" json:"sync_status"` // "idle", "syncing", "error"
	LastError    string                 `json:"last_error,omitempty"`
	CreatedAt    time.Time              `json:"created_at"`
	UpdatedAt    time.Time              `json:"updated_at"`

	// Relations
	Project  Project              `gorm:"foreignKey:ProjectID;references:ID" json:"project,omitempty"`
	SyncLogs []IntegrationSyncLog `gorm:"foreignKey:IntegrationID" json:"sync_logs,omitempty"`
}

func (ProjectIntegration) TableName() string {
	return "project_integration"
}

// IntegrationSyncLog tracks sync operations for integrations
type IntegrationSyncLog struct {
	ID              string    `gorm:"primaryKey" json:"id"`
	IntegrationID   string    `gorm:"index;not null" json:"integration_id"`
	SyncType        string    `json:"sync_type"`        // "auto", "manual", "playbook"
	ContactsSynced  int       `json:"contacts_synced"`  // Number of contacts successfully synced
	ContactsFailed  int       `json:"contacts_failed"`  // Number of contacts that failed to sync
	ContactsSkipped int       `json:"contacts_skipped"` // Already synced, no update needed
	ErrorMessage    string    `json:"error_message,omitempty"`
	Duration        int       `json:"duration"` // Duration in milliseconds
	CreatedAt       time.Time `json:"created_at"`

	// Relations
	Integration ProjectIntegration `gorm:"foreignKey:IntegrationID;references:ID" json:"integration,omitempty"`
}

func (IntegrationSyncLog) TableName() string {
	return "integration_sync_log"
}

// MailchimpSettings stores Mailchimp-specific configuration
type MailchimpSettings struct {
	ServerPrefix  string `json:"server_prefix"`  // e.g., "us21" - extracted from OAuth metadata
	AudienceID    string `json:"audience_id"`    // Selected Mailchimp list/audience
	AudienceName  string `json:"audience_name"`  // Display name
	SyncHighRisk  bool   `json:"sync_high_risk"` // Auto-sync at-risk users
	RiskThreshold int    `json:"risk_threshold"` // Churn risk score threshold (default: 70)
	AddTags       bool   `json:"add_tags"`       // Tag synced contacts
	TagName       string `json:"tag_name"`       // e.g., "churn_risk_high"
}

// =====================
// AUTOMATION SYSTEM
// =====================

// AutomationSettings stores configuration for automated email campaigns
type AutomationSettings struct {
	ID          string                 `gorm:"primaryKey" json:"id"`
	ProjectID   string                 `gorm:"index;not null" json:"project_id"`
	Name        string                 `gorm:"not null" json:"name"`
	Description string                 `json:"description"`
	Type        string                 `gorm:"index;not null" json:"type"` // "churn_prevention", "feature_adoption", "engagement"
	IsEnabled   bool                   `gorm:"default:true" json:"is_enabled"`
	Config      map[string]interface{} `gorm:"type:jsonb;serializer:json" json:"config"`
	CreatedAt   time.Time              `json:"created_at"`
	UpdatedAt   time.Time              `json:"updated_at"`

	// Custom prompt for AI email generation — overrides default prompt when set
	CustomPrompt string `gorm:"type:text" json:"custom_prompt,omitempty"`

	// Relations
	Project Project `gorm:"foreignKey:ProjectID;references:ID" json:"project,omitempty"`
}

func (AutomationSettings) TableName() string {
	return "automation_settings"
}

// EmailTemplate stores templates for automated emails (generated by Claude)
type EmailTemplate struct {
	ID                  string     `gorm:"primaryKey" json:"id"`
	ProjectID           string     `gorm:"index;not null" json:"project_id"`
	Name                string     `gorm:"not null" json:"name"`
	Type                string     `gorm:"index;not null" json:"type"` // "churn_prevention", "feature_adoption", "engagement", "discount"
	SubjectTemplate     string     `json:"subject_template"`
	ContentTemplate     string     `json:"content_template"`
	PersonalizationVars []string   `gorm:"type:jsonb;serializer:json" json:"personalization_vars"`
	IsActive            bool       `gorm:"default:true" json:"is_active"`
	LastGeneratedAt     *time.Time `json:"last_generated_at,omitempty"`
	GenerationPrompt    string     `json:"generation_prompt,omitempty"`
	CreatedAt           time.Time  `json:"created_at"`
	UpdatedAt           time.Time  `json:"updated_at"`

	// Relations
	Project Project `gorm:"foreignKey:ProjectID;references:ID" json:"project,omitempty"`
}

func (EmailTemplate) TableName() string {
	return "email_template"
}

// DiscountCode stores discount codes for churn prevention campaigns
type DiscountCode struct {
	ID              string     `gorm:"primaryKey" json:"id"`
	ProjectID       string     `gorm:"index;not null" json:"project_id"`
	Code            string     `gorm:"uniqueIndex;not null" json:"code"`
	DiscountPercent int        `json:"discount_percent"`
	ValidUntil      *time.Time `json:"valid_until,omitempty"`
	MaxUses         int        `json:"max_uses"`
	UsedCount       int        `gorm:"default:0" json:"used_count"`
	IsActive        bool       `gorm:"default:true" json:"is_active"`
	AutomationID    *string    `json:"automation_id,omitempty"`
	UserID          *string    `json:"user_id,omitempty"` // Specific user assignment
	CreatedAt       time.Time  `json:"created_at"`
	UpdatedAt       time.Time  `json:"updated_at"`

	// Relations
	Project Project `gorm:"foreignKey:ProjectID;references:ID" json:"project,omitempty"`
}

func (DiscountCode) TableName() string {
	return "discount_code"
}

// AutomationExecution tracks execution of automated campaigns
type AutomationExecution struct {
	ID              string                 `gorm:"primaryKey" json:"id"`
	AutomationID    string                 `gorm:"index;not null" json:"automation_id"`
	ProjectID       string                 `gorm:"index;not null" json:"project_id"`
	UserID          string                 `gorm:"index;not null" json:"user_id"`
	EmailTemplateID string                 `json:"email_template_id"`
	CampaignID      *string                `json:"campaign_id,omitempty"`           // Mailchimp campaign ID
	Status          string                 `gorm:"default:'pending'" json:"status"` // "pending", "sent", "failed", "skipped"
	TriggerReason   string                 `json:"trigger_reason"`
	Personalization map[string]interface{} `gorm:"type:jsonb;serializer:json" json:"personalization"`
	ExecutionResult map[string]interface{} `gorm:"type:jsonb;serializer:json" json:"execution_result,omitempty"`
	ScheduledAt     *time.Time             `json:"scheduled_at,omitempty"`
	SentAt          *time.Time             `json:"sent_at,omitempty"`
	FailedAt        *time.Time             `json:"failed_at,omitempty"`
	FailureReason   string                 `json:"failure_reason,omitempty"`
	CreatedAt       time.Time              `json:"created_at"`
	UpdatedAt       time.Time              `json:"updated_at"`

	// Stored email content — persisted for customer viewing and auditing
	EmailSubject   string `json:"email_subject,omitempty"`
	EmailHTML      string `gorm:"type:text" json:"email_html,omitempty"`
	EmailPlainText string `gorm:"type:text" json:"email_plain_text,omitempty"`

	// Relations
	Automation    AutomationSettings `gorm:"foreignKey:AutomationID;references:ID" json:"automation,omitempty"`
	EmailTemplate *EmailTemplate     `gorm:"foreignKey:EmailTemplateID;references:ID" json:"email_template,omitempty"`
}

func (AutomationExecution) TableName() string {
	return "automation_execution"
}

// ChurnPreventionConfig defines settings for churn prevention automation
type ChurnPreventionConfig struct {
	Enabled             bool   `json:"enabled"`
	RiskThreshold       int    `json:"risk_threshold"`      // 0-100
	DiscountPercentage  int    `json:"discount_percentage"` // e.g., 20 for 20%
	EmailTemplateID     string `json:"email_template_id"`
	CooldownDays        int    `json:"cooldown_days"`          // Prevent spam
	MaxCampaignsPerUser int    `json:"max_campaigns_per_user"` // Limit campaigns per user
}

// FeatureAdoptionConfig defines settings for feature adoption automation
type FeatureAdoptionConfig struct {
	Enabled                 bool     `json:"enabled"`
	UnusedFeaturesThreshold int      `json:"unused_features_threshold"` // days
	EmailTemplateID         string   `json:"email_template_id"`
	TargetFeatures          []string `json:"target_features"`   // Feature names to promote
	MinUserActivity         int      `json:"min_user_activity"` // minimum events to qualify
}

// EngagementAutomationConfig defines settings for engagement automation
type EngagementAutomationConfig struct {
	Enabled             bool   `json:"enabled"`
	EngagementThreshold int    `json:"engagement_threshold"` // low engagement score
	EmailTemplateID     string `json:"email_template_id"`
	InactivityDays      int    `json:"inactivity_days"`
	MinSessionCount     int    `json:"min_session_count"`
}

// AccountLimits stores the effective limits for an account.
// Override fields (nullable) take precedence over plan defaults when set by an admin.
type AccountLimits struct {
	ID        string    `gorm:"primaryKey" json:"id"`
	AccountID string    `gorm:"uniqueIndex;not null" json:"account_id"`
	CreatedAt time.Time `json:"created_at"`
	UpdatedAt time.Time `json:"updated_at"`

	// Admin overrides — nullable; nil means "use plan default"
	PaidUsersOverride       *int `json:"paid_users_override"`
	SessionReplaysOverride  *int `json:"session_replays_override"`
	AutomatedEmailsOverride *int `json:"automated_emails_override"`
	AIGenerationsOverride   *int `json:"ai_generations_override"`
	TeamMembersOverride     *int `json:"team_members_override"`

	// Relations
	Account Account `gorm:"foreignKey:AccountID;references:ID" json:"account,omitempty"`
}

func (AccountLimits) TableName() string {
	return "account_limits"
}

// AccountUsage tracks resource consumption for the current billing period.
// Counters are reset at the start of each billing cycle.
type AccountUsage struct {
	ID        string    `gorm:"primaryKey" json:"id"`
	AccountID string    `gorm:"uniqueIndex;not null" json:"account_id"`
	CreatedAt time.Time `json:"created_at"`
	UpdatedAt time.Time `json:"updated_at"`

	// Billing period this usage belongs to
	BillingPeriodStart time.Time `json:"billing_period_start"`
	BillingPeriodEnd   time.Time `json:"billing_period_end"`

	// Usage counters
	PaidUsersCount       int `json:"paid_users_count"`
	SessionReplaysCount  int `json:"session_replays_count"`
	AutomatedEmailsCount int `json:"automated_emails_count"`
	AIGenerationsCount   int `json:"ai_generations_count"`
	TeamMembersCount     int `json:"team_members_count"` // peak count during billing period

	// Relations
	Account Account `gorm:"foreignKey:AccountID;references:ID" json:"account,omitempty"`
}

func (AccountUsage) TableName() string {
	return "account_usage"
}

// UsageHistory stores a snapshot of usage at the end of each billing period.
type UsageHistory struct {
	ID        string    `gorm:"primaryKey" json:"id"`
	AccountID string    `gorm:"not null;uniqueIndex:idx_usage_history_account_period" json:"account_id"`
	CreatedAt time.Time `json:"created_at"`

	// Billing period this snapshot covers
	BillingPeriodStart time.Time `gorm:"not null;uniqueIndex:idx_usage_history_account_period" json:"billing_period_start"`
	BillingPeriodEnd   time.Time `json:"billing_period_end"`
	Tier               string    `json:"tier"`

	// Final usage counts
	PaidUsersCount       int `json:"paid_users_count"`
	SessionReplaysCount  int `json:"session_replays_count"`
	AutomatedEmailsCount int `json:"automated_emails_count"`
	AIGenerationsCount   int `json:"ai_generations_count"`
	TeamMembersCount     int `json:"team_members_count"`

	// Calculated overages and costs (cents)
	TotalOverageCost int64 `json:"total_overage_cost"`
	ProjectedBill    int64 `json:"projected_bill"`

	// Relations
	Account Account `gorm:"foreignKey:AccountID;references:ID" json:"account,omitempty"`
}

func (UsageHistory) TableName() string {
	return "usage_history"
}

// UsageAuditLog records individual usage change events for debugging and support.
type UsageAuditLog struct {
	ID        string    `gorm:"primaryKey" json:"id"`
	AccountID string    `gorm:"index;not null" json:"account_id"`
	CreatedAt time.Time `gorm:"index" json:"created_at"`

	Resource  string `json:"resource"`   // e.g. "paid_users", "session_replays"
	Action    string `json:"action"`     // "increment", "set", "reset"
	Amount    int    `json:"amount"`     // delta or absolute value
	NewTotal  int    `json:"new_total"`  // counter value after this change
	Source    string `json:"source"`     // what triggered the change, e.g. "api", "webhook", "cron"
	Detail    string `json:"detail"`     // optional extra context
}

func (UsageAuditLog) TableName() string {
	return "usage_audit_log"
}

// LifetimeKey is a one-time activation key for the lifetime plan.
// Keys are generated by admins and redeemed by users.
type LifetimeKey struct {
	ID        string     `gorm:"primaryKey" json:"id"`
	Key       string     `gorm:"uniqueIndex;not null" json:"key"` // the activation key string
	CreatedAt time.Time  `json:"created_at"`
	CreatedBy string     `gorm:"not null" json:"created_by"` // admin account ID that generated it

	// Redemption
	RedeemedAt  *time.Time `json:"redeemed_at"`
	RedeemedBy  *string    `gorm:"index" json:"redeemed_by"` // account ID that redeemed it
	IsRedeemed  bool       `gorm:"default:false;index" json:"is_redeemed"`

	// Optional metadata
	Note string `json:"note"` // admin note, e.g. "AppSumo batch 1"
}

func (LifetimeKey) TableName() string {
	return "lifetime_keys"
}

// ProjectSettings stores per-project configuration like email character limits.
type ProjectSettings struct {
	ID        string    `gorm:"primaryKey" json:"id"`
	ProjectID string    `gorm:"uniqueIndex;not null" json:"project_id"`
	CreatedAt time.Time `json:"created_at"`
	UpdatedAt time.Time `json:"updated_at"`

	// Email limits
	MaxEmailCharacters int `gorm:"default:0" json:"max_email_characters"` // 0 = no limit

	// Relations
	Project Project `gorm:"foreignKey:ProjectID;references:ID" json:"project,omitempty"`
}

func (ProjectSettings) TableName() string {
	return "project_settings"
}
