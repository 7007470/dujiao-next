package gormstore_test

import (
	"fmt"
	"strings"
	"testing"
	"time"

	workerdomain "github.com/dujiao-next/internal/modules/workerapi/domain"
	"github.com/dujiao-next/internal/modules/workerapi/infrastructure/gormstore"

	"github.com/glebarez/sqlite"
	"gorm.io/gorm"
)

func newWorkerMigrateTestDB(t *testing.T) *gorm.DB {
	t.Helper()
	dsn := fmt.Sprintf("file:worker_migrate_%d?mode=memory&cache=shared", time.Now().UnixNano())
	db, err := gorm.Open(sqlite.Open(dsn), &gorm.Config{})
	if err != nil {
		t.Fatalf("open sqlite: %v", err)
	}
	if err := gormstore.Migrate(db); err != nil {
		t.Fatalf("first migrate: %v", err)
	}
	if err := gormstore.Migrate(db); err != nil {
		t.Fatalf("second migrate: %v", err)
	}
	return db
}

func TestWorkerMigrateOnlyCreatesWorkerTables(t *testing.T) {
	db := newWorkerMigrateTestDB(t)
	var tables []string
	if err := db.Raw("SELECT name FROM sqlite_master WHERE type = 'table' AND name NOT LIKE 'sqlite_%' ORDER BY name").
		Scan(&tables).Error; err != nil {
		t.Fatalf("list tables: %v", err)
	}
	want := []string{"worker_leases", "worker_nodes", "worker_requests"}
	if strings.Join(tables, ",") != strings.Join(want, ",") {
		t.Fatalf("tables = %v, want exactly %v", tables, want)
	}
	if !db.Migrator().HasIndex(&workerdomain.Lease{}, "idx_worker_leases_active_target") {
		t.Fatal("missing idx_worker_leases_active_target")
	}
}

func TestWorkerMigrateEnforcesSingleActiveLeaseAndRequestDedup(t *testing.T) {
	db := newWorkerMigrateTestDB(t)
	expires := time.Now().Add(90 * time.Second)
	lease := func(token string) *workerdomain.Lease {
		return &workerdomain.Lease{Token: token, Kind: workerdomain.LeaseKindTask, TargetID: 1, WorkerID: 1, Active: true, ExpiresAt: expires}
	}

	if err := db.Create(lease("t1")).Error; err != nil {
		t.Fatalf("first lease: %v", err)
	}
	if err := db.Create(lease("t2")).Error; err == nil {
		t.Fatal("second active lease on the same task succeeded, want rejection")
	}
	if err := db.Model(&workerdomain.Lease{}).Where("token = ?", "t1").Update("active", false).Error; err != nil {
		t.Fatalf("close lease: %v", err)
	}
	if err := db.Create(lease("t2")).Error; err != nil {
		t.Fatalf("new lease after closing the old one: %v", err)
	}

	if err := db.Create(&workerdomain.Node{Name: "w1", APIKey: "k1", SecretCipher: "c", CapabilitiesJSON: "[]"}).Error; err != nil {
		t.Fatalf("create node: %v", err)
	}
	if err := db.Create(&workerdomain.Node{Name: "w2", APIKey: "k1", SecretCipher: "c", CapabilitiesJSON: "[]"}).Error; err == nil {
		t.Fatal("duplicate api_key succeeded, want rejection")
	}

	request := &workerdomain.Request{WorkerID: 1, RequestID: "r1", ResponseJSON: "{}"}
	if err := db.Create(request).Error; err != nil {
		t.Fatalf("create request: %v", err)
	}
	if err := db.Create(&workerdomain.Request{WorkerID: 1, RequestID: "r1", ResponseJSON: "{}"}).Error; err == nil {
		t.Fatal("duplicate (worker_id, request_id) succeeded, want rejection")
	}
	if err := db.Create(&workerdomain.Request{WorkerID: 2, RequestID: "r1", ResponseJSON: "{}"}).Error; err != nil {
		t.Fatalf("same request_id from another worker: %v", err)
	}
}
