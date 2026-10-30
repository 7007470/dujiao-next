package boosthttp

import (
	"encoding/json"
	"errors"
	"net/http"
	"strconv"
	"strings"

	boostapp "github.com/dujiao-next/internal/modules/boost/application"
	boostcontract "github.com/dujiao-next/internal/modules/boost/contract"
	boostdomain "github.com/dujiao-next/internal/modules/boost/domain"
	"github.com/dujiao-next/internal/platform/http/ginutil"
	"github.com/dujiao-next/internal/platform/http/response"

	"github.com/gin-gonic/gin"
)

// TaskHandler 处理游戏账号绑定和代练任务（用户端、后台、开发用模拟执行端共用，路由分开注册）。
type TaskHandler struct {
	tasks     *boostapp.TaskService
	audit     AuditRecorder
	simulator bool // 开发用模拟执行端是否开启（boost.dev_simulator）
}

// NewTaskHandler 创建处理器。audit 可以为 nil（用户端 / 测试）。
func NewTaskHandler(tasks *boostapp.TaskService, audit AuditRecorder, simulator bool) *TaskHandler {
	if tasks == nil {
		panic("boost task handler: tasks is nil")
	}
	return &TaskHandler{tasks: tasks, audit: audit, simulator: simulator}
}

func (h *TaskHandler) record(c *gin.Context, action, object string, detail map[string]interface{}) {
	(&AdminHandler{audit: h.audit}).record(c, action, object, detail)
}

// respondTask 映射绑定 / 任务错误。字段错误放在 data.errors 里，前端据此标红。
func respondTask(c *gin.Context, err error) {
	if ve, ok := boostapp.AsValidationError(err); ok {
		code := response.CodeBadRequest
		if errors.Is(err, boostcontract.ErrNotFound) {
			code = response.CodeNotFound
		}
		c.JSON(http.StatusOK, response.Response{StatusCode: code, Msg: ve.Msg, Data: gin.H{"error": taskErrorCode(err), "errors": ve.Issues}})
		return
	}
	switch {
	case errors.Is(err, boostcontract.ErrPointsInsufficient):
		response.Error(c, response.CodeBadRequest, "可用积分不足")
	case errors.Is(err, boostcontract.ErrSimulatorOff):
		response.Error(c, response.CodeForbidden, "模拟执行端未开启（boost.dev_simulator）")
	default:
		respond(c, err)
	}
}

// taskErrorCode 稳定的业务错误码，前端 / 执行端按它分支，不依赖中文提示。
func taskErrorCode(err error) string {
	for _, pair := range []struct {
		err  error
		code string
	}{
		{boostcontract.ErrParamsInvalid, "PARAMS_INVALID"},
		{boostcontract.ErrInvalidTransition, "INVALID_TRANSITION"},
		{boostcontract.ErrTaskLimit, "TASK_LIMIT_REACHED"},
		{boostcontract.ErrBindingBusy, "BINDING_BUSY"},
		{boostcontract.ErrBindingDuplicate, "DUPLICATE"},
		{boostcontract.ErrInputExpired, "INPUT_EXPIRED"},
		{boostcontract.ErrPointsInsufficient, "POINTS_INSUFFICIENT"},
		{boostcontract.ErrNotFound, "NOT_FOUND"},
	} {
		if errors.Is(err, pair.err) {
			return pair.code
		}
	}
	return "INVALID"
}

// ---------------------------------------------------------------------------
// 用户端：绑定
// ---------------------------------------------------------------------------

type createBindingRequest struct {
	GameID      uint   `json:"game_id" binding:"required"`
	ServerID    uint   `json:"server_id" binding:"required"`
	DisplayName string `json:"display_name"`
	Login       string `json:"login"`
	Password    string `json:"password"`
}

// MyBindings GET /boost/bindings
func (h *TaskHandler) MyBindings(c *gin.Context) {
	userID, ok := ginutil.GetUserID(c)
	if !ok {
		return
	}
	gameID, _ := strconv.ParseUint(c.Query("game_id"), 10, 64)
	page, size := pageQuery(c)
	rows, total, err := h.tasks.ListBindings(boostcontract.BindingFilter{UserID: userID, GameID: uint(gameID), Page: page, PageSize: size})
	if err != nil {
		respondTask(c, err)
		return
	}
	response.SuccessWithPage(c, rows, response.BuildPagination(page, size, total))
}

