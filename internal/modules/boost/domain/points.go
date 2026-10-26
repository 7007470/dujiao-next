package domain

import (
	"time"

	"github.com/dujiao-next/internal/shared/money"
)

// PointPack 积分包，对应一个 Dujiao 商品 SKU。售价以商品 SKU 为准，这里不重复保存。
type PointPack struct {
	ID        uint      `gorm:"primarykey" json:"id"`
	ProductID uint      `gorm:"not null;uniqueIndex:idx_boost_point_packs_sku,priority:1" json:"product_id"`
	SKUID     uint      `gorm:"column:sku_id;not null;uniqueIndex:idx_boost_point_packs_sku,priority:2" json:"sku_id"`
	Name      string    `gorm:"type:varchar(64);not null" json:"name"`
	Points    int64     `gorm:"not null" json:"points"`
	Bonus     int64     `gorm:"not null" json:"bonus"`
	ValidDays *int      `json:"valid_days,omitempty"`
	Active    bool      `gorm:"not null;index" json:"active"`
	CreatedAt time.Time `json:"created_at"`
	UpdatedAt time.Time `json:"updated_at"`
}

func (PointPack) TableName() string { return "boost_point_packs" }

// PointAccount 用户积分账户。不变量：Available = 有效批次 Remaining 之和 = 流水合计；
// Frozen = 进行中任务冻结之和；两者都不为负（由应用层条件更新保证）。
type PointAccount struct {
	UserID    uint      `gorm:"primaryKey;autoIncrement:false" json:"user_id"`
	Available int64     `gorm:"not null" json:"available"`
	Frozen    int64     `gorm:"not null" json:"frozen"`
	Version   int64     `gorm:"not null" json:"version"`
	CreatedAt time.Time `json:"created_at"`
	UpdatedAt time.Time `json:"updated_at"`
}

func (PointAccount) TableName() string { return "boost_point_accounts" }

// PointLot 积分批次。消耗按 ExpiresAt 先进先出；(source_type, source_ref) 唯一，保证同一来源只入账一次。
type PointLot struct {
	ID            uint          `gorm:"primarykey" json:"id"`
	UserID        uint          `gorm:"not null;index:idx_boost_point_lots_fifo,priority:1" json:"user_id"`
	SourceType    string        `gorm:"type:varchar(16);not null;uniqueIndex:idx_boost_point_lots_source,priority:1" json:"source_type"`
	SourceRef     string        `gorm:"type:varchar(64);not null;uniqueIndex:idx_boost_point_lots_source,priority:2" json:"source_ref"`
	Amount        int64         `gorm:"not null" json:"amount"`
	Remaining     int64         `gorm:"not null" json:"remaining"`
	Status        string        `gorm:"type:varchar(16);not null;default:'active';index:idx_boost_point_lots_fifo,priority:2" json:"status"`
	ExpiresAt     *time.Time    `gorm:"index:idx_boost_point_lots_fifo,priority:3" json:"expires_at,omitempty"`
	SuggestRefund *money.Amount `gorm:"type:decimal(20,2)" json:"suggest_refund,omitempty"`
	CreatedAt     time.Time     `json:"created_at"`
	UpdatedAt     time.Time     `json:"updated_at"`
}

func (PointLot) TableName() string { return "boost_point_lots" }

// PointLedgerEntry 积分流水。BizKey 全局唯一，重复请求返回原结果。
// LotAllocationsJSON 记录本笔变动涉及的批次和数量，用于按原批次退回。
type PointLedgerEntry struct {
	ID                 uint      `gorm:"primarykey" json:"id"`
	UserID             uint      `gorm:"not null;index" json:"user_id"`
	Type               string    `gorm:"type:varchar(16);not null;index" json:"type"`
	DeltaAvailable     int64     `gorm:"not null" json:"delta_available"`
	DeltaFrozen        int64     `gorm:"not null" json:"delta_frozen"`
	AvailableAfter     int64     `gorm:"not null" json:"available_after"`
	FrozenAfter        int64     `gorm:"not null" json:"frozen_after"`
	LotAllocationsJSON string    `gorm:"column:lot_allocations_json;type:text" json:"lot_allocations_json"`
	BizKey             string    `gorm:"type:varchar(128);not null;uniqueIndex" json:"biz_key"`
	TaskID             *uint     `gorm:"index" json:"task_id,omitempty"`
	OrderID            *uint     `gorm:"index" json:"order_id,omitempty"`
	OperatorAdminID    *uint     `gorm:"index" json:"operator_admin_id,omitempty"`
	Remark             string    `gorm:"type:varchar(255)" json:"remark"`
	CreatedAt          time.Time `gorm:"index" json:"created_at"`
}

func (PointLedgerEntry) TableName() string { return "boost_point_ledger_entries" }
