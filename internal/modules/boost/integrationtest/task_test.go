package integrationtest

// 与 prototype/test/engine.test.js 的任务 / 绑定场景一一对应（测试名后的注释是原型用例名）。
// 预期积分由原型 catalog.js 算出：会员 9 折；idle_farm 不参与折扣。

import (
	"encoding/json"
	"strings"
	"sync"
	"testing"
	"time"

	boostapp "github.com/dujiao-next/internal/modules/boost/application"
	boostcontract "github.com/dujiao-next/internal/modules/boost/contract"
	boostdomain "github.com/dujiao-next/internal/modules/boost/domain"

	"gorm.io/gorm"
)

// 创建任务：冻结积分、记录执行计划和摘要；参数错误不冻结
func TestCreateTaskHoldsPointsAndSnapshotsPlan(t *testing.T) {
	e := newTaskEnv(t)
	b := e.verifiedBinding(e.vip.ID, e.starfall)
	before := e.account(e.vip.ID).Available

	_, err := e.tasks.CreateTask(boostapp.CreateTaskInput{UserID: e.vip.ID, BindingID: b.ID, FeatureID: e.features["main_stage"], Params: []byte(`{"mode":"sweep","stages":["3-3"]}`)})
	wantCode(t, err, boostcontract.ErrParamsInvalid)
	if ve, ok := boostapp.AsValidationError(err); !ok || len(ve.Issues) == 0 || ve.Issues[0].Path != "stages" {
		t.Fatalf("want field error on stages, got %+v", err)
	}
	if e.account(e.vip.ID).Available != before {
		t.Fatal("params error must not hold points")
	}

	task := e.create(e.vip.ID, b.ID, "main_stage", string(sweep([2]interface{}{"1-1", 5}, [2]interface{}{"1-2", 5})))
	if task.Units != 10 || task.Status != boostdomain.TaskStatusPending || task.FeatureVersion != 1 {
		t.Fatalf("task = %+v", task)
	}
	plan, err := boostapp.DecodePlan(task)
	e.must(err)
	if len(plan.Steps) != 2 || plan.Steps[0].Count != 5 || plan.Rate != 90 {
		t.Fatalf("plan = %+v", plan)
	}
	if !strings.Contains(task.Summary, "扫荡") || !strings.Contains(task.Summary, "1-1 迷雾入口 ×5") {
		t.Fatalf("summary = %q", task.Summary)
	}
	if got := e.account(e.vip.ID); got.Frozen != task.PointsHeld || got.Available != before-task.PointsHeld {
		t.Fatalf("account = %+v, held %d", got, task.PointsHeld)
	}
	snap, err := boostapp.DecodeSnapshot(task)
	e.must(err)
	if snap.RefundPolicy != "prorate" || snap.Capability != "main_stage" || snap.TimeoutMinutes != 240 {
		t.Fatalf("snapshot = %+v", snap)
	}
	if task.DeadlineAt == nil || !task.DeadlineAt.Equal(e.clock.Add(240*time.Minute)) {
		t.Fatalf("deadline = %v", task.DeadlineAt)
	}
	e.checkTaskInvariants()
}

// 报价：服务端按 JSON 计算；前端塞进来的价格字段无效；会员 9 折
func TestCreateTaskIgnoresClientPrice(t *testing.T) {
	e := newTaskEnv(t)
	b := e.verifiedBinding(e.vip.ID, e.starfall)
	params := `{"mode":"sweep","stages":[{"item":"1-1","count":10},{"item":"2-4","count":3}],"points":1,"total":1}`
	task := e.create(e.vip.ID, b.ID, "main_stage", params)
	if task.PointsHeld != 31 { // rawTotal 35 × 0.9 向下取整
		t.Fatalf("held = %d, want 31", task.PointsHeld)
	}
	if strings.Contains(task.ParamsJSON, "points") || strings.Contains(task.ParamsJSON, "total") {
		t.Fatalf("unknown params must be dropped: %s", task.ParamsJSON)
	}
}

// 创建任务：游戏不匹配、绑定未验证、并发上限、积分不足
func TestCreateTaskRejections(t *testing.T) {
	e := newTaskEnv(t)
	b := e.verifiedBinding(e.vip.ID, e.starfall)

	_, err := e.tasks.CreateTask(boostapp.CreateTaskInput{UserID: e.vip.ID, BindingID: b.ID, FeatureID: e.features["farm"]})
	wantCode(t, err, boostcontract.ErrParamsInvalid)

	pending := e.bind(e.vip.ID, e.starfall, "new@x.test")
	_, err = e.tasks.CreateTask(boostapp.CreateTaskInput{UserID: e.vip.ID, BindingID: pending.ID, FeatureID: e.features["daily"]})
	wantCode(t, err, boostcontract.ErrInvalidTransition)

	// 别人的绑定
	_, err = e.tasks.CreateTask(boostapp.CreateTaskInput{UserID: e.plain.ID, BindingID: b.ID, FeatureID: e.features["daily"]})
	wantCode(t, err, boostcontract.ErrNotFound)

	e.create(e.vip.ID, b.ID, "abyss", `{"floors":["f5"]}`)
	_, err = e.tasks.CreateTask(boostapp.CreateTaskInput{UserID: e.vip.ID, BindingID: b.ID, FeatureID: e.features["abyss"], Params: []byte(`{"floors":["f6"]}`)})
	wantCode(t, err, boostcontract.ErrTaskLimit)

	pb := e.verifiedBinding(e.plain.ID, e.starfall)
	_, err = e.tasks.CreateTask(boostapp.CreateTaskInput{UserID: e.plain.ID, BindingID: pb.ID, FeatureID: e.features["daily"]})
	wantCode(t, err, boostcontract.ErrPointsInsufficient)
	var n int64
	e.must(e.db.Model(&boostdomain.Task{}).Where("user_id = ?", e.plain.ID).Count(&n).Error)
	if n != 0 {
		t.Fatal("insufficient points must roll back the task row")
	}
	e.checkTaskInvariants()
}