// CreateBinding POST /boost/bindings
func (h *TaskHandler) CreateBinding(c *gin.Context) {
	userID, ok := ginutil.GetUserID(c)
	if !ok {
		return
	}
	var req createBindingRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		ginutil.RespondBindError(c, err)
		return
	}
	binding, err := h.tasks.CreateBinding(boostapp.CreateBindingInput{UserID: userID, GameID: req.GameID, ServerID: req.ServerID, DisplayName: req.DisplayName, Login: req.Login, Password: req.Password})
	if err != nil {
		respondTask(c, err)
		return
	}
	response.Success(c, binding)
}

// DeleteBinding DELETE /boost/bindings/:id
func (h *TaskHandler) DeleteBinding(c *gin.Context) {
	userID, ok := ginutil.GetUserID(c)
	if !ok {
		return
	}
	id, ok := paramID(c, "id")
	if !ok {
		return
	}
	if err := h.tasks.DeleteBinding(userID, id); err != nil {
		respondTask(c, err)
		return
	}
	response.Success(c, gin.H{"ok": true})
}

type inputRequest struct {
	Value string `json:"value"`
}

// BindingInput POST /boost/bindings/:id/input
func (h *TaskHandler) BindingInput(c *gin.Context) {
	userID, ok := ginutil.GetUserID(c)
	if !ok {
		return
	}
	id, ok := paramID(c, "id")
	if !ok {
		return
	}
	var req inputRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		ginutil.RespondBindError(c, err)
		return
	}
	if err := h.tasks.SubmitInput(userID, boostdomain.InputTargetBinding, id, req.Value); err != nil {
		respondTask(c, err)
		return
	}
	response.Success(c, gin.H{"ok": true})
}

// BindingConfirm POST /boost/bindings/:id/confirm
func (h *TaskHandler) BindingConfirm(c *gin.Context) {
	userID, ok := ginutil.GetUserID(c)
	if !ok {
		return
	}
	id, ok := paramID(c, "id")
	if !ok {
		return
	}
	if err := h.tasks.ConfirmInGame(userID, id); err != nil {
		respondTask(c, err)
		return
	}
	response.Success(c, gin.H{"ok": true})
}

// ---------------------------------------------------------------------------
// 用户端：任务
// ---------------------------------------------------------------------------

type createTaskRequest struct {
	BindingID      uint            `json:"binding_id" binding:"required"`
	FeatureID      uint            `json:"feature_id" binding:"required"`
	Params         json.RawMessage `json:"params"`
	IdempotencyKey string          `json:"idempotency_key"`
}

// CreateTask POST /boost/tasks —— 请求里的任何价格字段都被忽略，服务端重新编译报价。
func (h *TaskHandler) CreateTask(c *gin.Context) {
	userID, ok := ginutil.GetUserID(c)
	if !ok {
		return
	}
	var req createTaskRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		ginutil.RespondBindError(c, err)
		return
	}
	result, err := h.tasks.CreateTask(boostapp.CreateTaskInput{UserID: userID, BindingID: req.BindingID, FeatureID: req.FeatureID, Params: req.Params, IdempotencyKey: req.IdempotencyKey})
	if err != nil {
		respondTask(c, err)
		return
	}
	response.Success(c, result)
}

// MyTasks GET /boost/tasks?status=active|PENDING|...
func (h *TaskHandler) MyTasks(c *gin.Context) {
	userID, ok := ginutil.GetUserID(c)
	if !ok {
		return
	}
	page, size := pageQuery(c)
	rows, total, err := h.tasks.ListTasks(boostcontract.TaskFilter{UserID: userID, Status: strings.TrimSpace(c.Query("status")), Page: page, PageSize: size})
	if err != nil {
		respondTask(c, err)
		return
	}
	response.SuccessWithPage(c, rows, response.BuildPagination(page, size, total))
}

// MyTask GET /boost/tasks/:task_no
func (h *TaskHandler) MyTask(c *gin.Context) {
	userID, ok := ginutil.GetUserID(c)
	if !ok {
		return
	}
	detail, err := h.tasks.GetTask(userID, c.Param("task_no"))
	if err != nil {
		respondTask(c, err)
		return
	}
	response.Success(c, detail)
}

