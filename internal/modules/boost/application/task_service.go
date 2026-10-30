package application

import (
	"bytes"
	"crypto/rand"
	"encoding/json"
	"errors"
	"fmt"
	"math/big"
	"strconv"
	"strings"
	"time"

	boostcontract "github.com/dujiao-next/internal/modules/boost/contract"
	boostdomain "github.com/dujiao-next/internal/modules/boost/domain"
	"github.com/dujiao-next/internal/modules/boost/domain/featuredef"
)

// TaskService 用户游戏账号绑定、代练任务创建 / 取消 / 结算，规则与原型 boost.js §6 一致：
// 任务必须关联一个验证成功的本人绑定和一个已上架功能；创建任务与冻结积分在同一事务；
// 任务保存功能版本、快照和编译后的执行计划；扣多少积分由服务端按快照计算，完成量超过计划时截断；
// 结算后 消耗 + 退回 = 冻结。执行端（Worker）那一侧的入口在 task_executor.go，S6 的 Worker 接口直接调用。
type TaskService struct {
	repo   boostcontract.TaskRepository
	points *PointsService
	rates  DiscountRateResolver
	sealer boostcontract.CredentialSealer // nil = 未配置凭据密钥，不能绑定账号
	now    func() time.Time

	InputExpire     time.Duration // 等待用户输入的时限
	BindDailyLimit  int64         // 每个用户每天最多提交的绑定数
	BindMaxAttempts int
	TaskMaxAttempts int
}

// NewTaskService 创建绑定 / 任务用例。sealer 为 nil 时账号绑定不可用（其余功能正常）。
func NewTaskService(repo boostcontract.TaskRepository, points *PointsService, rates DiscountRateResolver, sealer boostcontract.CredentialSealer) *TaskService {
	if repo == nil || points == nil {
		panic("boost task service: repo and points are required")
	}
	return &TaskService{
		repo: repo, points: points, rates: rates, sealer: sealer, now: time.Now,
		InputExpire: 5 * time.Minute, BindDailyLimit: 10, BindMaxAttempts: 3, TaskMaxAttempts: 2,
	}
}

// SetClock 替换时钟（测试用）。
func (s *TaskService) SetClock(now func() time.Time) {
	if now != nil {
		s.now = now
	}
}

// BindingEnabled 是否配置了凭据密钥。
func (s *TaskService) BindingEnabled() bool { return s.sealer != nil }

// ---------------------------------------------------------------------------
// 快照
// ---------------------------------------------------------------------------

// TaskSnapshot 任务创建时的功能快照（snapshot_json）。
type TaskSnapshot struct {
	Code             string `json:"code"`
	Name             string `json:"name"`
	Version          int    `json:"version"`
	Capability       string `json:"capability"`
	RefundPolicy     string `json:"refund_policy"`
	TimeoutMinutes   int    `json:"timeout_minutes"`
	MaxActivePerUser int    `json:"max_active_per_user"`
	Rate             int    `json:"rate"`
}

// PlanStep 执行计划里的一步（plan_json.steps 的元素）。Ref 原样保存，执行端据此定位关卡。
type PlanStep struct {
	Seq        int             `json:"seq"`
	Label      string          `json:"label"`
	Count      int64           `json:"count"`
	UnitPoints int64           `json:"unitPoints"`
	UnitLabel  string          `json:"unitLabel"`
	Ref        json.RawMessage `json:"ref"`
}

// PlanSnapshot 编译后的执行计划（plan_json）。键名与原型一致。
type PlanSnapshot struct {
	Steps       []PlanStep                 `json:"steps"`
	Fixed       []featuredef.FixedCharge   `json:"fixed"`
	Units       int64                      `json:"units"`
	FixedPoints int64                      `json:"fixedPoints"`
	RawTotal    int64                      `json:"rawTotal"`
	Total       int64                      `json:"total"`
	Rate        int                        `json:"rate"`
	Breakdown   []featuredef.BreakdownLine `json:"breakdown"`
}

func snapshotPlan(plan featuredef.Plan) (PlanSnapshot, error) {
	out := PlanSnapshot{Fixed: plan.Fixed, Units: plan.Units, FixedPoints: plan.FixedPoints, RawTotal: plan.RawTotal, Total: plan.Total, Rate: plan.Rate, Breakdown: plan.Breakdown}
	raw, err := json.Marshal(plan.Steps)
	if err != nil {
		return out, err
	}
	if err := json.Unmarshal(raw, &out.Steps); err != nil {
		return out, err
	}
	if out.Steps == nil {
		out.Steps = []PlanStep{}
	}
	return out, nil
}