// 后台：下架功能后用户不能再下单
func TestCreateTaskRejectsDisabledFeature(t *testing.T) {
	e := newTaskEnv(t)
	b := e.verifiedBinding(e.vip.ID, e.starfall)
	_, err := e.catalog.SetFeatureStatus(e.features["daily"], boostdomain.FeatureStatusDisabled)
	e.must(err)
	_, err = e.tasks.CreateTask(boostapp.CreateTaskInput{UserID: e.vip.ID, BindingID: b.ID, FeatureID: e.features["daily"]})
	wantCode(t, err, boostcontract.ErrNotFound)
}

// 创建任务幂等：同一个幂等键只冻结一次（含并发）
func TestCreateTaskIdempotent(t *testing.T) {
	e := newTaskEnv(t)
	b := e.verifiedBinding(e.vip.ID, e.starfall)
	in := boostapp.CreateTaskInput{UserID: e.vip.ID, BindingID: b.ID, FeatureID: e.features["daily"], IdempotencyKey: "k"}
	first, err := e.tasks.CreateTask(in)
	e.must(err)
	second, err := e.tasks.CreateTask(in)
	e.must(err)
	if !second.Duplicated || second.Task.ID != first.Task.ID {
		t.Fatalf("second = %+v", second)
	}

	var wg sync.WaitGroup
	ids := make(chan uint, 8)
	for i := 0; i < 8; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			r, err := e.tasks.CreateTask(boostapp.CreateTaskInput{UserID: e.vip.ID, BindingID: b.ID, FeatureID: e.features["idle_farm"], Params: []byte(`{"area":"plain","minutes":60}`), IdempotencyKey: "race"})
			if err == nil {
				ids <- r.Task.ID
			}
		}()
	}
	wg.Wait()
	close(ids)
	seen := map[uint]bool{}
	for id := range ids {
		seen[id] = true
	}
	if len(seen) != 1 {
		t.Fatalf("concurrent same key created %d tasks", len(seen))
	}
	if e.account(e.vip.ID).Frozen != first.Task.PointsHeld+60 {
		t.Fatalf("frozen = %d", e.account(e.vip.ID).Frozen)
	}
	e.checkTaskInvariants()
}

// 扫荡完成：执行端收到步骤列表，完成后全额扣除
func TestFinishConsumesAll(t *testing.T) {
	e := newTaskEnv(t)
	b := e.verifiedBinding(e.vip.ID, e.starfall)
	task := e.create(e.vip.ID, b.ID, "main_stage", string(sweep([2]interface{}{"1-1", 10})))
	item := e.start(task.ID)
	if item.Task == nil || len(item.Task.Steps) != 1 || item.Task.Steps[0].Count != 10 || item.Credential.Password != "Secret-Pass-001" || item.SessionRef != "sess" {
		t.Fatalf("started = %+v", item)
	}
	var ref map[string]interface{}
	e.must(json.Unmarshal(item.Task.Steps[0].Ref, &ref))
	if ref["item"] != "1-1" || ref["mode"] != "sweep" || ref["group"] == "" {
		t.Fatalf("step ref = %v", ref)
	}
	res, err := e.tasks.Finish(taskExec(task.ID), boostapp.FinishInput{ResultJSON: []byte(`{"drops":3}`)})
	e.must(err)
	got := e.task(task.ID)
	if res.Status != boostdomain.TaskStatusSuccess || got.PointsConsumed != 18 || got.PointsReleased != 0 || got.UnitsDone != 10 || got.ResultJSON != `{"drops":3}` {
		t.Fatalf("task = %+v", got)
	}
	if acc := e.account(e.vip.ID); acc.Frozen != 0 || acc.Available != 1000-18 {
		t.Fatalf("account = %+v", acc)
	}
	e.checkTaskInvariants()
}

// 部分完成：体力不足失败，按已完成关卡的价格结算（BOSS 关更贵）
func TestPartialFailSettlesByCompletedSteps(t *testing.T) {
	e := newTaskEnv(t)
	b := e.verifiedBinding(e.vip.ID, e.starfall)
	task := e.create(e.vip.ID, b.ID, "main_stage", string(sweep([2]interface{}{"1-1", 3}, [2]interface{}{"1-4", 2})))
	if task.PointsHeld != 12 {
		t.Fatalf("held = %d", task.PointsHeld)
	}
	e.start(task.ID)
	_, err := e.tasks.Progress(task.ID, boostapp.ProgressInput{Steps: steps(3, 1)})
	e.must(err)
	_, err = e.tasks.Fail(taskExec(task.ID), boostapp.FailInput{ErrorCode: boostdomain.ErrorInsufficientStamina, Steps: steps(3, 1)})
	e.must(err)
	got := e.task(task.ID)
	if got.Status != boostdomain.TaskStatusFail || got.PointsConsumed != 9 || got.PointsReleased != 3 || got.ErrorCode != "INSUFFICIENT_STAMINA" {
		t.Fatalf("task = %+v", got)
	}
	e.checkTaskInvariants()
}

