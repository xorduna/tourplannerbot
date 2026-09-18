package models

import "time"

const (
	// LLMRequestStatusSucceeded identifies a completed provider request.
	LLMRequestStatusSucceeded = "succeeded"
	// LLMRequestStatusFailed identifies a provider request that returned an error.
	LLMRequestStatusFailed = "failed"
)

// LLMRequest is an auditable LLM call. It deliberately stores metadata and
// usage only; conversation text remains in messages.
type LLMRequest struct {
	ID                    uint64 `gorm:"primaryKey"`
	CreatedAt             time.Time
	SourceMessageID       *uint64
	ChatID                int64
	MessageThreadID       int
	TelegramUserID        *int64
	Provider              string
	Model                 string
	Operation             string
	ProviderResponseID    *string
	Status                string
	DurationMS            int
	InputTokens           *int64
	CachedInputTokens     *int64
	OutputTokens          *int64
	ReasoningTokens       *int64
	TotalTokens           *int64
	EstimatedCostMicroUSD *int64
	PricingVersion        *string
	ErrorMessage          *string
}

// TableName returns the database table name used for persisted LLM requests.
func (LLMRequest) TableName() string { return "llm_requests" }
