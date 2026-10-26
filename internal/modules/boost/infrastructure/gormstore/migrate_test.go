package gormstore_test

import (
	"fmt"
	"strings"
	"testing"
	"time"

	boostdomain "github.com/dujiao-next/internal/modules/boost/domain"
	"github.com/dujiao-next/internal/modules/boost/infrastructure/gormstore"

	"github.com/glebarez/sqlite"
	"gorm.io/gorm"
)

func newBoostMigrateTestDB(t *testing.T) *gorm.DB {
	t.Helper()
	dsn := fmt.Sprintf("file:boost_migrate_%d?mode=memory&cache=shared", time.Now().UnixNano())
	db, err := gorm.Open(sqlite.Open(dsn), &gorm.Config{})
	if err != nil {
		t.Fatalf("open sqlite: %v", err)
	}
	if err := gormstore.Migrate(db); err != nil {
		t.Fatalf("first migrate: %v", err)
	}
	return db
}

func mustCreate(t *testing.T, db *gorm.DB, value interface{}) {
	t.Helper()
	if err := db.Create(value).Error; err != nil {
		t.Fatalf("create %T: %v", value, err)
	}
}

func mustReject(t *testing.T, db *gorm.DB, value interface{}, why string) {
	t.Helper()
	if err := db.Create(value).Error; err == nil {
		t.Fatalf("create %T succeeded, want rejection: %s", value, why)
	}
}

func TestMigrateIsRepeatableAndOnlyCreatesBoostTables(t *testing.T) {
	db := newBoostMigrateTestDB(t)
	if err := gormstore.Migrate(db); err != nil {
		t.Fatalf("second migrate: %v", err)
	}

	var tables []string
	if err := db.Raw("SELECT name FROM sqlite_master WHERE type = 'table' AND name NOT LIKE 'sqlite_%' ORDER BY name").
		Scan(&tables).Error; err != nil {
		t.Fatalf("list tables: %v", err)
	}
	want := []string{
		"boost_feature_versions", "boost_features", "boost_game_servers", "boost_games",
		"boost_inputs", "boost_point_accounts", "boost_point_ledger_entries", "boost_point_lots",
		"boost_point_packs", "boost_task_events", "boost_tasks", "boost_user_bindings",
	}
	if strings.Join(tables, ",") != strings.Join(want, ",") {
		t.Fatalf("tables = %v, want exactly %v", tables, want)
	}

	for _, index := range []struct {
		model interface{}
		name  string
	}{
		{&boostdomain.UserBinding{}, "idx_boost_user_bindings_active_login"},
		{&boostdomain.Task{}, "idx_boost_tasks_processing_binding"},
		{&boostdomain.Input{}, "idx_boost_inputs_open_target"},
		{&boostdomain.Task{}, "idx_boost_tasks_user_idempotency"},
		{&boostdomain.Task{}, "idx_boost_tasks_pull"},
		{&boostdomain.PointLot{}, "idx_boost_point_lots_source"},
		{&boostdomain.PointLot{}, "idx_boost_point_lots_fifo"},
		{&boostdomain.TaskEvent{}, "idx_boost_task_events_task_seq"},
	} {
		if !db.Migrator().HasIndex(index.model, index.name) {
			t.Errorf("missing index %s", index.name)
		}
	}
}

func TestMigrateRejectsDuplicateCodes(t *testing.T) {
	db := newBoostMigrateTestDB(t)
	mustCreate(t, db, &boostdomain.Game{Code: "starfall", Name: "星落"})
	mustReject(t, db, &boostdomain.Game{Code: "starfall", Name: "重复"}, "game code is unique")

	mustCreate(t, db, &boostdomain.GameServer{GameID: 1, Code: "s1", Name: "一区"})
	mustReject(t, db, &boostdomain.GameServer{GameID: 1, Code: "s1", Name: "重复"}, "server code is unique per game")
	mustCreate(t, db, &boostdomain.GameServer{GameID: 2, Code: "s1", Name: "另一个游戏的一区"})

	mustCreate(t, db, &boostdomain.Feature{GameID: 1, Code: "main_stage", DefinitionJSON: "{}"})
	mustReject(t, db, &boostdomain.Feature{GameID: 1, Code: "main_stage", DefinitionJSON: "{}"}, "feature code is unique per game")
	mustCreate(t, db, &boostdomain.Feature{GameID: 2, Code: "main_stage", DefinitionJSON: "{}"})

	mustCreate(t, db, &boostdomain.FeatureVersion{FeatureID: 1, Version: 1, DefinitionJSON: "{}"})
	mustReject(t, db, &boostdomain.FeatureVersion{FeatureID: 1, Version: 1, DefinitionJSON: "{}"}, "(feature_id, version) is the primary key")
}

