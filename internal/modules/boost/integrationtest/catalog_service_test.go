package integrationtest

import (
	"errors"
	"fmt"
	"os"
	"testing"
	"time"

	boostapp "github.com/dujiao-next/internal/modules/boost/application"
	boostcontract "github.com/dujiao-next/internal/modules/boost/contract"
	boostdomain "github.com/dujiao-next/internal/modules/boost/domain"
	"github.com/dujiao-next/internal/modules/boost/infrastructure/gormstore"

	"github.com/glebarez/sqlite"
	"gorm.io/gorm"
)

func newCatalog(t *testing.T) (*boostapp.CatalogService, *gorm.DB) {
	t.Helper()
	dsn := fmt.Sprintf("file:boost_catalog_%d?mode=memory&cache=shared", time.Now().UnixNano())
	db, err := gorm.Open(sqlite.Open(dsn), &gorm.Config{})
	if err != nil {
		t.Fatalf("open sqlite: %v", err)
	}
	if err := gormstore.Migrate(db); err != nil {
		t.Fatalf("migrate: %v", err)
	}
	return boostapp.NewCatalogService(gormstore.NewCatalogStore(db)), db
}

func ptr[T any](v T) *T { return &v }

func wantErr(t *testing.T, err, kind error) {
	t.Helper()
	if !errors.Is(err, kind) {
		t.Fatalf("err = %v, want %v", err, kind)
	}
}

// seedDefinition 读取原型示例定义（golden 里的 seed 用例），保证用的是规范里的真实定义。
func mainStageDefinition(t *testing.T) []byte {
	t.Helper()
	data, err := os.ReadFile("testdata/main_stage.json")
	if err != nil {
		t.Fatalf("read definition: %v", err)
	}
	return data
}

func TestGameCodeIsUniqueAndImmutable(t *testing.T) {
	svc, _ := newCatalog(t)
	game, err := svc.SaveGame(boostapp.GameInput{Code: "starfall", Name: "星落幻境"})
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	if game.Status != boostdomain.GameStatusDraft || game.Version != 1 || game.ShortName != "星落" || game.Color == "" {
		t.Fatalf("defaults wrong: %+v", game)
	}
	_, err = svc.SaveGame(boostapp.GameInput{Code: "starfall", Name: "重复"})
	wantErr(t, err, boostcontract.ErrDuplicate)

	_, err = svc.SaveGame(boostapp.GameInput{ID: game.ID, Code: "renamed", Name: "星落幻境"})
	wantErr(t, err, boostcontract.ErrCodeImmutable)

	for _, bad := range []boostapp.GameInput{{Code: "Bad-Code", Name: "x"}, {Code: "ok_code", Name: ""}, {Code: "x", Name: "x"}} {
		_, err := svc.SaveGame(bad)
		wantErr(t, err, boostcontract.ErrInvalid)
	}
}

func TestGameVersionConflict(t *testing.T) {
	svc, _ := newCatalog(t)
	game, _ := svc.SaveGame(boostapp.GameInput{Code: "starfall", Name: "星落幻境"})
	updated, err := svc.SaveGame(boostapp.GameInput{ID: game.ID, Version: ptr(1), Code: "starfall", Name: "星落幻境 2"})
	if err != nil || updated.Version != 2 {
		t.Fatalf("update = %+v, %v", updated, err)
	}
	_, err = svc.SaveGame(boostapp.GameInput{ID: game.ID, Version: ptr(1), Code: "starfall", Name: "过期的编辑"})
	wantErr(t, err, boostcontract.ErrVersionConflict)
	reloaded, _ := svc.GetGame(game.ID)
	if reloaded.Name != "星落幻境 2" {
		t.Fatalf("stale write changed the game: %+v", reloaded)
	}
}

