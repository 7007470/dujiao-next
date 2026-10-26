// Package gormstore 是 Worker 接口的 GORM 持久化实现。
package gormstore

import (
	"errors"

	workerdomain "github.com/dujiao-next/internal/modules/workerapi/domain"

	"gorm.io/gorm"
)

// 同一目标同时只能有一个有效租约；旧租约关闭（active = false）后才能发新租约。
const activeLeaseIndex = "CREATE UNIQUE INDEX IF NOT EXISTS idx_worker_leases_active_target ON worker_leases(kind, target_id) WHERE active"

// Migrate 创建或补齐 Worker 接口的表和索引，可重复执行。只建 worker_ 前缀的表。
func Migrate(db *gorm.DB) error {
	if db == nil {
		return errors.New("worker migration database is nil")
	}
	if err := db.AutoMigrate(
		&workerdomain.Node{},
		&workerdomain.Lease{},
		&workerdomain.Request{},
	); err != nil {
		return err
	}
	return db.Exec(activeLeaseIndex).Error
}
