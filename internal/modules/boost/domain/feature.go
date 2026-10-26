package domain

import "time"

// Feature 代练功能。DefinitionJSON 是当前版本经过 Normalize 的功能定义
// （格式见 docs/boost-feature-schema.md），按原文保存以便比较内容是否变化。
// 同一游戏内 Code 唯一，创建后不可修改。
type Feature struct {
	ID             uint      `gorm:"primarykey" json:"id"`
	GameID         uint      `gorm:"not null;uniqueIndex:idx_boost_features_game_code,priority:1" json:"game_id"`
	Code           string    `gorm:"type:varchar(32);not null;uniqueIndex:idx_boost_features_game_code,priority:2" json:"code"`
	Status         string    `gorm:"type:varchar(16);not null;default:'draft';index" json:"status"`
	Sort           int       `gorm:"not null" json:"sort"`
	Version        int       `gorm:"not null;default:1" json:"version"`
	DefinitionJSON string    `gorm:"column:definition_json;type:text;not null" json:"definition_json"`
	CreatedAt      time.Time `json:"created_at"`
	UpdatedAt      time.Time `json:"updated_at"`
}

func (Feature) TableName() string { return "boost_features" }

// FeatureVersion 功能定义的历史版本，主键 (feature_id, version)。
type FeatureVersion struct {
	FeatureID      uint      `gorm:"primaryKey;autoIncrement:false" json:"feature_id"`
	Version        int       `gorm:"primaryKey;autoIncrement:false" json:"version"`
	DefinitionJSON string    `gorm:"column:definition_json;type:text;not null" json:"definition_json"`
	AdminID        *uint     `gorm:"index" json:"admin_id,omitempty"`
	CreatedAt      time.Time `json:"created_at"`
}

func (FeatureVersion) TableName() string { return "boost_feature_versions" }
