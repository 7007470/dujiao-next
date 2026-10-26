// Package gormstore 是代练模块的 GORM 持久化实现。
package gormstore

import (
	"errors"

	boostdomain "github.com/dujiao-next/internal/modules/boost/domain"

	"gorm.io/gorm"
)

// partialUniqueIndexes 是 GORM 标签表达不了的部分唯一索引，SQLite 和 PostgreSQL 都支持。
// 它们把 AGENTS.md §3 的几条不变量落到数据库层，应用层的检查只是提前报错。
var partialUniqueIndexes = []string{
	// 同一用户未失败、未取消、未删除的绑定中，同一游戏登录名只能有一个。
	"CREATE UNIQUE INDEX IF NOT EXISTS idx_boost_user_bindings_active_login ON boost_user_bindings(user_id, login_fingerprint) " +
		"WHERE deleted_at IS NULL AND status NOT IN ('FAIL', 'CANCELLED')",
	// 同一绑定账号同时只能有一个执行中的任务。
	"CREATE UNIQUE INDEX IF NOT EXISTS idx_boost_tasks_processing_binding ON boost_tasks(binding_id) " +
		"WHERE status = 'PROCESSING'",
	// 同一目标同时只能有一个未消费的用户输入。
	"CREATE UNIQUE INDEX IF NOT EXISTS idx_boost_inputs_open_target ON boost_inputs(target, target_id) " +
		"WHERE status IN ('waiting', 'submitted')",
}

// Migrate 创建或补齐代练模块的表和索引，可重复执行。只建 boost_ 前缀的表，不改 Dujiao 原表。
func Migrate(db *gorm.DB) error {
	if db == nil {
		return errors.New("boost migration database is nil")
	}
	if err := db.AutoMigrate(
		&boostdomain.Game{},
		&boostdomain.GameServer{},
		&boostdomain.Feature{},
		&boostdomain.FeatureVersion{},
		&boostdomain.PointPack{},
		&boostdomain.PointAccount{},
		&boostdomain.PointLot{},
		&boostdomain.PointLedgerEntry{},
		&boostdomain.UserBinding{},
		&boostdomain.Input{},
		&boostdomain.Task{},
		&boostdomain.TaskEvent{},
	); err != nil {
		return err
	}
	for _, statement := range partialUniqueIndexes {
		if err := db.Exec(statement).Error; err != nil {
			return err
		}
	}
	return nil
}
