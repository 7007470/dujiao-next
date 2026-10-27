package integrationtest

import (
	"encoding/json"
	"fmt"
	"testing"

	userdomain "github.com/dujiao-next/internal/modules/identity/user/domain"
	memberleveldomain "github.com/dujiao-next/internal/modules/memberlevel/domain"

	boostapp "github.com/dujiao-next/internal/modules/boost/application"
	boostcontract "github.com/dujiao-next/internal/modules/boost/contract"
	boostdomain "github.com/dujiao-next/internal/modules/boost/domain"
	"github.com/dujiao-next/internal/modules/boost/infrastructure/gormstore"
	"github.com/dujiao-next/internal/modules/boost/infrastructure/memberrate"
	boosthttp "github.com/dujiao-next/internal/modules/boost/transport/http"
	"github.com/dujiao-next/internal/shared/money"

	"github.com/gin-gonic/gin"
	"github.com/shopspring/decimal"
)

type fakeUsers map[uint]*userdomain.User

func (f fakeUsers) GetByID(id uint) (*userdomain.User, error) { return f[id], nil }

type fakeLevels map[uint]*memberleveldomain.MemberLevel

func (f fakeLevels) GetByID(id uint) (*memberleveldomain.MemberLevel, error) { return f[id], nil }

func level(rate float64, active bool) *memberleveldomain.MemberLevel {
	return &memberleveldomain.MemberLevel{DiscountRate: money.FromDecimal(decimal.NewFromFloat(rate)), IsActive: active}
}

func TestMemberRateResolver(t *testing.T) {
	r := memberrate.New(
		fakeUsers{1: {MemberLevelID: 10}, 2: {MemberLevelID: 11}, 3: {MemberLevelID: 12}, 4: {}, 5: {MemberLevelID: 13}, 6: {MemberLevelID: 14}},
		fakeLevels{10: level(90, true), 11: level(90, false), 12: level(100, true), 13: level(85.5, true), 14: level(0, true)},
	)
	for user, want := range map[uint]int{0: 100, 1: 90, 2: 100, 3: 100, 4: 100, 5: 85, 6: 100, 99: 100} {
		if got := r.DiscountRate(user); got != want {
			t.Errorf("DiscountRate(%d) = %d, want %d", user, got, want)
		}
	}
}

type fixedRate int

func (r fixedRate) DiscountRate(uint) int { return int(r) }

// liveCatalog 建一个上线的游戏：一个启用区服 + 一个停用区服，一个上架功能 + 一个草稿功能。
func liveCatalog(t *testing.T) (*boostapp.CatalogService, *boostapp.StorefrontService, uint, uint) {
	t.Helper()
	svc, db := newCatalog(t)
	store := boostapp.NewStorefrontService(gormstore.NewCatalogStore(db), fixedRate(90))
	game, _ := svc.SaveGame(boostapp.GameInput{Code: "starfall", Name: "星落幻境", IconURL: "/uploads/category/a.png"})
	if _, err := svc.SaveServer(boostapp.ServerInput{GameID: game.ID, Code: "cn", Name: "国服"}); err != nil {
		t.Fatal(err)
	}
	off, _ := svc.SaveServer(boostapp.ServerInput{GameID: game.ID, Code: "asia", Name: "亚服"})
	if _, err := svc.SetServerStatus(off.ID, boostdomain.ServerStatusDisabled); err != nil {
		t.Fatal(err)
	}
	live, err := svc.SaveFeature(boostapp.FeatureInput{GameID: game.ID, Definition: mainStageDefinition(t)})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := svc.SaveFeature(boostapp.FeatureInput{GameID: game.ID, Definition: []byte(`{"code":"draft_one","name":"草稿","base_points":5,"fields":[{"key":"a","type":"switch","label":"A"}]}`)}); err != nil {
		t.Fatal(err)
	}
	if _, err := svc.SetFeatureStatus(live.Feature.ID, boostdomain.FeatureStatusActive); err != nil {
		t.Fatal(err)
	}
	if _, err := svc.SetGameStatus(game.ID, boostdomain.GameStatusActive); err != nil {
		t.Fatal(err)
	}
	return svc, store, game.ID, live.Feature.ID
}