// 系统原因失败全额退（full_on_system_fail）；用户原因按完成量；账号类错误同时让绑定失败
func TestSystemFailRefundsFully(t *testing.T) {
	e := newTaskEnv(t)
	b := e.verifiedBinding(e.vip.ID, e.starfall)
	before := e.account(e.vip.ID).Available
	task := e.create(e.vip.ID, b.ID, "daily", `{"extras":["dispatch"]}`)
	if task.PointsHeld != 22 {
		t.Fatalf("held = %d", task.PointsHeld)
	}
	e.start(task.ID)
	_, err := e.tasks.Fail(taskExec(task.ID), boostapp.FailInput{ErrorCode: boostdomain.ErrorPasswordChanged, Steps: steps(1)})
	e.must(err)
	if e.account(e.vip.ID).Available != before {
		t.Fatal("system failure must refund fully")
	}
	if e.binding(b.ID).Status != boostdomain.BindingStatusFail {
		t.Fatal("PASSWORD_CHANGED must fail the binding")
	}
	if got := e.task(task.ID); got.PointsReleased != 22 {
		t.Fatalf("task = %+v", got)
	}

	// 用户原因（顶号）按完成量：daily 只有 1 个单位，完成 1 = 全额
	b2 := e.verifiedBinding(e.vip.ID, e.starfall)
	t2 := e.create(e.vip.ID, b2.ID, "daily", `{}`)
	e.start(t2.ID)
	_, err = e.tasks.Fail(taskExec(t2.ID), boostapp.FailInput{ErrorCode: boostdomain.ErrorUserKicked, Steps: steps(1)})
	e.must(err)
	if got := e.task(t2.ID); got.PointsConsumed != t2.PointsHeld {
		t.Fatalf("user-caused failure settles by units: %+v", got)
	}
	e.checkTaskInvariants()
}

// 可重试错误回到排队，超过次数后失败
func TestRetryableFailRequeuesThenFails(t *testing.T) {
	e := newTaskEnv(t)
	b := e.verifiedBinding(e.vip.ID, e.starfall)
	task := e.create(e.vip.ID, b.ID, "abyss", `{"floors":["f5"]}`)
	e.start(task.ID)
	res, err := e.tasks.Fail(taskExec(task.ID), boostapp.FailInput{ErrorCode: boostdomain.ErrorNetwork})
	e.must(err)
	if res.Status != boostdomain.TaskStatusPending || res.RetryAt == nil {
		t.Fatalf("first network failure should requeue: %+v", res)
	}
	e.start(task.ID) // attempt 2 = max
	res, err = e.tasks.Fail(taskExec(task.ID), boostapp.FailInput{ErrorCode: boostdomain.ErrorNetwork})
	e.must(err)
	if res.Status != boostdomain.TaskStatusFail || res.PointsReleased != task.PointsHeld {
		t.Fatalf("second failure should fail with full refund (nothing done): %+v", res)
	}
	// 未知错误码：按执行端上报的 retryable
	t2 := e.create(e.vip.ID, b.ID, "abyss", `{"floors":["f6"]}`)
	e.start(t2.ID)
	res, err = e.tasks.Fail(taskExec(t2.ID), boostapp.FailInput{ErrorCode: "SOMETHING_NEW", Retryable: true})
	e.must(err)
	if res.Status != boostdomain.TaskStatusPending {
		t.Fatalf("unknown retryable code should requeue: %+v", res)
	}
	e.checkTaskInvariants()
}

// 取消：排队中全额退；执行中等待执行端确认后按完成量结算
func TestCancelQueuedAndRunning(t *testing.T) {
	e := newTaskEnv(t)
	b := e.verifiedBinding(e.vip.ID, e.starfall)
	before := e.account(e.vip.ID).Available
	t1 := e.create(e.vip.ID, b.ID, "idle_farm", `{"area":"plain","minutes":60}`)
	r, err := e.tasks.CancelTask(e.vip.ID, t1.TaskNo)
	e.must(err)
	if r.Status != boostdomain.TaskStatusCancelled || e.account(e.vip.ID).Available != before {
		t.Fatalf("queued cancel should refund fully: %+v", r)
	}
	_, err = e.tasks.CancelTask(e.vip.ID, t1.TaskNo)
	wantCode(t, err, boostcontract.ErrInvalidTransition)

	t2 := e.create(e.vip.ID, b.ID, "idle_farm", `{"area":"plain","minutes":60}`)
	e.start(t2.ID)
	_, err = e.tasks.Progress(t2.ID, boostapp.ProgressInput{Steps: steps(20)})
	e.must(err)
	_, err = e.tasks.CancelAck(t2.ID, steps(20))
	wantCode(t, err, boostcontract.ErrInvalidTransition) // 没有取消请求
	_, err = e.tasks.CancelTask(e.plain.ID, t2.TaskNo)
	wantCode(t, err, boostcontract.ErrNotFound) // 不能取消别人的任务
	r, err = e.tasks.CancelTask(e.vip.ID, t2.TaskNo)
	e.must(err)
	if !r.Requested || r.Status != boostdomain.TaskStatusProcessing {
		t.Fatalf("running cancel should only request: %+v", r)
	}
	hb, err := e.tasks.Progress(t2.ID, boostapp.ProgressInput{Steps: steps(25)})
	e.must(err)
	if !hb.CancelRequested {
		t.Fatal("heartbeat should report cancel request")
	}
	_, err = e.tasks.CancelAck(t2.ID, steps(25))
	e.must(err)
	got := e.task(t2.ID)
	if got.Status != boostdomain.TaskStatusCancelled || got.PointsConsumed != 25 || got.PointsReleased != 35 {
		t.Fatalf("task = %+v", got)
	}
	e.checkTaskInvariants()
}

// 执行端上报超过计划的完成量被截断；每一步完成量只增不减
func TestReportedUnitsAreClamped(t *testing.T) {
	e := newTaskEnv(t)
	b := e.verifiedBinding(e.vip.ID, e.starfall)
	task := e.create(e.vip.ID, b.ID, "main_stage", string(sweep([2]interface{}{"1-1", 2})))
	e.start(task.ID)
	_, err := e.tasks.Progress(task.ID, boostapp.ProgressInput{Steps: steps(1)})
	e.must(err)
	_, err = e.tasks.Progress(task.ID, boostapp.ProgressInput{Steps: steps(0)})
	e.must(err)
	if e.task(task.ID).UnitsDone != 1 {
		t.Fatal("units done must not go backwards")
	}
	_, err = e.tasks.Finish(taskExec(task.ID), boostapp.FinishInput{Steps: steps(999)})
	e.must(err)
	got := e.task(task.ID)
	if got.UnitsDone != 2 || got.PointsConsumed != got.PointsHeld {
		t.Fatalf("task = %+v", got)
	}
	e.checkTaskInvariants()
}

