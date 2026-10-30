package integrationtest

import (
	"encoding/json"
	"fmt"
	"net/http/httptest"
	"strings"
	"testing"

	boosthttp "github.com/dujiao-next/internal/modules/boost/transport/http"

	"github.com/gin-gonic/gin"
)

// newTaskRouter 用户端路由按请求头 X-Test-User 决定当前用户；后台路由固定 admin 7。
func newTaskRouter(t *testing.T, simulator bool) (*taskEnv, *gin.Engine, *fakeAudit) {
	t.Helper()
	gin.SetMode(gin.TestMode)
	e := newTaskEnv(t)
	audit := &fakeAudit{}
	r := gin.New()
	user := r.Group("/api/v1", func(c *gin.Context) {
		var id uint
		fmt.Sscan(c.GetHeader("X-Test-User"), &id)
		if id == 0 {
			id = e.vip.ID
		}
		c.Set("user_id", id)
	})
	boosthttp.RegisterTaskUserRoutes(user, boosthttp.NewTaskHandler(e.tasks, nil, false))
	admin := r.Group("/api/v1/admin", func(c *gin.Context) {
		c.Set("admin_id", uint(7))
		c.Set("username", "ops")
	})
	boosthttp.RegisterTaskAdminRoutes(admin, boosthttp.NewTaskHandler(e.tasks, audit, simulator))
	return e, r, audit
}

func TestTaskHTTPUserFlowWithSimulator(t *testing.T) {
	e, r, audit := newTaskRouter(t, true)

	// 绑定：返回体里没有密码 / 密文 / 指纹
	env := call(t, r, "POST", "/api/v1/boost/bindings", map[string]interface{}{
		"game_id": e.starfall, "server_id": e.starfallCN, "login": "hero@example.test", "password": "Secret-Pass-001", "display_name": "大号",
	})
	ok(t, env, nil)
	raw := string(env.Data)
	for _, leak := range []string{"Secret-Pass-001", "credential", "fingerprint", "hero@example.test", "lease_token"} {
		if strings.Contains(raw, leak) {
			t.Fatalf("binding response leaks %q: %s", leak, raw)
		}
	}
	var binding struct {
		ID     uint   `json:"id"`
		Status string `json:"status"`
	}
	ok(t, env, &binding)

	// 模拟执行端：领取 → 要验证码 → 用户提交 → 再领取 → 成功
	act := func(body map[string]interface{}) envelope {
		return call(t, r, "POST", "/api/v1/admin/boost/simulator", body)
	}
	start := act(map[string]interface{}{"kind": "binding", "id": binding.ID, "action": "start"})
	ok(t, start, nil)
	if strings.Contains(string(start.Data), "Secret-Pass-001") {
		t.Fatal("simulator must not echo credentials to the admin page")
	}
	ok(t, act(map[string]interface{}{"kind": "binding", "id": binding.ID, "action": "need_input", "input_kind": "sms_code"}), nil)
	var list []map[string]interface{}
	ok(t, call(t, r, "GET", "/api/v1/boost/bindings", nil), &list)
	if len(list) != 1 || list[0]["status"] != "WAIT_INPUT" || list[0]["input"] == nil {
		t.Fatalf("bindings = %+v", list)
	}
	ok(t, call(t, r, "POST", fmt.Sprintf("/api/v1/boost/bindings/%d/input", binding.ID), map[string]string{"value": "246810"}), nil)
	start = act(map[string]interface{}{"kind": "binding", "id": binding.ID, "action": "start"})
	ok(t, start, nil)
	if strings.Contains(string(start.Data), "246810") || !strings.Contains(string(start.Data), `"input_received":true`) {
		t.Fatalf("simulator start = %s", start.Data)
	}
	ok(t, act(map[string]interface{}{"kind": "binding", "id": binding.ID, "action": "finish"}), nil)

	// 下单：前端塞的价格无效
	var created struct {
		Task struct {
			TaskNo     string `json:"task_no"`
			ID         uint   `json:"id"`
			PointsHeld int64  `json:"points_held"`
		} `json:"task"`
	}
	ok(t, call(t, r, "POST", "/api/v1/boost/tasks", map[string]interface{}{
		"binding_id": binding.ID, "feature_id": e.features["main_stage"], "points": 1, "price": 1,
		"params":          map[string]interface{}{"mode": "sweep", "stages": []map[string]interface{}{{"item": "1-1", "count": 10}, {"item": "2-4", "count": 3}}, "points": 1},
		"idempotency_key": "ui-1",
	}), &created)
	if created.Task.PointsHeld != 31 {
		t.Fatalf("held = %d, want 31", created.Task.PointsHeld)
	}
	// 参数错误：返回字段错误和稳定错误码
	env = call(t, r, "POST", "/api/v1/boost/tasks", map[string]interface{}{"binding_id": binding.ID, "feature_id": e.features["main_stage"], "params": map[string]interface{}{"mode": "sweep", "stages": []string{"3-3"}}})
	if env.StatusCode == 0 || !strings.Contains(string(env.Data), `"PARAMS_INVALID"`) || !strings.Contains(string(env.Data), `"stages"`) {
		t.Fatalf("params error env = %+v %s", env, env.Data)
	}

	// 别人看不到、取消不了
	other := func(method, path string) envelope {
		return callAs(t, r, e.plain.ID, method, path)
	}
	if env := other("GET", "/api/v1/boost/tasks/"+created.Task.TaskNo); env.StatusCode == 0 {
		t.Fatal("other user must not read the task")
	}
	if env := other("POST", "/api/v1/boost/tasks/"+created.Task.TaskNo+"/cancel"); env.StatusCode == 0 {
		t.Fatal("other user must not cancel the task")
	}
	if env := other("DELETE", fmt.Sprintf("/api/v1/boost/bindings/%d", binding.ID)); env.StatusCode == 0 {
		t.Fatal("other user must not delete the binding")
	}

	// 执行：领取 → 进度 4 → 失败（体力不足）
	ok(t, act(map[string]interface{}{"kind": "task", "id": created.Task.ID, "action": "start"}), nil)
	ok(t, act(map[string]interface{}{"kind": "task", "id": created.Task.ID, "action": "progress", "steps": []map[string]int{{"seq": 1, "done": 3}, {"seq": 2, "done": 1}}}), nil)
	ok(t, act(map[string]interface{}{"kind": "task", "id": created.Task.ID, "action": "fail", "error_code": "INSUFFICIENT_STAMINA", "steps": []map[string]int{{"seq": 1, "done": 10}, {"seq": 2, "done": 1}}}), nil)
	var detail struct {
		Task struct {
			Status         string `json:"status"`
			PointsConsumed int64  `json:"points_consumed"`
			PointsReleased int64  `json:"points_released"`
		} `json:"task"`
		Steps []struct {
			Done int64 `json:"done"`
		} `json:"steps"`
	}
	ok(t, call(t, r, "GET", "/api/v1/boost/tasks/"+created.Task.TaskNo, nil), &detail)
	if detail.Task.Status != "FAIL" || detail.Task.PointsConsumed != 22 || detail.Task.PointsReleased != 9 || len(detail.Steps) != 2 || detail.Steps[0].Done != 10 || detail.Steps[1].Done != 1 {
		t.Fatalf("detail = %+v", detail)
	}

	// 后台列表 / 详情 / 审计
	var adminTasks []map[string]interface{}
	ok(t, call(t, r, "GET", "/api/v1/admin/boost/tasks?status=FAIL", nil), &adminTasks)
	if len(adminTasks) != 1 || adminTasks[0]["user_email"] == "" {
		t.Fatalf("admin tasks = %+v", adminTasks)
	}
	var adminBindings []map[string]interface{}
	env = call(t, r, "GET", "/api/v1/admin/boost/bindings", nil)
	ok(t, env, &adminBindings)
	if len(adminBindings) != 1 || strings.Contains(string(env.Data), "Secret") {
		t.Fatalf("admin bindings = %s", env.Data)
	}
	if len(audit.records) == 0 {
		t.Fatal("simulator actions must be audited")
	}
	e.checkTaskInvariants()
}