// compilePlan 还原成 featuredef.Plan，只用于结算计算（PointsForSteps）。
func (p PlanSnapshot) compilePlan() featuredef.Plan {
	steps := make([]featuredef.Step, len(p.Steps))
	for i, st := range p.Steps {
		steps[i] = featuredef.Step{Label: st.Label, Count: st.Count, UnitPoints: st.UnitPoints, UnitLabel: st.UnitLabel, Seq: st.Seq}
	}
	return featuredef.Plan{Steps: steps, FixedPoints: p.FixedPoints, Units: p.Units, Total: p.Total, Rate: p.Rate}
}

// DecodePlan 解析任务的执行计划快照。
func DecodePlan(task *boostdomain.Task) (PlanSnapshot, error) {
	var p PlanSnapshot
	err := json.Unmarshal([]byte(task.PlanJSON), &p)
	return p, err
}

// DecodeSnapshot 解析任务的功能快照。
func DecodeSnapshot(task *boostdomain.Task) (TaskSnapshot, error) {
	var s TaskSnapshot
	err := json.Unmarshal([]byte(task.SnapshotJSON), &s)
	return s, err
}

func strOf(v featuredef.Value) string {
	s, _ := v.Str()
	return s
}

func intOf(v featuredef.Value) int {
	n, _ := v.Num()
	return int(n)
}

// planSummary 对应原型 planSummary：按字段顺序拼一句话摘要，例如"扫荡 · 1-1 迷雾入口 ×5、1-2 旧哨塔 ×5"。
func planSummary(def featuredef.Value, plan featuredef.Plan) string {
	var parts []string
	optionLabel := func(f, v featuredef.Value) string {
		for _, o := range f.Get("options").Items() {
			if featuredef.StrictEqual(o.Get("value"), v) {
				return strOf(o.Get("label"))
			}
		}
		return ""
	}
	for _, f := range def.Get("fields").Items() {
		key := strOf(f.Get("key"))
		if vis := plan.Visible.Get(key); !vis.Truthy() {
			continue
		}
		v := plan.Values.Get(key)
		if !v.IsDefined() || featuredef.StrictEqual(v, featuredef.StringValue("")) || (v.IsArray() && len(v.Items()) == 0) {
			continue
		}
		label := strOf(f.Get("label"))
		switch strOf(f.Get("type")) {
		case "radio", "select":
			parts = append(parts, optionLabel(f, v))
		case "switch":
			if v.Truthy() {
				parts = append(parts, label)
			}
		case "checkbox":
			var names []string
			for _, x := range v.Items() {
				names = append(names, optionLabel(f, x))
			}
			parts = append(parts, strings.Join(names, "、"))
		case "number":
			n, _ := v.Num()
			parts = append(parts, label+" "+strconv.FormatFloat(n, 'f', -1, 64)+strOf(f.Get("unit")))
		case "stage_picker":
			cat := def.Get("catalogs").Get(strOf(f.Get("catalog")))
			names := map[string]string{}
			for _, g := range cat.Get("groups").Items() {
				for _, it := range g.Get("items").Items() {
					names[strOf(it.Get("code"))] = strOf(it.Get("name"))
				}
			}
			var items []string
			for _, e := range v.Items() {
				name := names[strOf(e.Get("item"))]
				if c, _ := e.Get("count").Num(); c > 1 {
					name += " ×" + strconv.FormatFloat(c, 'f', -1, 64)
				}
				items = append(items, name)
			}
			if len(items) > 3 {
				parts = append(parts, fmt.Sprintf("%s 等 %d 个%s", strings.Join(items[:3], "、"), len(items), strOf(cat.Get("label"))))
			} else {
				parts = append(parts, strings.Join(items, "、"))
			}
		}
	}
	var kept []string
	for _, p := range parts {
		if p != "" {
			kept = append(kept, p)
		}
	}
	return truncateRunes(strings.Join(kept, " · "), 250)
}

// ---------------------------------------------------------------------------
// 创建任务
// ---------------------------------------------------------------------------