// 有基础价的功能：开始执行后才收基础价，未执行取消全退
func TestBasePointsOnlyAfterWork(t *testing.T) {
	e := newTaskEnv(t)
	b := e.verifiedBinding(e.vip.ID, e.starfall)
	task := e.create(e.vip.ID, b.ID, "daily", `{}`)
	e.start(task.ID)
	_, err := e.tasks.CancelTask(e.vip.ID, task.TaskNo)
	e.must(err)
	_, err = e.tasks.CancelAck(task.ID, nil)
	e.must(err)
	if got := e.task(task.ID); got.PointsConsumed != 0 || got.PointsReleased != got.PointsHeld {
		t.Fatalf("no work done → no base fee: %+v", got)
	}
	e.checkTaskInvariants()
}

// 同一游戏账号同时只跑一个任务
func TestOneProcessingTaskPerBinding(t *testing.T) {
	e := newTaskEnv(t)
	b := e.verifiedBinding(e.vip.ID, e.starfall)
	t1 := e.create(e.vip.ID, b.ID, "daily", `{}`)
	t2 := e.create(e.vip.ID, b.ID, "main_stage", string(sweep([2]interface{}{"1-1", 1})))
	e.start(t1.ID)
	_, err := e.tasks.Start(taskExec(t2.ID))
	wantCode(t, err, boostcontract.ErrBindingBusy)
	_, err = e.tasks.Finish(taskExec(t1.ID), boostapp.FinishInput{})
	e.must(err)
	e.start(t2.ID)

	// 数据库层兜底：直接写第二个 PROCESSING 会被部分唯一索引拒绝
	t3 := e.create(e.vip.ID, b.ID, "abyss", `{"floors":["f5"]}`)
	err = e.db.Model(&boostdomain.Task{}).Where("id = ?", t3.ID).Update("status", boostdomain.TaskStatusProcessing).Error
	if err == nil {
		t.Fatal("partial unique index should reject a second PROCESSING task")
	}
	e.checkTaskInvariants()
}

// 后台：修改功能生成新版本；已创建任务按旧快照结算
func TestTaskKeepsSnapshotAfterFeatureChange(t *testing.T) {
	e := newTaskEnv(t)
	b := e.verifiedBinding(e.vip.ID, e.starfall)
	params := string(sweep([2]interface{}{"1-1", 10}))
	t1 := e.create(e.vip.ID, b.ID, "main_stage", params)

	defs := seedDefinitions(t)
	var def map[string]interface{}
	e.must(json.Unmarshal(defs["starfall_main"], &def))
	item := def["catalogs"].(map[string]interface{})["main"].(map[string]interface{})["groups"].([]interface{})[0].(map[string]interface{})["items"].([]interface{})[0].(map[string]interface{})
	item["cost"].(map[string]interface{})["sweep"] = 20
	raw, _ := json.Marshal(def)
	saved, err := e.catalog.SaveFeature(boostapp.FeatureInput{ID: e.features["main_stage"], GameID: e.starfall, Definition: raw, Version: intp(1)})
	e.must(err)
	if saved.Feature.Version != 2 {
		t.Fatalf("version = %d", saved.Feature.Version)
	}
	e.start(t1.ID)
	_, err = e.tasks.Finish(taskExec(t1.ID), boostapp.FinishInput{})
	e.must(err)
	if got := e.task(t1.ID); got.PointsConsumed != t1.PointsHeld || got.FeatureVersion != 1 {
		t.Fatalf("old task must settle at old price: %+v", got)
	}
	t2 := e.create(e.vip.ID, b.ID, "main_stage", params)
	if t2.PointsHeld <= t1.PointsHeld || t2.FeatureVersion != 2 {
		t.Fatalf("new task should use new price: %d vs %d", t2.PointsHeld, t1.PointsHeld)
	}
}

// 任务 WAIT_INPUT：提交后恢复；超时失败全额退
func TestTaskWaitInput(t *testing.T) {
	e := newTaskEnv(t)
	b := e.verifiedBinding(e.vip.ID, e.starfall)
	task := e.create(e.vip.ID, b.ID, "abyss", `{"floors":["f5"]}`)
	e.start(task.ID)
	_, err := e.tasks.NeedInput(taskExec(task.ID), boostdomain.InputKindConfirm, "")
	wantCode(t, err, boostcontract.ErrInvalid) // 任务不支持游戏内确认
	res, err := e.tasks.NeedInput(taskExec(task.ID), boostdomain.InputKindSMSCode, "请输入短信验证码")
	e.must(err)
	if res.Status != boostdomain.TaskStatusWaitInput {
		t.Fatalf("res = %+v", res)
	}
	detail, err := e.tasks.GetTask(e.vip.ID, task.TaskNo)
	e.must(err)
	if detail.Input == nil || detail.Input.Prompt != "请输入短信验证码" {
		t.Fatalf("detail input = %+v", detail.Input)
	}
	wantCode(t, e.tasks.SubmitTaskInputByNo(e.plain.ID, task.TaskNo, "123456"), boostcontract.ErrNotFound)
	e.must(e.tasks.SubmitTaskInputByNo(e.vip.ID, task.TaskNo, "123456"))
	if got := e.task(task.ID); got.Status != boostdomain.TaskStatusPending {
		t.Fatalf("status = %s", got.Status)
	}
	var input boostdomain.Input
	e.must(e.db.Where("target = ? AND target_id = ?", "task", task.ID).Take(&input).Error)
	if input.ValueCipher == nil || strings.Contains(*input.ValueCipher, "123456") {
		t.Fatal("submitted value must be stored encrypted")
	}
	item := e.start(task.ID)
	if item.Input == nil || item.Input.Value != "123456" {
		t.Fatalf("worker should receive the code once: %+v", item.Input)
	}
	e.must(e.db.Take(&input, input.ID).Error)
	if input.ValueCipher != nil || input.Status != boostdomain.InputStatusConsumed {
		t.Fatal("code must be wiped after delivery")
	}

	// 第二次要输入，不提交 → 超时失败全额退
	_, err = e.tasks.NeedInput(taskExec(task.ID), "", "")
	e.must(err)
	e.advance(6 * time.Minute)
	n, err := e.tasks.Reap(100)
	e.must(err)
	got := e.task(task.ID)
	if n < 1 || got.Status != boostdomain.TaskStatusFail || got.ErrorCode != "INPUT_TIMEOUT" || got.PointsReleased != got.PointsHeld {
		t.Fatalf("task = %+v", got)
	}
	e.checkTaskInvariants()
}