func TestTaskHTTPSimulatorDisabledByDefault(t *testing.T) {
	e, r, _ := newTaskRouter(t, false)
	b := e.bind(e.vip.ID, e.starfall, "x@example.test")
	env := call(t, r, "POST", "/api/v1/admin/boost/simulator", map[string]interface{}{"kind": "binding", "id": b.ID, "action": "start"})
	if env.StatusCode == 0 {
		t.Fatal("simulator must be off unless boost.dev_simulator=true")
	}
	if env := call(t, r, "POST", "/api/v1/admin/boost/simulator/reap", nil); env.StatusCode == 0 {
		t.Fatal("reap via simulator must be off")
	}
	var q map[string]interface{}
	ok(t, call(t, r, "GET", "/api/v1/admin/boost/simulator", nil), &q)
	if q["enabled"] != false || q["tasks"] != nil {
		t.Fatalf("queue = %+v", q)
	}
	if e.binding(b.ID).Status != "PENDING" {
		t.Fatal("binding must stay untouched")
	}
}

func TestTaskHTTPAdminCancel(t *testing.T) {
	e, r, audit := newTaskRouter(t, false)
	b := e.verifiedBinding(e.vip.ID, e.starfall)
	task := e.create(e.vip.ID, b.ID, "daily", `{}`)
	var res map[string]interface{}
	ok(t, call(t, r, "POST", "/api/v1/admin/boost/tasks/"+task.TaskNo+"/cancel", nil), &res)
	if res["status"] != "CANCELLED" || len(audit.records) != 1 {
		t.Fatalf("res = %+v audit = %d", res, len(audit.records))
	}
	e.checkTaskInvariants()
}

func callAs(t *testing.T, r *gin.Engine, userID uint, method, path string) envelope {
	t.Helper()
	req := httptest.NewRequest(method, path, nil)
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-Test-User", fmt.Sprint(userID))
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)
	var env envelope
	if err := json.Unmarshal(w.Body.Bytes(), &env); err != nil {
		t.Fatalf("%s %s: decode %s: %v", method, path, w.Body.String(), err)
	}
	return env
}