// CancelMyTask POST /boost/tasks/:task_no/cancel
func (h *TaskHandler) CancelMyTask(c *gin.Context) {
	userID, ok := ginutil.GetUserID(c)
	if !ok {
		return
	}
	result, err := h.tasks.CancelTask(userID, c.Param("task_no"))
	if err != nil {
		respondTask(c, err)
		return
	}
	response.Success(c, result)
}

// TaskInput POST /boost/tasks/:task_no/input
func (h *TaskHandler) TaskInput(c *gin.Context) {
	userID, ok := ginutil.GetUserID(c)
	if !ok {
		return
	}
	var req inputRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		ginutil.RespondBindError(c, err)
		return
	}
	if err := h.tasks.SubmitTaskInputByNo(userID, c.Param("task_no"), req.Value); err != nil {
		respondTask(c, err)
		return
	}
	response.Success(c, gin.H{"ok": true})
}

// ---------------------------------------------------------------------------
// 后台
// ---------------------------------------------------------------------------

func queryUint(c *gin.Context, key string) uint {
	v, _ := strconv.ParseUint(strings.TrimSpace(c.Query(key)), 10, 64)
	return uint(v)
}

// AdminTasks GET /admin/boost/tasks
func (h *TaskHandler) AdminTasks(c *gin.Context) {
	page, size := pageQuery(c)
	rows, total, err := h.tasks.ListTasks(boostcontract.TaskFilter{
		UserID: queryUint(c, "user_id"), GameID: queryUint(c, "game_id"),
		Status: strings.TrimSpace(c.Query("status")), TaskNo: strings.TrimSpace(c.Query("task_no")),
		Page: page, PageSize: size,
	})
	if err != nil {
		respondTask(c, err)
		return
	}
	response.SuccessWithPage(c, rows, response.BuildPagination(page, size, total))
}

// AdminTask GET /admin/boost/tasks/:task_no
func (h *TaskHandler) AdminTask(c *gin.Context) {
	detail, err := h.tasks.GetTask(0, c.Param("task_no"))
	if err != nil {
		respondTask(c, err)
		return
	}
	response.Success(c, gin.H{"detail": detail, "simulator": h.simulator})
}

// AdminCancelTask POST /admin/boost/tasks/:task_no/cancel
func (h *TaskHandler) AdminCancelTask(c *gin.Context) {
	taskNo := c.Param("task_no")
	result, err := h.tasks.CancelTask(0, taskNo)
	if err != nil {
		respondTask(c, err)
		return
	}
	h.record(c, "boost_task_cancel", "boost_task:"+taskNo, map[string]interface{}{
		"status": result.Status, "cancel_requested": result.Requested, "points_consumed": result.Consumed, "points_released": result.Released,
	})
	response.Success(c, result)
}

// AdminBindings GET /admin/boost/bindings
func (h *TaskHandler) AdminBindings(c *gin.Context) {
	page, size := pageQuery(c)
	rows, total, err := h.tasks.ListBindings(boostcontract.BindingFilter{
		UserID: queryUint(c, "user_id"), GameID: queryUint(c, "game_id"), Status: strings.TrimSpace(c.Query("status")),
		IncludeDeleted: c.Query("include_deleted") == "1", Page: page, PageSize: size,
	})
	if err != nil {
		respondTask(c, err)
		return
	}
	response.SuccessWithPage(c, rows, response.BuildPagination(page, size, total))
}

// ---------------------------------------------------------------------------
// 开发用模拟执行端（boost.dev_simulator=true 才可用；S6 Worker 接口上线后移除）。
// 调用的是和 S6 Worker 接口同一套状态机，只是不经过签名和租约。凭据明文不返回给后台页面。
// ---------------------------------------------------------------------------

// SimulatorQueue GET /admin/boost/simulator
func (h *TaskHandler) SimulatorQueue(c *gin.Context) {
	out := gin.H{"enabled": h.simulator, "error_codes": boostdomain.ErrorCodes()}
	if !h.simulator {
		response.Success(c, out)
		return
	}
	tasks, err := h.tasks.QueueTasks(100)
	if err != nil {
		respondTask(c, err)
		return
	}
	var bindings []boostapp.BindingView
	for _, status := range []string{boostdomain.BindingStatusPending, boostdomain.BindingStatusProcessing, boostdomain.BindingStatusWaitInput, boostdomain.BindingStatusWaitUser} {
		rows, _, err := h.tasks.ListBindings(boostcontract.BindingFilter{Status: status, PageSize: 100})
		if err != nil {
			respondTask(c, err)
			return
		}
		bindings = append(bindings, rows...)
	}
	out["tasks"] = tasks
	out["bindings"] = bindings
	response.Success(c, out)
}