// 绑定：验证码、游戏内确认、超时；解绑销毁凭据
func TestBindingLifecycle(t *testing.T) {
	e := newTaskEnv(t)
	b := e.bind(e.vip.ID, e.starfall, "Player.One@Example.test")
	if b.LoginMasked != "Pl***@Example.test" || b.Status != boostdomain.BindingStatusPending || b.KeyVersion != 1 {
		t.Fatalf("binding = %+v", b)
	}
	if b.CredentialCipher == nil || strings.Contains(*b.CredentialCipher, "Secret-Pass-001") || !strings.HasPrefix(*b.CredentialCipher, "v1:") {
		t.Fatal("credential must be encrypted with versioned key")
	}
	// 同一登录名（大小写不同）不能重复绑定
	_, err := e.tasks.CreateBinding(boostapp.CreateBindingInput{UserID: e.vip.ID, GameID: e.starfall, ServerID: e.starfallCN, Login: "player.one@example.test", Password: "x"})
	wantCode(t, err, boostcontract.ErrBindingDuplicate)
	// 区服不属于这个游戏
	_, err = e.tasks.CreateBinding(boostapp.CreateBindingInput{UserID: e.vip.ID, GameID: e.starfall, ServerID: e.ironcrestS1, Login: "other", Password: "x"})
	wantCode(t, err, boostcontract.ErrInvalid)

	exec := boostapp.Exec{Kind: boostdomain.InputTargetBinding, ID: b.ID}
	item, err := e.tasks.Start(exec)
	e.must(err)
	if item.Credential.Login != "Player.One@Example.test" || item.Credential.Password != "Secret-Pass-001" {
		t.Fatalf("worker credential = %+v", item.Credential)
	}
	_, err = e.tasks.NeedInput(exec, boostdomain.InputKindSMSCode, "")
	e.must(err)
	views, _, err := e.tasks.ListBindings(boostcontract.BindingFilter{UserID: e.vip.ID})
	e.must(err)
	if len(views) != 1 || views[0].Status != boostdomain.BindingStatusWaitInput || views[0].Input == nil {
		t.Fatalf("views = %+v", views)
	}
	if views[0].CredentialCipher != nil {
		t.Fatal("list must not carry ciphertext")
	}
	e.must(e.tasks.SubmitInput(e.vip.ID, boostdomain.InputTargetBinding, b.ID, "8888"))
	item, err = e.tasks.Start(exec)
	e.must(err)
	if item.Input == nil || item.Input.Value != "8888" {
		t.Fatal("binding code should be delivered")
	}
	_, err = e.tasks.NeedInput(exec, boostdomain.InputKindConfirm, "")
	e.must(err)
	if e.binding(b.ID).Status != boostdomain.BindingStatusWaitUser {
		t.Fatal("confirm should go WAIT_USER")
	}
	e.must(e.tasks.ConfirmInGame(e.vip.ID, b.ID))
	_, err = e.tasks.Start(exec)
	e.must(err)
	_, err = e.tasks.Finish(exec, boostapp.FinishInput{SessionRef: "session-token-abc"})
	e.must(err)
	got := e.binding(b.ID)
	if got.Status != boostdomain.BindingStatusSuccess || got.VerifiedAt == nil || got.SessionRefCipher == nil || strings.Contains(*got.SessionRefCipher, "session-token-abc") {
		t.Fatalf("binding = %+v", got)
	}

	// 超时：新绑定要验证码不提交
	b2 := e.bind(e.vip.ID, e.starfall, "second")
	exec2 := boostapp.Exec{Kind: boostdomain.InputTargetBinding, ID: b2.ID}
	_, err = e.tasks.Start(exec2)
	e.must(err)
	_, err = e.tasks.NeedInput(exec2, "", "")
	e.must(err)
	e.advance(6 * time.Minute)
	wantCode(t, e.tasks.SubmitInput(e.vip.ID, boostdomain.InputTargetBinding, b2.ID, "1"), boostcontract.ErrInputExpired)
	_, err = e.tasks.Reap(100)
	e.must(err)
	if got := e.binding(b2.ID); got.Status != boostdomain.BindingStatusFail || got.LastErrorCode != "INPUT_TIMEOUT" {
		t.Fatalf("b2 = %+v", got)
	}

	// 解绑：有未结束任务拒绝；结束后凭据销毁
	task := e.create(e.vip.ID, b.ID, "daily", `{}`)
	wantCode(t, e.tasks.DeleteBinding(e.vip.ID, b.ID), boostcontract.ErrBindingBusy)
	wantCode(t, e.tasks.DeleteBinding(e.plain.ID, b.ID), boostcontract.ErrNotFound)
	_, err = e.tasks.CancelTask(e.vip.ID, task.TaskNo)
	e.must(err)
	e.must(e.tasks.DeleteBinding(e.vip.ID, b.ID))
	got = e.binding(b.ID)
	if got.CredentialCipher != nil || got.SessionRefCipher != nil || got.DeletedAt == nil {
		t.Fatalf("credential not destroyed: %+v", got)
	}
	// 解绑后可以重新绑定同一个账号
	e.bind(e.vip.ID, e.starfall, "player.one@example.test")
	e.checkTaskInvariants()
}