// CreateTaskInput 用户下单。前端传来的价格一律不用，只用 Params 重新编译。
type CreateTaskInput struct {
	UserID         uint
	BindingID      uint
	FeatureID      uint
	Params         []byte
	IdempotencyKey string // 同一用户同一键只建一个任务；为空时服务端生成（不去重）
}

// CreateTaskResult 下单结果。Duplicated = 幂等键命中，返回的是原任务。
type CreateTaskResult struct {
	Task       *boostdomain.Task `json:"task"`
	Duplicated bool              `json:"duplicated"`
}

func randomDigits(n int) string {
	var b strings.Builder
	for i := 0; i < n; i++ {
		d, err := rand.Int(rand.Reader, big.NewInt(10))
		if err != nil {
			panic(err)
		}
		b.WriteByte(byte('0' + d.Int64()))
	}
	return b.String()
}

// newTaskNo BT + 北京时间 yyMMdd + 8 位随机数（唯一索引兜底）。
func (s *TaskService) newTaskNo() string {
	return "BT" + s.now().In(beijing).Format("060102") + randomDigits(8)
}

var beijing = time.FixedZone("Asia/Shanghai", 8*3600)

// CreateTask 编译报价 → 检查绑定、功能、并发上限 → 同一事务里建任务并按 FIFO 冻结积分。
// 参数错误不冻结（ErrParamsInvalid，Issues 里是字段错误）；幂等键重复返回原任务。
func (s *TaskService) CreateTask(in CreateTaskInput) (*CreateTaskResult, error) {
	key := strings.TrimSpace(in.IdempotencyKey)
	if len(key) > 64 {
		return nil, invalid(boostcontract.ErrInvalid, "幂等键最多 64 个字符")
	}
	if key != "" {
		existing, err := s.repo.GetTaskByIdempotency(in.UserID, key)
		if err != nil || existing != nil {
			return &CreateTaskResult{Task: existing, Duplicated: existing != nil}, err
		}
	} else {
		key = "auto:" + randomDigits(20)
	}

	binding, err := s.repo.GetBinding(in.BindingID)
	if err != nil {
		return nil, err
	}
	if binding == nil || binding.UserID != in.UserID || binding.DeletedAt != nil {
		return nil, invalid(boostcontract.ErrNotFound, "只能使用自己的游戏账号")
	}
	if binding.Status != boostdomain.BindingStatusSuccess {
		return nil, invalid(boostcontract.ErrInvalidTransition, "游戏账号还没有验证成功")
	}
	catalog := s.repo.Catalog()
	feature, err := catalog.GetFeature(in.FeatureID)
	if err != nil {
		return nil, err
	}
	if feature == nil || feature.Status != boostdomain.FeatureStatusActive {
		return nil, invalid(boostcontract.ErrNotFound, "代练功能不存在或已下架")
	}
	game, err := catalog.GetGame(feature.GameID)
	if err != nil {
		return nil, err
	}
	if game == nil || game.Status != boostdomain.GameStatusActive {
		return nil, invalid(boostcontract.ErrNotFound, "游戏未上线")
	}
	if feature.GameID != binding.GameID {
		return nil, invalid(boostcontract.ErrParamsInvalid, "功能和游戏账号不属于同一个游戏")
	}

	def, err := featuredef.ParseValue([]byte(feature.DefinitionJSON))
	if err != nil {
		return nil, err
	}
	params := featuredef.Value{}
	if len(bytes.TrimSpace(in.Params)) > 0 {
		parsed, err := featuredef.ParseValue(in.Params)
		if err != nil {
			return nil, invalid(boostcontract.ErrParamsInvalid, "参数不是合法的 JSON")
		}
		params = parsed
	}
	rate := 100
	if s.rates != nil {
		rate = s.rates.DiscountRate(in.UserID)
	}
	plan := featuredef.Compile(def, params, rate)
	if !plan.OK {
		ve := &ValidationError{Kind: boostcontract.ErrParamsInvalid, Msg: "参数不合法"}
		if len(plan.Errors) > 0 {
			ve.Msg = plan.Errors[0]
		}
		for _, k := range plan.FieldErrors.Keys() {
			ve.Issues = append(ve.Issues, featuredef.Issue{Path: k, Msg: strOf(plan.FieldErrors.Get(k))})
		}
		return nil, ve
	}

	norm := featuredef.Normalize(def)
	snap := TaskSnapshot{
		Code: strOf(norm.Get("code")), Name: strOf(norm.Get("name")), Version: feature.Version,
		Capability: strOf(norm.Get("capability")), RefundPolicy: strOf(norm.Get("refund_policy")),
		TimeoutMinutes: intOf(norm.Get("timeout_minutes")), MaxActivePerUser: intOf(norm.Get("max_active_per_user")), Rate: plan.Rate,
	}
	planSnap, err := snapshotPlan(plan)
	if err != nil {
		return nil, err
	}
	snapJSON, _ := json.Marshal(snap)
	planJSON, _ := json.Marshal(planSnap)
	paramsJSON, err := plan.Values.MarshalJSON()
	if err != nil {
		return nil, err
	}

	now := s.now()
	deadline := now.Add(time.Duration(snap.TimeoutMinutes) * time.Minute)
	task := &boostdomain.Task{
		TaskNo:         s.newTaskNo(),
		UserID:         in.UserID,
		BindingID:      binding.ID,
		GameID:         binding.GameID,
		FeatureID:      feature.ID,
		FeatureVersion: feature.Version,
		SnapshotJSON:   string(snapJSON),
		ParamsJSON:     string(paramsJSON),
		PlanJSON:       string(planJSON),
		Summary:        planSummary(norm, plan),
		Units:          int(plan.Units),
		PointsHeld:     plan.Total,
		Status:         boostdomain.TaskStatusPending,
		ProgressText:   "排队中",
		MaxAttempts:    s.TaskMaxAttempts,
		AvailableAt:    now,
		DeadlineAt:     &deadline,
		IdempotencyKey: key,
		CreatedAt:      now,
		UpdatedAt:      now,
	}
	err = s.repo.WithinTransaction(func(repo boostcontract.TaskRepository) error {
		// 先锁积分账户：同一用户的下单在数据库层串行，并发上限检查才可靠
		if _, err := repo.Points().LockAccount(in.UserID); err != nil {
			return err
		}
		active, err := repo.CountActiveTasks(in.UserID, feature.ID)
		if err != nil {
			return err
		}
		if snap.MaxActivePerUser > 0 && active >= int64(snap.MaxActivePerUser) {
			return invalid(boostcontract.ErrTaskLimit, "「%s」同时进行的任务最多 %d 个", snap.Name, snap.MaxActivePerUser)
		}
		if err := repo.CreateTask(task); err != nil {
			return err
		}
		if _, err := s.points.HoldPoints(repo.Points(), in.UserID, plan.Total, task.ID); err != nil {
			return err
		}
		return s.addEvent(repo, task, nil, boostdomain.EventTypeState, fmt.Sprintf("已创建，冻结 %d 积分（%d 个执行单位）", plan.Total, plan.Units))
	})
	if errors.Is(err, boostcontract.ErrUniqueConflict) && in.IdempotencyKey != "" {
		// 同一幂等键的并发请求：另一个已经建好了
		existing, getErr := s.repo.GetTaskByIdempotency(in.UserID, key)
		if getErr == nil && existing != nil {
			return &CreateTaskResult{Task: existing, Duplicated: true}, nil
		}
	}
	if err != nil {
		return nil, err
	}
	return &CreateTaskResult{Task: task}, nil
}

