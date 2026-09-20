package models

import (
	"encoding/json"
	"time"
)

// DraftKind identifies the communication channel or generic purpose of a draft.
type DraftKind string

const (
	// DraftKindEmail identifies a draft intended for email.
	DraftKindEmail DraftKind = "email"
	// DraftKindWhatsApp identifies a draft intended for WhatsApp.
	DraftKindWhatsApp DraftKind = "whatsapp"
	// DraftKindGeneric identifies a draft without a channel-specific format.
	DraftKindGeneric DraftKind = "generic"
)

// IsValid reports whether draftKind is supported by the drafts database constraint.
func (draftKind DraftKind) IsValid() bool {
	return draftKind == DraftKindEmail || draftKind == DraftKindWhatsApp || draftKind == DraftKindGeneric
}

// DraftStatus describes whether a draft is the active draft for its conversation.
type DraftStatus string

const (
	// DraftStatusActive marks the one draft currently active in a conversation.
	DraftStatusActive DraftStatus = "active"
	// DraftStatusSuperseded marks a draft replaced by a newer conversation draft.
	DraftStatusSuperseded DraftStatus = "superseded"
)

// Draft is the persistent, canonical representation of editable assistant text.
// ContentJSON is a Tiptap/ProseMirror document; BodyText is its plain-text projection.
type Draft struct {
	ID                string    `gorm:"type:uuid;primaryKey"`
	ChatID            int64     `gorm:"not null"`
	MessageThreadID   int       `gorm:"not null"`
	OwnerTelegramID   int64     `gorm:"not null"`
	Kind              DraftKind `gorm:"not null"`
	Subject           *string
	ContentJSON       json.RawMessage `gorm:"type:jsonb;not null"`
	BodyText          string          `gorm:"not null"`
	Status            DraftStatus     `gorm:"not null"`
	Revision          int             `gorm:"not null"`
	TelegramMessageID *int64
	CreatedAt         time.Time `gorm:"not null;autoCreateTime"`
	UpdatedAt         time.Time `gorm:"not null;autoCreateTime;autoUpdateTime"`
}

// TableName returns the table used to persist drafts.
func (Draft) TableName() string {
	return "drafts"
}
