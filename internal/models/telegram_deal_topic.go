package models

import "time"

// TelegramDealTopic stores only the Telegram topic associated with a Bigin
// deal. Bigin remains the source of truth for the deal itself.
type TelegramDealTopic struct {
	DealID          string    `gorm:"primaryKey"`
	MessageThreadID int64     `gorm:"not null"`
	CreatedAt       time.Time `gorm:"not null;autoCreateTime"`
}

// TableName returns the table used for Bigin deal-to-topic associations.
func (TelegramDealTopic) TableName() string {
	return "telegram_deal_topics"
}