func TestGameCannotGoLiveWithoutActiveServerAndFeature(t *testing.T) {
	svc, _ := newCatalog(t)
	game, _ := svc.SaveGame(boostapp.GameInput{Code: "starfall", Name: "星落幻境"})

	_, err := svc.SetGameStatus(game.ID, boostdomain.GameStatusActive)
	wantErr(t, err, boostcontract.ErrGameNotReady)

	server, err := svc.SaveServer(boostapp.ServerInput{GameID: game.ID, Code: "cn-sky", Name: "国服 · 天空岛"})
	if err != nil {
		t.Fatalf("server: %v", err)
	}
	_, err = svc.SetGameStatus(game.ID, boostdomain.GameStatusActive)
	wantErr(t, err, boostcontract.ErrGameNotReady) // 还没有上架的功能

	saved, err := svc.SaveFeature(boostapp.FeatureInput{GameID: game.ID, Definition: mainStageDefinition(t)})
	if err != nil {
		t.Fatalf("feature: %v", err)
	}
	_, err = svc.SetGameStatus(game.ID, boostdomain.GameStatusActive)
	wantErr(t, err, boostcontract.ErrGameNotReady) // 功能还是草稿

	if _, err := svc.SetFeatureStatus(saved.Feature.ID, boostdomain.FeatureStatusActive); err != nil {
		t.Fatalf("publish feature: %v", err)
	}
	live, err := svc.SetGameStatus(game.ID, boostdomain.GameStatusActive)
	if err != nil || live.Status != boostdomain.GameStatusActive {
		t.Fatalf("go live = %+v, %v", live, err)
	}

	// 上线后不能下架唯一的区服 / 功能
	_, err = svc.SetServerStatus(server.ID, boostdomain.ServerStatusDisabled)
	wantErr(t, err, boostcontract.ErrGameNotReady)
	_, err = svc.SetFeatureStatus(saved.Feature.ID, boostdomain.FeatureStatusDisabled)
	wantErr(t, err, boostcontract.ErrGameNotReady)

	// 隐藏游戏后就可以
	if _, err := svc.SetGameStatus(game.ID, boostdomain.GameStatusHidden); err != nil {
		t.Fatalf("hide: %v", err)
	}
	if _, err := svc.SetServerStatus(server.ID, boostdomain.ServerStatusDisabled); err != nil {
		t.Fatalf("disable server after hiding: %v", err)
	}
}

func TestServerCodeUniquePerGame(t *testing.T) {
	svc, _ := newCatalog(t)
	a, _ := svc.SaveGame(boostapp.GameInput{Code: "starfall", Name: "星落幻境"})
	b, _ := svc.SaveGame(boostapp.GameInput{Code: "ironcrest", Name: "铁冠征途"})
	s1, err := svc.SaveServer(boostapp.ServerInput{GameID: a.ID, Code: "asia", Name: "亚服", Platform: "weird"})
	if err != nil || s1.Platform != boostdomain.PlatformAll {
		t.Fatalf("server = %+v, %v", s1, err)
	}
	_, err = svc.SaveServer(boostapp.ServerInput{GameID: a.ID, Code: "asia", Name: "重复"})
	wantErr(t, err, boostcontract.ErrDuplicate)
	if _, err := svc.SaveServer(boostapp.ServerInput{GameID: b.ID, Code: "asia", Name: "另一个游戏"}); err != nil {
		t.Fatalf("same code in another game: %v", err)
	}
	s2, _ := svc.SaveServer(boostapp.ServerInput{GameID: a.ID, Code: "cn", Name: "国服"})
	_, err = svc.SaveServer(boostapp.ServerInput{ID: s2.ID, GameID: a.ID, Code: "asia", Name: "改名撞车"})
	wantErr(t, err, boostcontract.ErrDuplicate)
	_, err = svc.SaveServer(boostapp.ServerInput{ID: s2.ID, GameID: b.ID, Code: "cn", Name: "换游戏"})
	wantErr(t, err, boostcontract.ErrNotFound)
}