// ---------------------------------------------------------------------------
// 查询
// ---------------------------------------------------------------------------

// ListTasks 任务列表（UserID 为 0 表示后台全部）。
func (s *TaskService) ListTasks(filter boostcontract.TaskFilter) ([]boostcontract.TaskRow, int64, error) {
	return s.repo.ListTasks(filter)
}

// StepView 步骤进度。
type StepView struct {
	Seq        int    `json:"seq"`
	Label      string `json:"label"`
	Count      int64  `json:"count"`
	Done       int64  `json:"done"`
	UnitPoints int64  `json:"unit_points"`
}

// InputView 等待用户提交的输入（不含值）。
type InputView struct {
	ID        uint      `json:"id"`
	Kind      string    `json:"kind"`
	Prompt    string    `json:"prompt"`
	Status    string    `json:"status"`
	ExpiresAt time.Time `json:"expires_at"`
}

func presentInput(in *boostdomain.Input) *InputView {
	if in == nil {
		return nil
	}
	return &InputView{ID: in.ID, Kind: in.Kind, Prompt: in.Prompt, Status: in.Status, ExpiresAt: in.ExpiresAt}
}

// TaskDetail 任务详情：任务、步骤进度、最近事件、等待中的输入。
type TaskDetail struct {
	Task       boostcontract.TaskRow      `json:"task"`
	Snapshot   TaskSnapshot               `json:"snapshot"`
	Steps      []StepView                 `json:"steps"`
	Breakdown  []featuredef.BreakdownLine `json:"breakdown"`
	Settlement Settlement                 `json:"settlement"`
	Events     []boostdomain.TaskEvent    `json:"events"`
	Input      *InputView                 `json:"input"`
}