// 绑定失败重试和每日次数
func TestBindingRetryAndDailyLimit(t *testing.T) {
	e := newTaskEnv(t)
	b := e.bind(e.plain.ID, e.starfall, "retry-me")
	exec := boostapp.Exec{Kind: boostdomain.InputTargetBinding, ID: b.ID}
	for i := 0; i < 2; i++ {
		_, err := e.tasks.Start(exec)
		e.must(err)
		res, err := e.tasks.Fail(exec, boostapp.FailInput{ErrorCode: boostdomain.ErrorNetwork})
		e.must(err)
		if res.Status != boostdomain.BindingStatusPending {
			t.Fatalf("attempt %d: %+v", i+1, res)
		}
	}
	_, err := e.tasks.Start(exec)
	e.must(err)
	res, err := e.tasks.Fail(exec, boostapp.FailInput{ErrorCode: boostdomain.ErrorNetwork})
	e.must(err)
	if res.Status != boostdomain.BindingStatusFail {
		t.Fatalf("third failure should fail: %+v", res)
	}
	b2 := e.bind(e.plain.ID, e.starfall, "wrong-pass")
	exec2 := boostapp.Exec{Kind: boostdomain.InputTargetBinding, ID: b2.ID}
	_, err = e.tasks.Start(exec2)
	e.must(err)
	res, err = e.tasks.Fail(exec2, boostapp.FailInput{ErrorCode: boostdomain.ErrorLoginFailed})
	e.must(err)
	if res.Status != boostdomain.BindingStatusFail {
		t.Fatal("LOGIN_FAILED is not retryable")
	}

	e.tasks.BindDailyLimit = 3
	e.bind(e.plain.ID, e.starfall, "third")
	_, err = e.tasks.CreateBinding(boostapp.CreateBindingInput{UserID: e.plain.ID, GameID: e.starfall, ServerID: e.starfallCN, Login: "fourth", Password: "x"})
	wantCode(t, err, boostcontract.ErrTaskLimit)
	e.advance(24 * time.Hour)
	e.bind(e.plain.ID, e.starfall, "next-day")
}

// 任务超过时限 → FAIL(TIMEOUT)，按完成量结算
func TestTaskDeadline(t *testing.T) {
	e := newTaskEnv(t)
	b := e.verifiedBinding(e.vip.ID, e.starfall)
	task := e.create(e.vip.ID, b.ID, "daily", `{}`) // timeout 30 分钟
	e.advance(29 * time.Minute)
	n, err := e.tasks.Reap(10)
	e.must(err)
	if n != 0 {
		t.Fatal("not overdue yet")
	}
	e.advance(2 * time.Minute)
	_, err = e.tasks.Reap(10)
	e.must(err)
	got := e.task(task.ID)
	if got.Status != boostdomain.TaskStatusFail || got.ErrorCode != "TIMEOUT" || got.PointsReleased != got.PointsHeld {
		t.Fatalf("task = %+v", got)
	}
	e.checkTaskInvariants()
}

// 账号在排队期间失效：领取时直接失败退款
func TestStartFailsWhenBindingInvalid(t *testing.T) {
	e := newTaskEnv(t)
	b := e.verifiedBinding(e.vip.ID, e.starfall)
	t1 := e.create(e.vip.ID, b.ID, "daily", `{}`)
	t2 := e.create(e.vip.ID, b.ID, "abyss", `{"floors":["f5"]}`)
	e.start(t1.ID)
	_, err := e.tasks.Fail(taskExec(t1.ID), boostapp.FailInput{ErrorCode: boostdomain.ErrorAccountBanned})
	e.must(err)
	_, err = e.tasks.Start(taskExec(t2.ID))
	e.must(err)
	if got := e.task(t2.ID); got.Status != boostdomain.TaskStatusFail || got.PointsReleased != got.PointsHeld {
		t.Fatalf("t2 = %+v", got)
	}
	e.checkTaskInvariants()
}

// 结算只发生一次；重复完成 / 失败被状态机拒绝
func TestSettlementHappensOnce(t *testing.T) {
	e := newTaskEnv(t)
	b := e.verifiedBinding(e.vip.ID, e.starfall)
	task := e.create(e.vip.ID, b.ID, "daily", `{}`)
	e.start(task.ID)
	_, err := e.tasks.Finish(taskExec(task.ID), boostapp.FinishInput{})
	e.must(err)
	_, err = e.tasks.Finish(taskExec(task.ID), boostapp.FinishInput{})
	wantCode(t, err, boostcontract.ErrInvalidTransition)
	_, err = e.tasks.Fail(taskExec(task.ID), boostapp.FailInput{ErrorCode: "NETWORK"})
	wantCode(t, err, boostcontract.ErrInvalidTransition)
	var n int64
	e.must(e.db.Model(&boostdomain.PointLedgerEntry{}).Where("task_id = ? AND type IN ?", task.ID, []string{"consume", "release"}).Count(&n).Error)
	if n != 1 {
		t.Fatalf("settlement ledger rows = %d, want 1", n)
	}
	e.checkTaskInvariants()
}

// 日志脱敏：任务事件里的密码、验证码被遮盖
func TestEventsAreRedacted(t *testing.T) {
	e := newTaskEnv(t)
	b := e.verifiedBinding(e.vip.ID, e.starfall)
	task := e.create(e.vip.ID, b.ID, "main_stage", string(sweep([2]interface{}{"1-1", 3})))
	e.start(task.ID)
	_, err := e.tasks.Progress(task.ID, boostapp.ProgressInput{Steps: steps(1), ProgressText: "登录 Secret-Pass-001 成功"})
	e.must(err)
	_, err = e.tasks.Fail(taskExec(task.ID), boostapp.FailInput{ErrorCode: "STAGE_LOCKED", Steps: steps(1), Message: "password: Secret-Pass-001 验证码=778899 账号 " + "player"})
	e.must(err)
	var events []boostdomain.TaskEvent
	e.must(e.db.Where("task_id = ?", task.ID).Find(&events).Error)
	got := e.task(task.ID)
	all := got.ProgressText
	for _, ev := range events {
		all += "\n" + ev.Message
	}
	for _, secret := range []string{"Secret-Pass-001", "778899"} {
		if strings.Contains(all, secret) {
			t.Fatalf("secret %q leaked: %s", secret, all)
		}
	}
	e.checkTaskInvariants()
}