func TestFeatureVersioning(t *testing.T) {
	svc, db := newCatalog(t)
	game, _ := svc.SaveGame(boostapp.GameInput{Code: "starfall", Name: "星落幻境"})
	def := mainStageDefinition(t)

	created, err := svc.SaveFeature(boostapp.FeatureInput{GameID: game.ID, Definition: def, AdminID: ptr(uint(1))})
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	f := created.Feature
	if !created.Changed || f.Version != 1 || f.Code != "main_stage" || f.Status != boostdomain.FeatureStatusDraft {
		t.Fatalf("created = %+v", created)
	}

	// 内容不变（哪怕空白和键顺序不同）不升版本
	same, err := svc.SaveFeature(boostapp.FeatureInput{ID: f.ID, GameID: game.ID, Version: ptr(1), Definition: append([]byte("  \n"), def...)})
	if err != nil || same.Changed || same.Feature.Version != 1 {
		t.Fatalf("unchanged save = %+v, %v", same, err)
	}

	changed := []byte(`{"code":"main_stage","name":"主线关卡（改）","base_points":2,"fields":[{"key":"a","type":"switch","label":"A","points":1}]}`)
	v2, err := svc.SaveFeature(boostapp.FeatureInput{ID: f.ID, GameID: game.ID, Version: ptr(1), Definition: changed})
	if err != nil || !v2.Changed || v2.Feature.Version != 2 {
		t.Fatalf("v2 = %+v, %v", v2, err)
	}
	_, err = svc.SaveFeature(boostapp.FeatureInput{ID: f.ID, GameID: game.ID, Version: ptr(1), Definition: def})
	wantErr(t, err, boostcontract.ErrVersionConflict)

	versions, err := svc.ListFeatureVersions(f.ID)
	if err != nil || len(versions) != 2 || versions[0].Version != 2 || versions[1].Version != 1 {
		t.Fatalf("versions = %+v, %v", versions, err)
	}
	v1, err := svc.GetFeatureVersion(f.ID, 1)
	if err != nil || v1.DefinitionJSON == v2.Feature.DefinitionJSON {
		t.Fatalf("v1 = %+v, %v", v1, err)
	}

	// code 不可修改；不能移到别的游戏；同游戏 code 不能重复
	_, err = svc.SaveFeature(boostapp.FeatureInput{ID: f.ID, GameID: game.ID, Definition: []byte(`{"code":"other","name":"x","fields":[{"key":"a","type":"switch","label":"A","points":1}]}`)})
	wantErr(t, err, boostcontract.ErrCodeImmutable)
	other, _ := svc.SaveGame(boostapp.GameInput{Code: "ironcrest", Name: "铁冠征途"})
	_, err = svc.SaveFeature(boostapp.FeatureInput{ID: f.ID, GameID: other.ID, Definition: changed})
	wantErr(t, err, boostcontract.ErrFeatureWrongGame)
	_, err = svc.SaveFeature(boostapp.FeatureInput{GameID: game.ID, Definition: changed})
	wantErr(t, err, boostcontract.ErrDuplicate)

	// 非法定义不保存，也不留半成品
	var before int64
	db.Model(&boostdomain.FeatureVersion{}).Count(&before)
	_, err = svc.SaveFeature(boostapp.FeatureInput{GameID: game.ID, Definition: []byte(`{"code":"bad","name":"x","fields":[]}`)})
	wantErr(t, err, boostcontract.ErrDefinition)
	ve, ok := boostapp.AsValidationError(err)
	if !ok || len(ve.Issues) == 0 || ve.Issues[0].Path != "fields" {
		t.Fatalf("validation error = %+v", ve)
	}
	_, err = svc.SaveFeature(boostapp.FeatureInput{GameID: game.ID, Definition: []byte(`{not json`)})
	wantErr(t, err, boostcontract.ErrDefinition)
	var after, features int64
	db.Model(&boostdomain.FeatureVersion{}).Count(&after)
	db.Model(&boostdomain.Feature{}).Count(&features)
	if after != before || features != 1 {
		t.Fatalf("invalid definitions left rows: versions %d→%d, features %d", before, after, features)
	}
}

func TestFeatureOrderingAndPreview(t *testing.T) {
	svc, _ := newCatalog(t)
	game, _ := svc.SaveGame(boostapp.GameInput{Code: "starfall", Name: "星落幻境"})
	mk := func(code string) uint {
		r, err := svc.SaveFeature(boostapp.FeatureInput{GameID: game.ID, Definition: []byte(`{"code":"` + code + `","name":"` + code + `","base_points":5,"fields":[{"key":"a","type":"switch","label":"A"}]}`)})
		if err != nil {
			t.Fatalf("create %s: %v", code, err)
		}
		return r.Feature.ID
	}
	a, b, c := mk("aa"), mk("bb"), mk("cc")
	list, err := svc.MoveFeature(c, -1)
	if err != nil {
		t.Fatal(err)
	}
	order := func(list []boostdomain.Feature) string {
		out := ""
		for _, f := range list {
			out += f.Code + " "
		}
		return out
	}
	if got := order(list); got != "aa cc bb " {
		t.Fatalf("order after move = %q", got)
	}
	if _, err := svc.MoveFeature(a, -1); err != nil { // 已经在最前，不变
		t.Fatal(err)
	}
	reloaded, _ := svc.ListFeatures(game.ID)
	if got := order(reloaded); got != "aa cc bb " {
		t.Fatalf("persisted order = %q", got)
	}
	_ = b

	plan, err := svc.Preview(boostapp.PreviewInput{Definition: mainStageDefinition(t), Params: []byte(`{"mode":"sweep","stages":[{"item":"1-1","count":10},{"item":"2-4","count":3}]}`), DiscountRate: 90})
	if err != nil || !plan.OK || plan.RawTotal != 35 || plan.Total != 31 {
		t.Fatalf("preview = %+v, %v", plan, err)
	}
	_, err = svc.Preview(boostapp.PreviewInput{Definition: []byte(`{}`)})
	wantErr(t, err, boostcontract.ErrDefinition)
}