func TestStorefrontOnlyShowsLiveContent(t *testing.T) {
	svc, store, gameID, featureID := liveCatalog(t)
	if _, err := svc.SaveGame(boostapp.GameInput{Code: "hidden_game", Name: "未上线"}); err != nil {
		t.Fatal(err)
	}

	games, err := store.ListGames()
	if err != nil || len(games) != 1 || games[0].Code != "starfall" || len(games[0].Servers) != 1 || games[0].FeatureCount != 1 || games[0].IconURL == "" {
		t.Fatalf("games = %+v, %v", games, err)
	}
	detail, err := store.GetGame("starfall")
	if err != nil || len(detail.Features) != 1 || detail.Features[0].ID != featureID || len(detail.Servers) != 1 {
		t.Fatalf("detail = %+v, %v", detail, err)
	}
	_, err = store.GetGame("hidden_game")
	wantErr(t, err, boostcontract.ErrNotFound)

	// 会员折扣只对登录用户生效
	params := []byte(`{"mode":"sweep","stages":[{"item":"1-1","count":10},{"item":"2-4","count":3}]}`)
	anon, err := store.Quote(boostapp.QuoteInput{FeatureID: featureID, Params: params})
	if err != nil || anon.Total != 35 || anon.Rate != 100 {
		t.Fatalf("anonymous quote = %+v, %v", anon, err)
	}
	member, err := store.Quote(boostapp.QuoteInput{FeatureID: featureID, Params: params, UserID: 1})
	if err != nil || member.Total != 31 || member.Rate != 90 {
		t.Fatalf("member quote = %+v, %v", member, err)
	}

	// 下架功能 / 隐藏游戏后不能再报价
	if _, err := svc.SetGameStatus(gameID, boostdomain.GameStatusHidden); err != nil {
		t.Fatal(err)
	}
	_, err = store.Quote(boostapp.QuoteInput{FeatureID: featureID, Params: params})
	wantErr(t, err, boostcontract.ErrNotFound)
	games, _ = store.ListGames()
	if len(games) != 0 {
		t.Fatalf("hidden game still listed: %+v", games)
	}
}

func TestStorefrontHTTP(t *testing.T) {
	_, store, _, featureID := liveCatalog(t)
	gin.SetMode(gin.TestMode)
	r := gin.New()
	boosthttp.RegisterPublicRoutes(r.Group("/api/v1/public"), boosthttp.NewStorefrontHandler(store))
	user := r.Group("/api/v1", func(c *gin.Context) { c.Set("user_id", uint(5)) })
	boosthttp.RegisterUserRoutes(user, boosthttp.NewStorefrontHandler(store))

	var games []map[string]interface{}
	ok(t, call(t, r, "GET", "/api/v1/public/boost/games", nil), &games)
	if len(games) != 1 || games[0]["icon_url"] != "/uploads/category/a.png" {
		t.Fatalf("games = %+v", games)
	}

	var detail struct {
		Game     map[string]interface{} `json:"game"`
		Features []struct {
			ID         uint                   `json:"id"`
			Name       string                 `json:"name"`
			Definition map[string]interface{} `json:"definition"`
			Status     string                 `json:"status"`
		} `json:"features"`
	}
	ok(t, call(t, r, "GET", "/api/v1/public/boost/games/starfall", nil), &detail)
	if detail.Game["code"] != "starfall" || len(detail.Features) != 1 || detail.Features[0].Name != "主线关卡" || detail.Features[0].Definition["catalogs"] == nil {
		t.Fatalf("detail = %+v", detail)
	}
	if missing := call(t, r, "GET", "/api/v1/public/boost/games/nope", nil); missing.StatusCode != 404 {
		t.Fatalf("missing game = %+v", missing)
	}

	body := map[string]interface{}{"feature_id": featureID, "params": json.RawMessage(`{"mode":"sweep","stages":[{"item":"1-1","count":10},{"item":"2-4","count":3}]}`)}
	var plan struct {
		Total int64 `json:"total"`
		Rate  int   `json:"rate"`
	}
	ok(t, call(t, r, "POST", "/api/v1/public/boost/quote", body), &plan)
	if plan.Total != 35 || plan.Rate != 100 {
		t.Fatalf("public quote = %+v", plan)
	}
	ok(t, call(t, r, "POST", "/api/v1/boost/quote", body), &plan)
	if plan.Total != 31 || plan.Rate != 90 {
		t.Fatalf("user quote = %+v", plan)
	}
	if bad := call(t, r, "POST", "/api/v1/public/boost/quote", map[string]interface{}{"feature_id": 9999}); bad.StatusCode != 404 {
		t.Fatalf("unknown feature = %+v", bad)
	}
	if bad := call(t, r, "POST", "/api/v1/public/boost/quote", `{"feature_id":`+fmt.Sprint(featureID)+`,"params":"oops"}`); bad.StatusCode != 0 {
		// 参数不是对象时按空参数编译（与原型一致），返回字段错误而不是 400
		t.Fatalf("non-object params = %+v", bad)
	}
}