func TestMigrateEnforcesPointIdempotency(t *testing.T) {
	db := newBoostMigrateTestDB(t)
	mustCreate(t, db, &boostdomain.PointAccount{UserID: 7})
	mustReject(t, db, &boostdomain.PointAccount{UserID: 7}, "one account per user")

	mustCreate(t, db, &boostdomain.PointLot{UserID: 7, SourceType: boostdomain.LotSourceOrder, SourceRef: "order_item:1", Amount: 100, Remaining: 100})
	mustReject(t, db, &boostdomain.PointLot{UserID: 7, SourceType: boostdomain.LotSourceOrder, SourceRef: "order_item:1", Amount: 100, Remaining: 100}, "order credited once")

	mustCreate(t, db, &boostdomain.PointLedgerEntry{UserID: 7, Type: boostdomain.LedgerPurchase, BizKey: "order:order_item:1"})
	mustReject(t, db, &boostdomain.PointLedgerEntry{UserID: 7, Type: boostdomain.LedgerPurchase, BizKey: "order:order_item:1"}, "biz_key is unique")

	mustCreate(t, db, &boostdomain.PointPack{ProductID: 3, SKUID: 4, Name: "100 积分", Points: 100})
	mustReject(t, db, &boostdomain.PointPack{ProductID: 3, SKUID: 4, Name: "重复", Points: 100}, "one pack per sku")
}

func TestMigrateEnforcesBindingAndTaskInvariants(t *testing.T) {
	db := newBoostMigrateTestDB(t)
	now := time.Now()
	binding := func(status string) *boostdomain.UserBinding {
		return &boostdomain.UserBinding{
			UserID: 7, GameID: 1, ServerID: 1, LoginMasked: "a***@x.com",
			LoginFingerprint: "fp-1", Status: status, AvailableAt: now,
		}
	}

	failed := binding(boostdomain.BindingStatusFail)
	mustCreate(t, db, failed)
	mustCreate(t, db, binding(boostdomain.BindingStatusCancelled))
	active := binding(boostdomain.BindingStatusSuccess)
	mustCreate(t, db, active)
	mustReject(t, db, binding(boostdomain.BindingStatusPending), "same login cannot be bound twice while active")

	task := func(no, key, status string) *boostdomain.Task {
		return &boostdomain.Task{
			TaskNo: no, UserID: 7, BindingID: active.ID, GameID: 1, FeatureID: 1, FeatureVersion: 1,
			SnapshotJSON: "{}", ParamsJSON: "{}", PlanJSON: "{}", Status: status, AvailableAt: now, IdempotencyKey: key,
		}
	}
	mustCreate(t, db, task("T1", "k1", boostdomain.TaskStatusProcessing))
	mustReject(t, db, task("T2", "k2", boostdomain.TaskStatusProcessing), "one PROCESSING task per binding")
	mustCreate(t, db, task("T3", "k3", boostdomain.TaskStatusPending))
	mustReject(t, db, task("T4", "k3", boostdomain.TaskStatusPending), "idempotency key is unique per user")
	mustReject(t, db, task("T3", "k5", boostdomain.TaskStatusPending), "task_no is unique")

	mustCreate(t, db, &boostdomain.TaskEvent{TaskID: 1, Seq: 1, Type: boostdomain.EventTypeLog})
	mustReject(t, db, &boostdomain.TaskEvent{TaskID: 1, Seq: 1, Type: boostdomain.EventTypeLog}, "(task_id, seq) dedupes worker events")

	input := func(status string) *boostdomain.Input {
		return &boostdomain.Input{
			Target: boostdomain.InputTargetTask, TargetID: 1, Kind: boostdomain.InputKindSMSCode,
			Status: status, ExpiresAt: now.Add(5 * time.Minute),
		}
	}
	mustCreate(t, db, input(boostdomain.InputStatusConsumed))
	mustCreate(t, db, input(boostdomain.InputStatusWaiting))
	mustReject(t, db, input(boostdomain.InputStatusSubmitted), "one open input per target")
}
