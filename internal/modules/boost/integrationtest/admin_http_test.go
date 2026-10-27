package integrationtest

import (
	"bytes"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"

	auditlogapp "github.com/dujiao-next/internal/modules/auditlog/application"
	boosthttp "github.com/dujiao-next/internal/modules/boost/transport/http"

	"github.com/gin-gonic/gin"
)

type fakeAudit struct{ records []auditlogapp.AuthzRecord }

func (f *fakeAudit) Record(r auditlogapp.AuthzRecord) error {
	f.records = append(f.records, r)
	return nil
}

type envelope struct {
	StatusCode int             `json:"status_code"`
	Msg        string          `json:"msg"`
	Data       json.RawMessage `json:"data"`
}

func newAdminRouter(t *testing.T) (*gin.Engine, *fakeAudit) {
	t.Helper()
	gin.SetMode(gin.TestMode)
	svc, _ := newCatalog(t)
	audit := &fakeAudit{}
	r := gin.New()
	authorized := r.Group("/api/v1/admin", func(c *gin.Context) {
		c.Set("admin_id", uint(7))
		c.Set("username", "ops")
		c.Set("request_id", "req-1")
	})
	boosthttp.RegisterAdminRoutes(authorized, boosthttp.NewAdminHandler(svc, audit))
	return r, audit
}

func call(t *testing.T, r *gin.Engine, method, path string, body interface{}) envelope {
	t.Helper()
	var reader *bytes.Reader
	switch b := body.(type) {
	case nil:
		reader = bytes.NewReader(nil)
	case string:
		reader = bytes.NewReader([]byte(b))
	default:
		data, _ := json.Marshal(b)
		reader = bytes.NewReader(data)
	}
	req := httptest.NewRequest(method, path, reader)
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("%s %s: http %d %s", method, path, w.Code, w.Body.String())
	}
	var env envelope
	if err := json.Unmarshal(w.Body.Bytes(), &env); err != nil {
		t.Fatalf("%s %s: decode %s: %v", method, path, w.Body.String(), err)
	}
	return env
}

func ok(t *testing.T, env envelope, into interface{}) {
	t.Helper()
	if env.StatusCode != 0 {
		t.Fatalf("status_code = %d msg = %s data = %s", env.StatusCode, env.Msg, env.Data)
	}
	if into != nil {
		if err := json.Unmarshal(env.Data, into); err != nil {
			t.Fatalf("decode data %s: %v", env.Data, err)
		}
	}
}

