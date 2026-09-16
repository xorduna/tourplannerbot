// Package models contains the GORM models used by the application.
package models

import "time"

// AllowedUser is a Telegram user authorized to use the bot.
type AllowedUser struct {
	TelegramID int64     `gorm:"primaryKey"`
	Name       string    `gorm:"not null"`
	CreatedAt  time.Time `gorm:"not null;autoCreateTime"`
}

// TableName returns the table used to persist authorized Telegram users.
func (AllowedUser) TableName() string {
	return "allowed_users"
}
