// Package domain 定义 Worker 接口的持久化模型：执行端节点、租约和请求去重。
package domain

import "time"

// 节点状态。
const (
	NodeStatusActive   = "active"
	NodeStatusDisabled = "disabled"
)

// 租约类型。
const (
	LeaseKindTask = "task"
	LeaseKindBind = "bind"
)

// Node 执行端节点。SecretCipher 是 HMAC 密钥的密文，KeyVersion 记录加密密钥版本。
type Node struct {
	ID               uint       `gorm:"primarykey" json:"id"`
	Name             string     `gorm:"type:varchar(64);not null" json:"name"`
	APIKey           string     `gorm:"column:api_key;type:varchar(64);not null;uniqueIndex" json:"api_key"`
	SecretCipher     string     `gorm:"type:text;not null" json:"-"`
	KeyVersion       int        `gorm:"not null" json:"-"`
	CapabilitiesJSON string     `gorm:"column:capabilities_json;type:text;not null" json:"capabilities_json"`
	MaxConcurrency   int        `gorm:"not null;default:1" json:"max_concurrency"`
	Status           string     `gorm:"type:varchar(16);not null;default:'active';index" json:"status"`
	LastSeenAt       *time.Time `json:"last_seen_at,omitempty"`
	LastIP           string     `gorm:"column:last_ip;type:varchar(64)" json:"last_ip"`
	Version          string     `gorm:"type:varchar(32)" json:"version"`
	CreatedAt        time.Time  `json:"created_at"`
	UpdatedAt        time.Time  `json:"updated_at"`
}

func (Node) TableName() string { return "worker_nodes" }

// Lease 租约。同一 (kind, target_id) 同时只有一个有效租约（部分唯一索引，见 gormstore）；
// 旧租约的任何写入都返回 LEASE_LOST。
type Lease struct {
	Token       string     `gorm:"type:varchar(64);primaryKey" json:"-"`
	Kind        string     `gorm:"type:varchar(16);not null;index:idx_worker_leases_target,priority:1" json:"kind"`
	TargetID    uint       `gorm:"not null;index:idx_worker_leases_target,priority:2" json:"target_id"`
	WorkerID    uint       `gorm:"not null;index" json:"worker_id"`
	Active      bool       `gorm:"not null;index" json:"active"`
	ExpiresAt   time.Time  `gorm:"not null;index" json:"expires_at"`
	ClosedAt    *time.Time `json:"closed_at,omitempty"`
	CloseReason string     `gorm:"type:varchar(32)" json:"close_reason"`
	CreatedAt   time.Time  `json:"created_at"`
}

func (Lease) TableName() string { return "worker_leases" }

// Request Worker 写请求的 request_id 去重记录，主键 (worker_id, request_id)，重复请求返回原响应。
type Request struct {
	WorkerID     uint      `gorm:"primaryKey;autoIncrement:false" json:"worker_id"`
	RequestID    string    `gorm:"type:varchar(64);primaryKey" json:"request_id"`
	ResponseJSON string    `gorm:"column:response_json;type:text;not null" json:"response_json"`
	CreatedAt    time.Time `gorm:"index" json:"created_at"`
}

func (Request) TableName() string { return "worker_requests" }