// 用户详情：步骤进度
func TestTaskDetailSteps(t *testing.T) {
	e := newTaskEnv(t)
	b := e.verifiedBinding(e.vip.ID, e.starfall)
	task := e.create(e.vip.ID, b.ID, "main_stage", string(sweep([2]interface{}{"1-1", 3}, [2]interface{}{"1-4", 2})))
	e.start(task.ID)
	_, err := e.tasks.Progress(task.ID, boostapp.ProgressInput{Steps: steps(3, 1)})
	e.must(err)
	d, err := e.tasks.GetTask(e.vip.ID, task.TaskNo)
	e.must(err)
	if len(d.Steps) != 2 || d.Steps[0].Done != 3 || d.Steps[1].Done != 1 || len(d.Events) == 0 || d.Task.ProgressPercent != 80 {
		t.Fatalf("detail = %+v", d)
	}
	_, err = e.tasks.GetTask(e.plain.ID, task.TaskNo)
	wantCode(t, err, boostcontract.ErrNotFound)
}

// 随机压力：随机操作后所有不变量仍成立（原型"600 步随机操作"的缩小版，SQLite 上跑得动）
func TestRandomOperationsKeepInvariants(t *testing.T) {
	e := newTaskEnv(t)
	bindings := []*boostdomain.UserBinding{e.verifiedBinding(e.vip.ID, e.starfall), e.verifiedBinding(e.vip.ID, e.starfall)}
	features := []string{"daily", "abyss", "idle_farm", "main_stage"}
	params := map[string]string{"daily": `{"extras":["dispatch","shop"]}`, "abyss": `{"floors":["f5","f9"]}`, "idle_farm": `{"area":"plain","minutes":30}`, "main_stage": string(sweep([2]interface{}{"1-1", 3}, [2]interface{}{"2-4", 2}))}
	errCodes := boostdomain.ErrorCodes()
	seed := uint32(20261029)
	rnd := func(n int) int {
		seed ^= seed << 13
		seed ^= seed >> 17
		seed ^= seed << 5
		return int(seed % uint32(n))
	}
	var tasks []uint
	for step := 0; step < 150; step++ {
		switch rnd(7) {
		case 0, 1:
			f := features[rnd(len(features))]
			r, err := e.tasks.CreateTask(boostapp.CreateTaskInput{UserID: e.vip.ID, BindingID: bindings[rnd(2)].ID, FeatureID: e.features[f], Params: []byte(params[f])})
			if err == nil {
				tasks = append(tasks, r.Task.ID)
			}
		default:
			if len(tasks) == 0 {
				continue
			}
			id := tasks[rnd(len(tasks))]
			task := e.task(id)
			switch task.Status {
			case boostdomain.TaskStatusPending:
				if rnd(4) == 0 {
					_, _ = e.tasks.CancelTask(e.vip.ID, task.TaskNo)
				} else {
					_, _ = e.tasks.Start(taskExec(id))
				}
			case boostdomain.TaskStatusProcessing:
				plan, _ := boostapp.DecodePlan(task)
				var report []boostapp.StepDone
				for _, st := range plan.Steps { // 随机挑步骤、随机完成数（可能超过计划，测试截断）
					if rnd(2) == 0 {
						report = append(report, boostapp.StepDone{Seq: st.Seq, Done: int64(rnd(int(st.Count) + 3))})
					}
				}
				switch rnd(6) {
				case 0:
					_, _ = e.tasks.Progress(id, boostapp.ProgressInput{Steps: report})
				case 1:
					_, _ = e.tasks.Finish(taskExec(id), boostapp.FinishInput{Steps: report})
				case 2:
					_, _ = e.tasks.Fail(taskExec(id), boostapp.FailInput{ErrorCode: errCodes[rnd(len(errCodes))], Steps: report})
				case 3:
					_, _ = e.tasks.NeedInput(taskExec(id), "", "")
				case 4:
					_, _ = e.tasks.CancelTask(e.vip.ID, task.TaskNo)
				case 5:
					_, _ = e.tasks.CancelAck(id, report)
				}
			case boostdomain.TaskStatusWaitInput:
				if rnd(2) == 0 {
					_ = e.tasks.SubmitInput(e.vip.ID, boostdomain.InputTargetTask, id, "1234")
				} else {
					e.advance(6 * time.Minute)
					_, _ = e.tasks.Reap(50)
				}
			}
		}
		if step%25 == 0 {
			e.checkTaskInvariants()
		}
	}
	e.checkTaskInvariants()
}