// GetTask 按任务号返回详情。userID 不为 0 时只能看自己的任务（别人的返回 ErrNotFound）。
func (s *TaskService) GetTask(userID uint, taskNo string) (*TaskDetail, error) {
	rows, _, err := s.repo.ListTasks(boostcontract.TaskFilter{UserID: userID, TaskNo: taskNo, PageSize: 1})
	if err != nil {
		return nil, err
	}
	if len(rows) == 0 || strings.TrimSpace(taskNo) == "" {
		return nil, boostcontract.ErrNotFound
	}
	row := rows[0]
	plan, err := DecodePlan(&row.Task)
	if err != nil {
		return nil, err
	}
	snap, _ := DecodeSnapshot(&row.Task)
	steps := make([]StepView, len(plan.Steps))
	done := stepsDone(&row.Task, plan)
	for i, st := range plan.Steps {
		steps[i] = StepView{Seq: st.Seq, Label: st.Label, Count: st.Count, Done: done[i], UnitPoints: st.UnitPoints}
	}
	events, err := s.repo.ListEvents(row.ID, 50)
	if err != nil {
		return nil, err
	}
	input, err := s.repo.OpenInput(boostdomain.InputTargetTask, row.ID)
	if err != nil {
		return nil, err
	}
	if input != nil && input.Status != boostdomain.InputStatusWaiting {
		input = nil
	}
	return &TaskDetail{Task: row, Snapshot: snap, Steps: steps, Breakdown: plan.Breakdown, Settlement: buildSettlement(&row.Task, plan, snap), Events: events, Input: presentInput(input)}, nil
}

// ---------------------------------------------------------------------------
// 取消
// ---------------------------------------------------------------------------

// CancelResult 取消结果。Requested = 执行中，已记下取消请求，等执行端确认后按完成量结算。
type CancelResult struct {
	Status    string `json:"status"`
	Requested bool   `json:"cancel_requested"`
	Consumed  int64  `json:"points_consumed"`
	Released  int64  `json:"points_released"`
}

// CancelTask 排队中 / 等待输入：立即取消并结算（没执行过就全额退回）；执行中：记下取消请求，等执行端确认。
// userID 为 0 表示管理员操作。
func (s *TaskService) CancelTask(userID uint, taskNo string) (*CancelResult, error) {
	found, err := s.repo.GetTaskByNo(strings.TrimSpace(taskNo))
	if err != nil {
		return nil, err
	}
	if found == nil || (userID != 0 && found.UserID != userID) {
		return nil, boostcontract.ErrNotFound
	}
	by := "用户"
	if userID == 0 {
		by = "管理员"
	}
	var out *CancelResult
	err = s.repo.WithinTransaction(func(repo boostcontract.TaskRepository) error {
		task, err := repo.LockTask(found.ID)
		if err != nil {
			return err
		}
		if boostdomain.IsTerminalTask(task.Status) {
			return invalid(boostcontract.ErrInvalidTransition, "任务已结束")
		}
		if task.Status == boostdomain.TaskStatusProcessing {
			if !task.CancelRequested {
				task.CancelRequested = true
				if err := repo.SaveTask(task); err != nil {
					return err
				}
				if err := s.addEvent(repo, task, nil, boostdomain.EventTypeState, by+"请求取消，等待执行端确认"); err != nil {
					return err
				}
			}
			out = &CancelResult{Status: task.Status, Requested: true}
			return nil
		}
		if err := s.finishTerminal(repo, task, boostdomain.TaskStatusCancelled, "", by+"取消"); err != nil {
			return err
		}
		out = &CancelResult{Status: task.Status, Consumed: task.PointsConsumed, Released: task.PointsReleased}
		return nil
	})
	return out, err
}
