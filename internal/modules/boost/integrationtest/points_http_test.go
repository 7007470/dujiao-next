package integrationtest

import (
	"strings"
	"testing"

	boosthttp "github.com/dujiao-next/internal/modules/boost/transport/http"

	"github.com/gin-gonic/gin"
)

func newPointsRouter(t *testing.T) (*pointsEnv, *gin.Engine, *fakeAudit) {
	t.Helper()
	gin.SetMode(gin.TestMode)
	e := newPointsEnv(t)
	audit := &fakeAudit{}
	h := boosthttp.NewPointsHandler(e.points, audit)
	r := gin.New()
	admin := r.Group("/api/v1/admin", func(c *gin.Context) {
		c.Set("admin_id", uint(7))
		c.Set("username", "finance")
	})
	boosthttp.RegisterPointsAdminRoutes(admin, h)
	user := r.Group("/api/v1", func(c *gin.Context) { c.Set("user_id", e.user.ID) })
	boosthttp.RegisterPointsUserRoutes(user, h)
	boosthttp.RegisterPointsPublicRoutes(r.Group("/api/v1/public"), h)
	return e, r, audit
}

func TestPointsHTTPAdminFlowAndUserViews(t *testing.T) {
	e, r, audit := newPointsRouter(t)
	product, sku, _ := e.newPackProduct("http", 30, 300, 30, days(90))

	// 后台列表带售价（以 SKU 为准）
	var packs []map[string]interface{}
	ok(t, call(t, r, "GET", "/api/v1/admin/boost/point-packs", nil), &packs)
	if len(packs) != 1 || packs[0]["price"] != "30.00" || packs[0]["fulfillment_type"] != "boost_points" {
		t.Fatalf("packs = %+v", packs)
	}
	// 修改积分包
	var updated map[string]interface{}
	ok(t, call(t, r, "PUT", "/api/v1/admin/boost/point-packs/1", map[string]interface{}{"points": 320, "bonus": 30, "valid_days": 0, "active": true}), &updated)
	if updated["points"].(float64) != 320 || updated["valid_days"] != nil {
		t.Fatalf("updated = %+v", updated)
	}
	// 新建时 SKU 重复：中文提示
	env := call(t, r, "POST", "/api/v1/admin/boost/point-packs", map[string]interface{}{"sku_id": sku.ID, "points": 1})
	if env.StatusCode == 0 || !strings.Contains(env.Msg, "已经有积分包") {
		t.Fatalf("dup pack env = %+v", env)
	}

	// 发放（审计） + 重复提交
	body := map[string]interface{}{"user_id": e.user.ID, "amount": 100, "reason": "客服补偿", "request_id": "ui-1"}
	ok(t, call(t, r, "POST", "/api/v1/admin/boost/points/grant", body), nil)
	ok(t, call(t, r, "POST", "/api/v1/admin/boost/points/grant", body), nil)
	// 扣减超额：提示可用数量
	env = call(t, r, "POST", "/api/v1/admin/boost/points/deduct", map[string]interface{}{"user_id": e.user.ID, "amount": 999, "reason": "x", "request_id": "ui-2"})
	if env.StatusCode == 0 || !strings.Contains(env.Msg, "可用积分只有 100 点") {
		t.Fatalf("deduct env = %+v", env)
	}

	// 用户买积分包
	order := e.buy(e.user.ID, product, sku, 1)
	e.payOnline(order)

	var summary struct {
		Available    int64 `json:"available"`
		ExpiringSoon []struct {
			Amount int64 `json:"amount"`
		} `json:"expiring_soon"`
	}
	ok(t, call(t, r, "GET", "/api/v1/boost/points", nil), &summary)
	if summary.Available != 450 || len(summary.ExpiringSoon) != 0 {
		t.Fatalf("summary = %+v", summary)
	}
	var ledger []map[string]interface{}
	ok(t, call(t, r, "GET", "/api/v1/boost/points/ledger", nil), &ledger)
	if len(ledger) != 2 || ledger[0]["type"] != "purchase" || !strings.HasPrefix(ledger[1]["remark"].(string), "平台调整：") {
		t.Fatalf("user ledger = %+v", ledger)
	}
	if _, leaked := ledger[1]["operator_admin_id"]; leaked {
		t.Fatalf("user ledger must not expose operator admin")
	}
	if _, leaked := ledger[0]["biz_key"]; leaked {
		t.Fatalf("user ledger must not expose biz_key")
	}

	// 后台账户列表 / 用户积分详情
	var accounts []map[string]interface{}
	ok(t, call(t, r, "GET", "/api/v1/admin/boost/point-accounts?keyword=buyer", nil), &accounts)
	if len(accounts) != 1 || accounts[0]["available"].(float64) != 450 {
		t.Fatalf("accounts = %+v", accounts)
	}
	var detail struct {
		Lots []map[string]interface{} `json:"lots"`
	}
	ok(t, call(t, r, "GET", "/api/v1/admin/boost/users/1/points", nil), &detail)
	if len(detail.Lots) != 2 {
		t.Fatalf("lots = %+v", detail.Lots)
	}

	// 撤回
	var revoke map[string]interface{}
	ok(t, call(t, r, "POST", "/api/v1/admin/boost/points/revoke-order", map[string]interface{}{"order_no": order.OrderNo}), &revoke)
	if revoke["revoked"].(float64) != 350 || revoke["suggest_refund"] != "30.00" {
		t.Fatalf("revoke = %+v", revoke)
	}

	// 公开积分包列表
	var public []map[string]interface{}
	ok(t, call(t, r, "GET", "/api/v1/public/boost/point-packs", nil), &public)
	if len(public) != 1 || public[0]["product_slug"] != "http" || public[0]["price"] != "30.00" {
		t.Fatalf("public packs = %+v", public)
	}

	actions := map[string]int{}
	for _, rec := range audit.records {
		actions[rec.Action]++
		if rec.OperatorAdminID != 7 {
			t.Fatalf("audit operator = %d", rec.OperatorAdminID)
		}
	}
	// 重复发放也记审计（同一 biz_key，便于追查重复提交）；撤回只记一次
	if actions["boost_point_pack_update"] != 1 || actions["boost_points_grant"] != 2 || actions["boost_points_revoke_order"] != 1 {
		t.Fatalf("audit actions = %v", actions)
	}
}