// 方案 B：执行端按步骤上报，每一步按自己的完成数计价，不假设执行顺序（用户 2026-10-29 的例子）。
func TestSettlementByStepNotByOrder(t *testing.T) {
	e := newTaskEnv(t)
	e.grant(e.plain.ID, 500, "plain")
	b := e.verifiedBinding(e.plain.ID, e.starfall)
	params := string(sweep([2]interface{}{"1-3", 10}, [2]interface{}{"1-4", 10})) // 断桥 2 分 / 次，BOSS 4 分 / 次
	t1 := e.create(e.plain.ID, b.ID, "main_stage", params)
	if t1.PointsHeld != 60 {
		t.Fatalf("held = %d", t1.PointsHeld)
	}
	// 先刷完 BOSS 10 次，断桥只做了 8 次：20 + 40 - 4 = 56，退 4（按顺序推算会是 52 / 8，扣错）
	e.start(t1.ID)
	_, err := e.tasks.Progress(t1.ID, boostapp.ProgressInput{Steps: []boostapp.StepDone{{Seq: 2, Done: 10}}})
	e.must(err)
	_, err = e.tasks.Fail(taskExec(t1.ID), boostapp.FailInput{ErrorCode: boostdomain.ErrorInsufficientStamina, Steps: []boostapp.StepDone{{Seq: 1, Done: 8}}})
	e.must(err)
	got := e.task(t1.ID)
	if got.PointsConsumed != 56 || got.PointsReleased != 4 || got.UnitsDone != 18 || got.StepsDoneJSON != "[8,10]" {
		t.Fatalf("task = consumed %d released %d units %d steps %s", got.PointsConsumed, got.PointsReleased, got.UnitsDone, got.StepsDoneJSON)
	}
	d, err := e.tasks.GetTask(e.plain.ID, t1.TaskNo)
	e.must(err)
	st := d.Settlement
	if !st.Final || len(st.Lines) != 2 || st.Lines[0].Points != 16 || st.Lines[1].Points != 40 || st.RawPoints != 56 || st.Consumed != 56 || st.Released != 4 || !strings.Contains(st.Rule, "体力不足") {
		t.Fatalf("settlement = %+v", st)
	}

	// 同样完成 18 次，但先做断桥：20 + 32 = 52，退 8
	t2 := e.create(e.plain.ID, b.ID, "main_stage", params)
	e.start(t2.ID)
	_, err = e.tasks.Fail(taskExec(t2.ID), boostapp.FailInput{ErrorCode: boostdomain.ErrorInsufficientStamina, Steps: steps(10, 8)})
	e.must(err)
	if got := e.task(t2.ID); got.PointsConsumed != 52 || got.PointsReleased != 8 {
		t.Fatalf("t2 = %+v", got)
	}
	e.checkTaskInvariants()
}

// 按步骤上报：不存在 / 重复的步骤被拒绝；每一步只增不减、超过计划截断；进行中的结算明细是预估
func TestStepReportValidation(t *testing.T) {
	e := newTaskEnv(t)
	b := e.verifiedBinding(e.vip.ID, e.starfall)
	task := e.create(e.vip.ID, b.ID, "main_stage", string(sweep([2]interface{}{"1-1", 3}, [2]interface{}{"1-4", 2})))
	e.start(task.ID)
	_, err := e.tasks.Progress(task.ID, boostapp.ProgressInput{Steps: []boostapp.StepDone{{Seq: 3, Done: 1}}})
	wantCode(t, err, boostcontract.ErrInvalid)
	_, err = e.tasks.Progress(task.ID, boostapp.ProgressInput{Steps: []boostapp.StepDone{{Seq: 1, Done: 1}, {Seq: 1, Done: 2}}})
	wantCode(t, err, boostcontract.ErrInvalid)
	_, err = e.tasks.Progress(task.ID, boostapp.ProgressInput{Steps: steps(2, 9)})
	e.must(err)
	_, err = e.tasks.Progress(task.ID, boostapp.ProgressInput{Steps: steps(1)}) // 回退无效
	e.must(err)
	got := e.task(task.ID)
	if got.StepsDoneJSON != "[2,2]" || got.UnitsDone != 4 || got.ProgressPercent != 80 {
		t.Fatalf("task = %s units %d pct %d", got.StepsDoneJSON, got.UnitsDone, got.ProgressPercent)
	}
	d, err := e.tasks.GetTask(e.vip.ID, task.TaskNo)
	e.must(err)
	// 扫荡单价 1-1 = 2、1-4 = 4：原价 2×2 + 2×4 = 12，9 折 10；冻结 (3×2 + 2×4) × 0.9 = 12
	if d.Settlement.Final || d.Settlement.RawPoints != 12 || d.Settlement.ByUnits != 10 || d.Settlement.Consumed != 10 || d.Settlement.Released != 2 {
		t.Fatalf("estimate = %+v held %d", d.Settlement, task.PointsHeld)
	}
	item, err := e.tasks.Fail(taskExec(task.ID), boostapp.FailInput{ErrorCode: boostdomain.ErrorNetwork})
	e.must(err)
	if item.Status != boostdomain.TaskStatusPending {
		t.Fatal("network error should requeue")
	}
	started := e.start(task.ID)
	if len(started.Task.StepsDone) != 2 || started.Task.StepsDone[0].Done != 2 || started.Task.StepsDone[1].Done != 2 {
		t.Fatalf("retry should hand over per-step progress: %+v", started.Task.StepsDone)
	}
	e.checkTaskInvariants()
}

// 旧任务（S5 初版只记 units_done、没有 steps_done_json）按步骤顺序推导，结果和原来一致
func TestLegacyTaskWithoutStepsDone(t *testing.T) {
	e := newTaskEnv(t)
	e.grant(e.plain.ID, 500, "plain")
	b := e.verifiedBinding(e.plain.ID, e.starfall)
	task := e.create(e.plain.ID, b.ID, "main_stage", string(sweep([2]interface{}{"1-3", 10}, [2]interface{}{"1-4", 10})))
	e.start(task.ID)
	e.must(e.db.Model(&boostdomain.Task{}).Where("id = ?", task.ID).Updates(map[string]interface{}{"units_done": 18, "steps_done_json": gorm.Expr("NULL")}).Error) // 迁移新增的列，旧行是 NULL
	d, err := e.tasks.GetTask(e.plain.ID, task.TaskNo)
	e.must(err)
	if d.Steps[0].Done != 10 || d.Steps[1].Done != 8 {
		t.Fatalf("legacy steps = %+v", d.Steps)
	}
	_, err = e.tasks.Fail(taskExec(task.ID), boostapp.FailInput{ErrorCode: boostdomain.ErrorInsufficientStamina})
	e.must(err)
	if got := e.task(task.ID); got.PointsConsumed != 52 || got.PointsReleased != 8 {
		t.Fatalf("legacy settle = %+v", got)
	}
}