func TestAdminHTTPFullCatalogFlow(t *testing.T) {
	r, audit := newAdminRouter(t)
	base := "/api/v1/admin/boost"

	var game struct {
		ID      uint   `json:"id"`
		Code    string `json:"code"`
		Status  string `json:"status"`
		Version int    `json:"version"`
	}
	ok(t, call(t, r, "POST", base+"/games", map[string]interface{}{"code": "starfall", "name": "星落幻境"}), &game)
	if game.Code != "starfall" || game.Status != "draft" {
		t.Fatalf("game = %+v", game)
	}
	gid := fmt.Sprint(game.ID)

	dup := call(t, r, "POST", base+"/games", map[string]interface{}{"code": "starfall", "name": "x"})
	if dup.StatusCode != 400 || dup.Msg != "游戏 code「starfall」已存在" {
		t.Fatalf("duplicate = %+v", dup)
	}
	notReady := call(t, r, "POST", base+"/games/"+gid+"/status", map[string]string{"status": "active"})
	if notReady.StatusCode != 400 || notReady.Msg != "上线前至少要有一个启用的区服" {
		t.Fatalf("not ready = %+v", notReady)
	}

	var server struct{ ID uint }
	ok(t, call(t, r, "POST", base+"/games/"+gid+"/servers", map[string]string{"code": "cn-sky", "name": "国服 · 天空岛", "platform": "android"}), &server)
	var servers []map[string]interface{}
	ok(t, call(t, r, "GET", base+"/games/"+gid+"/servers", nil), &servers)
	if len(servers) != 1 || servers[0]["platform"] != "android" {
		t.Fatalf("servers = %+v", servers)
	}

	def := string(mainStageDefinition(t))

	// 编辑器实时校验：非法定义返回 200 + 错误列表，不报错
	var check struct {
		OK     bool `json:"ok"`
		Errors []struct {
			Path string `json:"path"`
		} `json:"errors"`
	}
	ok(t, call(t, r, "POST", base+"/features/validate", map[string]interface{}{"definition": map[string]interface{}{"code": "x"}}), &check)
	if check.OK || len(check.Errors) == 0 {
		t.Fatalf("validate bad = %+v", check)
	}
	// 定义可以以 JSON 字符串（编辑器原文）提交
	ok(t, call(t, r, "POST", base+"/features/validate", map[string]interface{}{"definition": def}), &check)
	if !check.OK {
		t.Fatalf("validate good = %+v", check)
	}

	var plan struct {
		OK       bool  `json:"ok"`
		Total    int64 `json:"total"`
		RawTotal int64 `json:"rawTotal"`
	}
	ok(t, call(t, r, "POST", base+"/features/preview", map[string]interface{}{
		"definition":    def,
		"params":        map[string]interface{}{"mode": "sweep", "stages": []interface{}{map[string]interface{}{"item": "1-1", "count": 10}, map[string]interface{}{"item": "2-4", "count": 3}}},
		"discount_rate": 90,
	}), &plan)
	if !plan.OK || plan.RawTotal != 35 || plan.Total != 31 {
		t.Fatalf("preview = %+v", plan)
	}

	// 保存非法定义：400 + 全部错误（前端据此标注）
	bad := call(t, r, "POST", base+"/features", map[string]interface{}{"game_id": game.ID, "definition": `{"code":"bad","name":"x","fields":[]}`})
	var badData struct {
		Errors []struct{ Path, Msg string } `json:"errors"`
	}
	_ = json.Unmarshal(bad.Data, &badData)
	if bad.StatusCode != 400 || len(badData.Errors) != 1 || badData.Errors[0].Path != "fields" {
		t.Fatalf("bad definition = %+v %s", bad, bad.Data)
	}

	var saved struct {
		Feature struct {
			ID         uint                   `json:"id"`
			Name       string                 `json:"name"`
			Version    int                    `json:"version"`
			Definition map[string]interface{} `json:"definition"`
		} `json:"feature"`
		Changed bool `json:"changed"`
	}
	ok(t, call(t, r, "POST", base+"/features", map[string]interface{}{"game_id": game.ID, "definition": def}), &saved)
	if !saved.Changed || saved.Feature.Name != "主线关卡" || saved.Feature.Version != 1 || saved.Feature.Definition["timeout_minutes"] != float64(240) {
		t.Fatalf("saved = %+v", saved)
	}
	fid := fmt.Sprint(saved.Feature.ID)

	// 不带 game_id 也能保存新版本；旧版本号冲突
	ok(t, call(t, r, "PUT", base+"/features/"+fid, map[string]interface{}{"version": 1, "definition": `{"code":"main_stage","name":"主线关卡 v2","base_points":3,"fields":[{"key":"a","type":"switch","label":"A"}]}`}), &saved)
	if saved.Feature.Version != 2 {
		t.Fatalf("v2 = %+v", saved)
	}
	conflict := call(t, r, "PUT", base+"/features/"+fid, map[string]interface{}{"version": 1, "definition": def})
	if conflict.StatusCode != 400 || conflict.Msg != "数据已被其他人修改，请刷新后重试" {
		t.Fatalf("conflict = %+v", conflict)
	}

	var versions []struct {
		Version    int             `json:"version"`
		Definition json.RawMessage `json:"definition"`
	}
	ok(t, call(t, r, "GET", base+"/features/"+fid+"/versions", nil), &versions)
	if len(versions) != 2 || versions[0].Version != 2 || len(versions[0].Definition) != 0 {
		t.Fatalf("versions = %+v", versions)
	}
	var v1 struct {
		Definition map[string]interface{} `json:"definition"`
	}
	ok(t, call(t, r, "GET", base+"/features/"+fid+"/versions/1", nil), &v1)
	if v1.Definition["name"] != "主线关卡" {
		t.Fatalf("v1 = %+v", v1)
	}

	ok(t, call(t, r, "POST", base+"/features/"+fid+"/status", map[string]string{"status": "active"}), nil)
	ok(t, call(t, r, "POST", base+"/games/"+gid+"/status", map[string]string{"status": "active"}), &game)
	if game.Status != "active" {
		t.Fatalf("live game = %+v", game)
	}

	var games []struct {
		Code               string `json:"code"`
		ActiveServerCount  int    `json:"active_server_count"`
		ActiveFeatureCount int    `json:"active_feature_count"`
	}
	ok(t, call(t, r, "GET", base+"/games", nil), &games)
	if len(games) != 1 || games[0].ActiveServerCount != 1 || games[0].ActiveFeatureCount != 1 {
		t.Fatalf("games = %+v", games)
	}

	missing := call(t, r, "GET", base+"/features/9999", nil)
	if missing.StatusCode != 404 {
		t.Fatalf("missing = %+v", missing)
	}
	wrongGame := call(t, r, "POST", base+"/games/9999/servers/"+fmt.Sprint(server.ID)+"/status", map[string]string{"status": "disabled"})
	if wrongGame.StatusCode != 404 {
		t.Fatalf("server under wrong game = %+v", wrongGame)
	}

	// 每个成功的写操作都有审计；没变化的保存、失败的请求不记
	want := []string{"boost.game.create", "boost.server.create", "boost.feature.create", "boost.feature.update", "boost.feature.status", "boost.game.status"}
	if len(audit.records) != len(want) {
		t.Fatalf("audit = %+v", audit.records)
	}
	for i, rec := range audit.records {
		if rec.Action != want[i] || rec.OperatorAdminID != 7 || rec.OperatorUsername != "ops" || rec.RequestID != "req-1" || rec.Object == "" {
			t.Fatalf("audit[%d] = %+v, want action %s", i, rec, want[i])
		}
	}
}