type simulatorRequest struct {
	Kind   string `json:"kind"` // binding / task
	ID     uint   `json:"id"`
	Action string `json:"action"` // start / progress / need_input / finish / fail / cancel_ack
	// Steps 每一步的完成数 [{seq, done}]（progress / finish / fail / cancel_ack）；finish 不传表示全部完成
	Steps     []boostapp.StepDone `json:"steps"`
	ErrorCode string              `json:"error_code"`
	InputKind string              `json:"input_kind"`
	Prompt    string              `json:"prompt"`
	Message   string              `json:"message"`
	Result    json.RawMessage     `json:"result"`
}

// SimulatorAct POST /admin/boost/simulator —— 推进一个绑定或任务。
func (h *TaskHandler) SimulatorAct(c *gin.Context) {
	if !h.simulator {
		respondTask(c, boostcontract.ErrSimulatorOff)
		return
	}
	var req simulatorRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		ginutil.RespondBindError(c, err)
		return
	}
	if req.Kind != boostdomain.InputTargetBinding && req.Kind != boostdomain.InputTargetTask {
		response.Error(c, response.CodeBadRequest, "kind 必须是 binding 或 task")
		return
	}
	target := boostapp.Exec{Kind: req.Kind, ID: req.ID}
	var (
		result interface{}
		err    error
	)
	switch req.Action {
	case "start":
		var item *boostapp.StartedItem
		item, err = h.tasks.Start(target)
		if item != nil {
			// 只回显不敏感的部分：页面上看得到执行端收到了什么计划，看不到凭据和验证码
			view := gin.H{"kind": item.Kind, "id": item.ID, "attempt": item.Attempt, "game": item.GameCode, "has_session": item.SessionRef != ""}
			if item.Input != nil {
				view["input_kind"] = item.Input.Kind
				view["input_received"] = item.Input.Value != ""
			}
			if item.Task != nil {
				view["task"] = item.Task
			}
			result = view
		}
	case "progress":
		if req.Kind != boostdomain.InputTargetTask {
			response.Error(c, response.CodeBadRequest, "只有任务能上报进度")
			return
		}
		result, err = h.tasks.Progress(req.ID, boostapp.ProgressInput{Steps: req.Steps, ProgressText: req.Message})
	case "need_input":
		result, err = h.tasks.NeedInput(target, req.InputKind, req.Prompt)
	case "finish":
		result, err = h.tasks.Finish(target, boostapp.FinishInput{Steps: req.Steps, ResultJSON: req.Result, SessionRef: "sim-session"})
	case "fail":
		result, err = h.tasks.Fail(target, boostapp.FailInput{ErrorCode: req.ErrorCode, Steps: req.Steps, Message: req.Message})
	case "cancel_ack":
		if req.Kind != boostdomain.InputTargetTask {
			response.Error(c, response.CodeBadRequest, "只有任务能确认取消")
			return
		}
		result, err = h.tasks.CancelAck(req.ID, req.Steps)
	default:
		response.Error(c, response.CodeBadRequest, "未知的 action")
		return
	}
	if err != nil {
		respondTask(c, err)
		return
	}
	h.record(c, "boost_simulator_"+req.Action, "boost_"+req.Kind+":"+strconv.FormatUint(uint64(req.ID), 10), map[string]interface{}{
		"steps": req.Steps, "error_code": req.ErrorCode, "input_kind": req.InputKind,
	})
	response.Success(c, result)
}

// SimulatorReap POST /admin/boost/simulator/reap —— 立即跑一次回收（输入超时、任务超时）。
func (h *TaskHandler) SimulatorReap(c *gin.Context) {
	if !h.simulator {
		respondTask(c, boostcontract.ErrSimulatorOff)
		return
	}
	n, err := h.tasks.Reap(200)
	if err != nil {
		respondTask(c, err)
		return
	}
	response.Success(c, gin.H{"changed": n})
}
