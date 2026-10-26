package domain

import "time"

// UserBinding 用户绑定的游戏账号。凭据只以密文保存（AES-256-GCM，KeyVersion 记录密钥版本），
// 解绑后 CredentialCipher / SessionRefCipher 立即置空。
// 同一用户未失败、未取消、未删除的绑定中 LoginFingerprint 唯一（部分唯一索引，见 gormstore）。
type UserBinding struct {
	ID               uint       `gorm:"primarykey" json:"id"`
	UserID           uint       `gorm:"not null;index" json:"user_id"`
	GameID           uint       `gorm:"not null;index" json:"game_id"`
	ServerID         uint       `gorm:"not null" json:"server_id"`
	DisplayName      string     `gorm:"type:varchar(64)" json:"display_name"`
	LoginMasked      string     `gorm:"type:varchar(64);not null" json:"login_masked"`
	LoginFingerprint string     `gorm:"type:varchar(64);not null" json:"-"`
	CredentialCipher *string    `gorm:"type:text" json:"-"`
	KeyVersion       int        `gorm:"not null" json:"-"`
	Status           string     `gorm:"type:varchar(16);not null;default:'PENDING';index" json:"status"`
	Attempt          int        `gorm:"not null" json:"attempt"`
	MaxAttempts      int        `gorm:"not null" json:"max_attempts"`
	AvailableAt      time.Time  `gorm:"not null;index" json:"available_at"`
	LastErrorCode    string     `gorm:"type:varchar(32)" json:"last_error_code"`
	UserMessage      string     `gorm:"type:varchar(255)" json:"user_message"`
	SessionRefCipher *string    `gorm:"type:text" json:"-"`
	WorkerID         *uint      `gorm:"index" json:"worker_id,omitempty"`
	LeaseToken       *string    `gorm:"type:varchar(64)" json:"-"`
	LeaseExpiresAt   *time.Time `gorm:"index" json:"lease_expires_at,omitempty"`
	VerifiedAt       *time.Time `json:"verified_at,omitempty"`
	DeletedAt        *time.Time `gorm:"index" json:"-"`
	CreatedAt        time.Time  `json:"created_at"`
	UpdatedAt        time.Time  `json:"updated_at"`
}

func (UserBinding) TableName() string { return "boost_user_bindings" }

// Input WAIT_INPUT / WAIT_USER 时向用户索取的输入。ValueCipher 下发给 Worker 后立即清空。
// 同一目标同时只有一个未消费的输入（部分唯一索引，见 gormstore）。
type Input struct {
	ID          uint      `gorm:"primarykey" json:"id"`
	Target      string    `gorm:"type:varchar(16);not null;index:idx_boost_inputs_target,priority:1" json:"target"`
	TargetID    uint      `gorm:"not null;index:idx_boost_inputs_target,priority:2" json:"target_id"`
	Kind        string    `gorm:"type:varchar(16);not null" json:"kind"`
	Prompt      string    `gorm:"type:varchar(255)" json:"prompt"`
	ValueCipher *string   `gorm:"type:text" json:"-"`
	Status      string    `gorm:"type:varchar(16);not null;default:'waiting';index" json:"status"`
	ExpiresAt   time.Time `gorm:"not null;index" json:"expires_at"`
	CreatedAt   time.Time `json:"created_at"`
	UpdatedAt   time.Time `json:"updated_at"`
}

func (Input) TableName() string { return "boost_inputs" }
