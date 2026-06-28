package model

import (
	"time"

	"github.com/google/uuid"
	"gorm.io/gorm"
)

// Base 基础模型，包含 ID 和 timestamps
type Base struct {
	ID        uuid.UUID `gorm:"type:uuid;primaryKey;default:gen_random_uuid()"`
	CreatedAt time.Time
	UpdatedAt time.Time
	DeletedAt gorm.DeletedAt `gorm:"index"`
}
