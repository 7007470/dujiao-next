package application

import (
	"encoding/json"
	"fmt"
	"regexp"
	"strings"
	"time"
	"unicode/utf8"

	boostcontract "github.com/dujiao-next/internal/modules/boost/contract"
	boostdomain "github.com/dujiao-next/internal/modules/boost/domain"
	"github.com/dujiao-next/internal/modules/boost/domain/featuredef"
)

// 执行端一侧的状态机和结算（原型 boost.js §7 workerPull / heartbeat / need-input / finish / fail / cancel-ack / reap）。
// S5 先由后台"模拟执行端"调用；S6 的 Worker 接口在这些函数外面加签名认证、租约校验和 request_id 去重，
// 不另写一份状态机。这里不处理租约：调用方保证同一目标同一时刻只有一个执行者。

// Exec 执行目标。
type Exec struct {
	Kind string // boostdomain.InputTargetBinding / InputTargetTask
	ID   uint
}

// StartedItem 执行端领取到的内容。Credential / Input 是明文，只能交给执行端，不能写日志或返回给后台页面。
type StartedItem struct {
	Kind       string             `json:"kind"`
	ID         uint               `json:"id"`
	Attempt    int                `json:"attempt"`
	GameCode   string             `json:"game"`
	Credential Credential         `json:"credential"`
	SessionRef string             `json:"session_ref,omitempty"`
	Input      *StartedInput      `json:"input,omitempty"`
	Task       *StartedTaskDetail `json:"task,omitempty"`
}

// StartedInput 用户提交的输入（消费后数据库里的密文立即清空）。
type StartedInput struct {
	Kind  string `json:"kind"`
	Value string `json:"value"`
}

// StartedTaskDetail 任务的执行计划。
type StartedTaskDetail struct {
	TaskNo         string          `json:"task_no"`
	Feature        string          `json:"feature"`
	Capability     string          `json:"capability"`
	FeatureVersion int             `json:"feature_version"`
	Params         json.RawMessage `json:"params"`
	Steps          []PlanStep      `json:"steps"`
	Units          int             `json:"units"`
	UnitsDone      int             `json:"units_done"`
	StepsDone      []StepDone      `json:"steps_done"` // 重试时执行端据此跳过已完成的部分
}

// ExecResult 执行端写操作的结果。
type ExecResult struct {
	Status          string     `json:"status"`
	CancelRequested bool       `json:"cancel_requested,omitempty"`
	RetryAt         *time.Time `json:"retry_at,omitempty"`
	PointsConsumed  int64      `json:"points_consumed"`
	PointsReleased  int64      `json:"points_released"`
	InputID         uint       `json:"input_id,omitempty"`
	InputExpiresAt  *time.Time `json:"input_expires_at,omitempty"`
}

// ---------------------------------------------------------------------------
// 脱敏和事件
// ---------------------------------------------------------------------------

var sensitiveKV = regexp.MustCompile(`(?i)(password|passwd|pwd|token|code|验证码|密码)\s*[:=：]\s*\S+`)

// redactFor 去掉消息里该绑定的登录名 / 密码原文，以及常见 "密码: xxx" 形式；截断到 500 字。
func (s *TaskService) redactFor(binding *boostdomain.UserBinding, message string) string {
	msg := truncateRunes(message, 500)
	if binding != nil && binding.CredentialCipher != nil && s.sealer != nil {
		if plain, err := s.sealer.Open(*binding.CredentialCipher); err == nil {
			var cred Credential
			if json.Unmarshal([]byte(plain), &cred) == nil {
				for _, v := range []string{cred.Password, cred.Login} {
					if utf8.RuneCountInString(v) >= 3 {
						msg = strings.ReplaceAll(msg, v, "***")
					}
				}
			}
		}
	}
	return sensitiveKV.ReplaceAllString(msg, "$1=***")
}

// addEvent 写一条任务事件（消息先脱敏）。seq 由服务端顺序分配。
func (s *TaskService) addEvent(repo boostcontract.TaskRepository, task *boostdomain.Task, binding *boostdomain.UserBinding, typ, message string) error {
	if binding == nil && task.BindingID != 0 {
		b, err := repo.GetBinding(task.BindingID)
		if err != nil {
			return err
		}
		binding = b
	}
	seq, err := repo.MaxEventSeq(task.ID)
	if err != nil {
		return err
	}
	return repo.CreateEvent(&boostdomain.TaskEvent{TaskID: task.ID, Seq: seq + 1, Type: typ, Message: s.redactFor(binding, message), CreatedAt: s.now()})
}

// ---------------------------------------------------------------------------
// 结算
// ---------------------------------------------------------------------------

// consumedFor 成功 / 取消按每一步的完成数计价（PointsForSteps）；失败按 refund_policy 和错误码分类。
func consumedFor(task *boostdomain.Task, plan PlanSnapshot, snap TaskSnapshot, status, errorCode string) int64 {
	byUnits := featuredef.PointsForSteps(stepsPlan(plan), stepsDone(task, plan))
	if status == boostdomain.TaskStatusSuccess || status == boostdomain.TaskStatusCancelled {
		return byUnits
	}
	info, known := boostdomain.LookupErrorCode(errorCode)
	switch snap.RefundPolicy {
	case boostdomain.RefundNone:
		return task.PointsHeld
	case boostdomain.RefundFullOnSystemFail:
		if known && info.System {
			return 0
		}
	}
	return byUnits
}

// finishTerminal 任务进入终态并结算积分（同一事务）。消耗 + 退回 = 冻结；只结算一次（账本 biz_key 保证）。
func (s *TaskService) finishTerminal(repo boostcontract.TaskRepository, task *boostdomain.Task, status, errorCode, reason string) error {
	plan, err := DecodePlan(task)
	if err != nil {
		return err
	}
	snap, err := DecodeSnapshot(task)
	if err != nil {
		return err
	}
	now := s.now()
	task.Status = status
	task.ErrorCode = errorCode
	task.FinishedAt = &now
	task.LeaseToken = nil
	task.LeaseExpiresAt = nil
	task.CancelRequested = false
	task.ProgressText = truncateRunes(reason, 120)
	if err := s.expireOpenInput(repo, boostdomain.InputTargetTask, task.ID); err != nil {
		return err
	}
	consumed := consumedFor(task, plan, snap, status, errorCode)
	settled, err := s.points.SettleTaskPoints(repo.Points(), task.UserID, task.ID, task.PointsHeld, consumed, truncateRunes(task.TaskNo+" "+reason, 100))
	if err != nil {
		return err
	}
	if !settled.Already {
		task.PointsConsumed = settled.Consumed
		task.PointsReleased = settled.Released
	}
	if err := repo.SaveTask(task); err != nil {
		return err
	}
	return s.addEvent(repo, task, nil, boostdomain.EventTypeState,
		fmt.Sprintf("%s：%s。消耗 %d 点，退回 %d 点", taskStatusLabel(status), reason, task.PointsConsumed, task.PointsReleased))
}

func taskStatusLabel(status string) string {
	switch status {
	case boostdomain.TaskStatusSuccess:
		return "已完成"
	case boostdomain.TaskStatusFail:
		return "失败"
	case boostdomain.TaskStatusCancelled:
		return "已取消"
	}
	return status
}

func retryBackoff(attempt int) time.Duration {
	if attempt < 1 {
		attempt = 1
	}
	if attempt > 6 {
		attempt = 6
	}
	return 30 * time.Second * time.Duration(1<<(attempt-1))
}

// ---------------------------------------------------------------------------
// 领取
// ---------------------------------------------------------------------------

func (s *TaskService) openCredential(binding *boostdomain.UserBinding) (Credential, error) {
	var cred Credential
	if binding.CredentialCipher == nil || s.sealer == nil {
		return cred, invalid(boostcontract.ErrInvalidTransition, "绑定的凭据已销毁")
	}
	plain, err := s.sealer.Open(*binding.CredentialCipher)
	if err != nil {
		return cred, err
	}
	err = json.Unmarshal([]byte(plain), &cred)
	return cred, err
}

// consumeInput 取出已提交的输入并立即清空密文。
func (s *TaskService) consumeInput(repo boostcontract.TaskRepository, target string, targetID uint) (*StartedInput, error) {
	input, err := repo.OpenInput(target, targetID)
	if err != nil || input == nil || input.Status != boostdomain.InputStatusSubmitted {
		return nil, err
	}
	out := &StartedInput{Kind: input.Kind}
	if input.ValueCipher != nil {
		value, err := s.sealer.Open(*input.ValueCipher)
		if err != nil {
			return nil, err
		}
		out.Value = value
	}
	input.Status = boostdomain.InputStatusConsumed
	input.ValueCipher = nil
	return out, repo.SaveInput(input)
}

// Start PENDING → PROCESSING，attempt + 1，返回执行需要的凭据、输入和计划。
// 任务：同一绑定已有执行中任务时返回 ErrBindingBusy（部分唯一索引兜底）；绑定已失效时任务直接失败退款。
func (s *TaskService) Start(target Exec) (*StartedItem, error) {
	var out *StartedItem
	err := s.repo.WithinTransaction(func(repo boostcontract.TaskRepository) error {
		now := s.now()
		switch target.Kind {
		case boostdomain.InputTargetBinding:
			b, err := repo.LockBinding(target.ID)
			if err != nil {
				return err
			}
			if b == nil || b.DeletedAt != nil {
				return boostcontract.ErrNotFound
			}
			if b.Status != boostdomain.BindingStatusPending {
				return invalid(boostcontract.ErrInvalidTransition, "绑定当前不是排队状态")
			}
			cred, err := s.openCredential(b)
			if err != nil {
				return err
			}
			game, err := repo.Catalog().GetGame(b.GameID)
			if err != nil {
				return err
			}
			input, err := s.consumeInput(repo, boostdomain.InputTargetBinding, b.ID)
			if err != nil {
				return err
			}
			b.Status = boostdomain.BindingStatusProcessing
			b.Attempt++
			b.UserMessage = "正在登录验证"
			if err := repo.SaveBinding(b); err != nil {
				return err
			}
			out = &StartedItem{Kind: target.Kind, ID: b.ID, Attempt: b.Attempt, Credential: cred, Input: input}
			if game != nil {
				out.GameCode = game.Code
			}
			if b.SessionRefCipher != nil {
				out.SessionRef, _ = s.sealer.Open(*b.SessionRefCipher)
			}
			return nil

		case boostdomain.InputTargetTask:
			t, err := repo.LockTask(target.ID)
			if err != nil {
				return err
			}
			if t == nil {
				return boostcontract.ErrNotFound
			}
			if t.Status != boostdomain.TaskStatusPending {
				return invalid(boostcontract.ErrInvalidTransition, "任务当前不是排队状态")
			}
			b, err := repo.LockBinding(t.BindingID)
			if err != nil {
				return err
			}
			if b == nil || b.DeletedAt != nil || b.Status != boostdomain.BindingStatusSuccess {
				// 账号在排队期间失效（密码修改、被封）：系统原因失败
				return s.finishTerminal(repo, t, boostdomain.TaskStatusFail, boostdomain.ErrorLoginFailed, "游戏账号已失效")
			}
			busy, err := repo.CountProcessingTasksForBinding(b.ID)
			if err != nil {
				return err
			}
			if busy > 0 {
				return invalid(boostcontract.ErrBindingBusy, "该游戏账号有任务正在执行")
			}
			cred, err := s.openCredential(b)
			if err != nil {
				return err
			}
			input, err := s.consumeInput(repo, boostdomain.InputTargetTask, t.ID)
			if err != nil {
				return err
			}
			plan, err := DecodePlan(t)
			if err != nil {
				return err
			}
			snap, _ := DecodeSnapshot(t)
			game, err := repo.Catalog().GetGame(t.GameID)
			if err != nil {
				return err
			}
			t.Status = boostdomain.TaskStatusProcessing
			t.Attempt++
			if t.StartedAt == nil {
				t.StartedAt = &now
			}
			t.ProgressText = "执行中"
			if err := repo.SaveTask(t); err != nil {
				return err
			}
			if err := s.addEvent(repo, t, b, boostdomain.EventTypeState, fmt.Sprintf("执行端领取（第 %d 次）", t.Attempt)); err != nil {
				return err
			}
			out = &StartedItem{Kind: target.Kind, ID: t.ID, Attempt: t.Attempt, Credential: cred, Input: input, Task: &StartedTaskDetail{
				TaskNo: t.TaskNo, Feature: snap.Code, Capability: snap.Capability, FeatureVersion: t.FeatureVersion,
				Params: json.RawMessage(t.ParamsJSON), Steps: plan.Steps, Units: t.Units, UnitsDone: t.UnitsDone,
				StepsDone: stepDoneList(t, plan),
			}}
			if game != nil {
				out.GameCode = game.Code
			}
			if b.SessionRefCipher != nil {
				out.SessionRef, _ = s.sealer.Open(*b.SessionRefCipher)
			}
			return nil
		}
		return invalid(boostcontract.ErrInvalid, "未知的执行目标")
	})
	if err != nil {
		return nil, err
	}
	return out, nil
}

// ---------------------------------------------------------------------------
// 进度 / 需要输入 / 完成 / 失败 / 确认取消
// ---------------------------------------------------------------------------

// ProgressInput 心跳上报。Steps 是每一步的完成数（可以只报有变化的步骤），每一步只增不减、超过计划截断。
type ProgressInput struct {
	Steps           []StepDone
	ProgressPercent *int
	ProgressText    string
}

// Progress 执行中的任务上报进度，返回是否有取消请求。
func (s *TaskService) Progress(taskID uint, in ProgressInput) (*ExecResult, error) {
	var out *ExecResult
	err := s.repo.WithinTransaction(func(repo boostcontract.TaskRepository) error {
		t, err := s.processingTask(repo, taskID)
		if err != nil {
			return err
		}
		plan, err := DecodePlan(t)
		if err != nil {
			return err
		}
		if _, err := applyStepReport(t, plan, in.Steps); err != nil {
			return err
		}
		percent := 100
		if t.Units > 0 {
			percent = t.UnitsDone * 100 / t.Units
		}
		if in.ProgressPercent != nil {
			percent = *in.ProgressPercent
		}
		if percent < 0 {
			percent = 0
		}
		if percent > 100 {
			percent = 100
		}
		t.ProgressPercent = percent
		if text := strings.TrimSpace(in.ProgressText); text != "" {
			b, _ := repo.GetBinding(t.BindingID)
			t.ProgressText = truncateRunes(s.redactFor(b, text), 120)
		}
		if err := repo.SaveTask(t); err != nil {
			return err
		}
		out = &ExecResult{Status: t.Status, CancelRequested: t.CancelRequested}
		return nil
	})
	return out, err
}

// NeedInput 执行端需要用户输入：任务 → WAIT_INPUT；绑定 → WAIT_INPUT（验证码）或 WAIT_USER（kind=confirm，游戏内确认）。
func (s *TaskService) NeedInput(target Exec, kind, prompt string) (*ExecResult, error) {
	switch kind {
	case "":
		kind = boostdomain.InputKindSMSCode
	case boostdomain.InputKindSMSCode, boostdomain.InputKindEmailCode, boostdomain.InputKindCaptcha, boostdomain.InputKindConfirm:
	default:
		return nil, invalid(boostcontract.ErrInvalid, "未知的输入类型 %s", kind)
	}
	var out *ExecResult
	err := s.repo.WithinTransaction(func(repo boostcontract.TaskRepository) error {
		now := s.now()
		expires := now.Add(s.InputExpire)
		input := &boostdomain.Input{Target: target.Kind, TargetID: target.ID, Kind: kind, Status: boostdomain.InputStatusWaiting, ExpiresAt: expires, CreatedAt: now, UpdatedAt: now}
		switch target.Kind {
		case boostdomain.InputTargetTask:
			if kind == boostdomain.InputKindConfirm {
				return invalid(boostcontract.ErrInvalid, "代练任务不支持游戏内确认")
			}
			t, err := s.processingTask(repo, target.ID)
			if err != nil {
				return err
			}
			b, _ := repo.GetBinding(t.BindingID)
			input.Prompt = truncateRunes(s.redactFor(b, firstNonEmpty(strings.TrimSpace(prompt), "请输入验证码")), 120)
			if err := repo.CreateInput(input); err != nil {
				return err
			}
			t.Status = boostdomain.TaskStatusWaitInput
			t.ProgressText = input.Prompt
			if err := repo.SaveTask(t); err != nil {
				return err
			}
			if err := s.addEvent(repo, t, b, boostdomain.EventTypeInputRequest, input.Prompt); err != nil {
				return err
			}
			out = &ExecResult{Status: t.Status, InputID: input.ID, InputExpiresAt: &expires}
			return nil
		case boostdomain.InputTargetBinding:
			b, err := repo.LockBinding(target.ID)
			if err != nil {
				return err
			}
			if b == nil || b.DeletedAt != nil {
				return boostcontract.ErrNotFound
			}
			if b.Status != boostdomain.BindingStatusProcessing {
				return invalid(boostcontract.ErrInvalidTransition, "绑定当前不在验证中")
			}
			status, fallback := boostdomain.BindingStatusWaitInput, "请输入验证码"
			if kind == boostdomain.InputKindConfirm {
				status, fallback = boostdomain.BindingStatusWaitUser, "请在游戏内确认登录"
			}
			input.Prompt = truncateRunes(s.redactFor(b, firstNonEmpty(strings.TrimSpace(prompt), fallback)), 120)
			if err := repo.CreateInput(input); err != nil {
				return err
			}
			b.Status = status
			b.UserMessage = input.Prompt
			if err := repo.SaveBinding(b); err != nil {
				return err
			}
			out = &ExecResult{Status: b.Status, InputID: input.ID, InputExpiresAt: &expires}
			return nil
		}
		return invalid(boostcontract.ErrInvalid, "未知的执行目标")
	})
	return out, err
}

// FinishInput 完成上报。Steps 为空表示全部完成；否则按每一步上报的完成数结算（部分完成也算成功结束）。
type FinishInput struct {
	Steps      []StepDone
	ResultJSON []byte
	SessionRef string
}

// Finish 任务 → SUCCESS 并按完成量结算；绑定 → SUCCESS（可选保存会话，加密）。
func (s *TaskService) Finish(target Exec, in FinishInput) (*ExecResult, error) {
	var out *ExecResult
	err := s.repo.WithinTransaction(func(repo boostcontract.TaskRepository) error {
		var sessionCipher *string
		if ref := strings.TrimSpace(in.SessionRef); ref != "" {
			if len(ref) > 2000 {
				return invalid(boostcontract.ErrInvalid, "session_ref 过长")
			}
			c, _, err := s.sealer.Seal(ref)
			if err != nil {
				return err
			}
			sessionCipher = &c
		}
		switch target.Kind {
		case boostdomain.InputTargetTask:
			t, err := s.processingTask(repo, target.ID)
			if err != nil {
				return err
			}
			plan, err := DecodePlan(t)
			if err != nil {
				return err
			}
			if len(in.Steps) == 0 {
				completeAllSteps(t, plan)
			} else if _, err := applyStepReport(t, plan, in.Steps); err != nil {
				return err
			}
			t.ProgressPercent = 100
			if len(in.ResultJSON) > 0 {
				if len(in.ResultJSON) > 8000 || !json.Valid(in.ResultJSON) {
					return invalid(boostcontract.ErrInvalid, "result 必须是不超过 8000 字节的 JSON")
				}
				t.ResultJSON = string(in.ResultJSON)
			}
			if sessionCipher != nil {
				b, err := repo.LockBinding(t.BindingID)
				if err != nil {
					return err
				}
				if b != nil && b.DeletedAt == nil {
					b.SessionRefCipher = sessionCipher
					if err := repo.SaveBinding(b); err != nil {
						return err
					}
				}
			}
			if err := s.finishTerminal(repo, t, boostdomain.TaskStatusSuccess, "", "执行完成"); err != nil {
				return err
			}
			out = &ExecResult{Status: t.Status, PointsConsumed: t.PointsConsumed, PointsReleased: t.PointsReleased}
			return nil
		case boostdomain.InputTargetBinding:
			b, err := repo.LockBinding(target.ID)
			if err != nil {
				return err
			}
			if b == nil || b.DeletedAt != nil {
				return boostcontract.ErrNotFound
			}
			if b.Status != boostdomain.BindingStatusProcessing {
				return invalid(boostcontract.ErrInvalidTransition, "绑定当前不在验证中")
			}
			now := s.now()
			b.Status = boostdomain.BindingStatusSuccess
			b.VerifiedAt = &now
			b.LastErrorCode = ""
			b.UserMessage = "验证成功，可以创建代练任务"
			if sessionCipher != nil {
				b.SessionRefCipher = sessionCipher
			}
			if err := repo.SaveBinding(b); err != nil {
				return err
			}
			out = &ExecResult{Status: b.Status}
			return nil
		}
		return invalid(boostcontract.ErrInvalid, "未知的执行目标")
	})
	return out, err
}

// FailInput 失败上报。Retryable 只对错误码表里没有的错误码生效。
type FailInput struct {
	ErrorCode string
	Steps     []StepDone // 失败前每一步的完成数（可只报有变化的步骤）
	Message   string
	Retryable bool
}

// Fail 可重试且未超次数 / 时限 → 回到 PENDING（退避）；否则任务 FAIL 并按退款策略结算，绑定 FAIL。
// LOGIN_FAILED / ACCOUNT_BANNED / PASSWORD_CHANGED 同时把绑定置 FAIL。
func (s *TaskService) Fail(target Exec, in FailInput) (*ExecResult, error) {
	code := strings.ToUpper(strings.TrimSpace(in.ErrorCode))
	if code == "" {
		code = boostdomain.ErrorNetwork
	}
	if len(code) > 32 {
		return nil, invalid(boostcontract.ErrInvalid, "错误码过长")
	}
	info, known := boostdomain.LookupErrorCode(code)
	retryable := info.Retryable
	label := info.Label
	if !known {
		retryable = in.Retryable
		label = code
	}
	var out *ExecResult
	err := s.repo.WithinTransaction(func(repo boostcontract.TaskRepository) error {
		now := s.now()
		switch target.Kind {
		case boostdomain.InputTargetTask:
			t, err := s.processingTask(repo, target.ID)
			if err != nil {
				return err
			}
			plan, err := DecodePlan(t)
			if err != nil {
				return err
			}
			if _, err := applyStepReport(t, plan, in.Steps); err != nil {
				return err
			}
			b, err := repo.LockBinding(t.BindingID)
			if err != nil {
				return err
			}
			if msg := strings.TrimSpace(in.Message); msg != "" {
				if err := s.addEvent(repo, t, b, boostdomain.EventTypeLog, msg); err != nil {
					return err
				}
			}
			if info.BindFail && b != nil && b.DeletedAt == nil {
				b.Status = boostdomain.BindingStatusFail
				b.LastErrorCode = code
				b.UserMessage = label + "，请重新绑定"
				if err := repo.SaveBinding(b); err != nil {
					return err
				}
			}
			beforeDeadline := t.DeadlineAt == nil || now.Before(*t.DeadlineAt)
			if retryable && t.Attempt < t.MaxAttempts && beforeDeadline {
				retryAt := now.Add(retryBackoff(t.Attempt))
				t.Status = boostdomain.TaskStatusPending
				t.ErrorCode = code
				t.AvailableAt = retryAt
				t.ProgressText = label + "，稍后重试"
				if err := repo.SaveTask(t); err != nil {
					return err
				}
				if err := s.addEvent(repo, t, b, boostdomain.EventTypeState, fmt.Sprintf("失败（%s），将重试", code)); err != nil {
					return err
				}
				out = &ExecResult{Status: t.Status, RetryAt: &retryAt}
				return nil
			}
			if err := s.finishTerminal(repo, t, boostdomain.TaskStatusFail, code, label); err != nil {
				return err
			}
			out = &ExecResult{Status: t.Status, PointsConsumed: t.PointsConsumed, PointsReleased: t.PointsReleased}
			return nil

		case boostdomain.InputTargetBinding:
			b, err := repo.LockBinding(target.ID)
			if err != nil {
				return err
			}
			if b == nil || b.DeletedAt != nil {
				return boostcontract.ErrNotFound
			}
			if b.Status != boostdomain.BindingStatusProcessing {
				return invalid(boostcontract.ErrInvalidTransition, "绑定当前不在验证中")
			}
			b.LastErrorCode = code
			if retryable && b.Attempt < b.MaxAttempts {
				retryAt := now.Add(retryBackoff(b.Attempt))
				b.Status = boostdomain.BindingStatusPending
				b.AvailableAt = retryAt
				b.UserMessage = label + "，稍后自动重试"
				out = &ExecResult{Status: b.Status, RetryAt: &retryAt}
			} else {
				b.Status = boostdomain.BindingStatusFail
				b.UserMessage = label + "，请检查账号后重新绑定"
				out = &ExecResult{Status: b.Status}
			}
			return repo.SaveBinding(b)
		}
		return invalid(boostcontract.ErrInvalid, "未知的执行目标")
	})
	return out, err
}

// CancelAck 执行端确认取消：合并最后一次按步骤上报的完成数，按完成量结算并置 CANCELLED。没有取消请求时拒绝。
func (s *TaskService) CancelAck(taskID uint, steps []StepDone) (*ExecResult, error) {
	var out *ExecResult
	err := s.repo.WithinTransaction(func(repo boostcontract.TaskRepository) error {
		t, err := s.processingTask(repo, taskID)
		if err != nil {
			return err
		}
		if !t.CancelRequested {
			return invalid(boostcontract.ErrInvalidTransition, "该任务没有取消请求")
		}
		plan, err := DecodePlan(t)
		if err != nil {
			return err
		}
		if _, err := applyStepReport(t, plan, steps); err != nil {
			return err
		}
		if err := s.finishTerminal(repo, t, boostdomain.TaskStatusCancelled, "", "已按完成量结算并取消"); err != nil {
			return err
		}
		out = &ExecResult{Status: t.Status, PointsConsumed: t.PointsConsumed, PointsReleased: t.PointsReleased}
		return nil
	})
	return out, err
}

func (s *TaskService) processingTask(repo boostcontract.TaskRepository, id uint) (*boostdomain.Task, error) {
	t, err := repo.LockTask(id)
	if err != nil {
		return nil, err
	}
	if t == nil {
		return nil, boostcontract.ErrNotFound
	}
	if t.Status != boostdomain.TaskStatusProcessing {
		return nil, invalid(boostcontract.ErrInvalidTransition, "任务当前不在执行中")
	}
	return t, nil
}

// ---------------------------------------------------------------------------
// 回收（每分钟）：输入超时、任务超过时限。租约过期回收在 S6 加。
// ---------------------------------------------------------------------------

// Reap 处理过期的用户输入（任务 FAIL(INPUT_TIMEOUT) 全额退，绑定 FAIL）和超过 deadline 的任务（FAIL(TIMEOUT)）。
// 每条记录单独一个事务，一条失败不影响其余。返回处理条数。
func (s *TaskService) Reap(limit int) (int, error) {
	if limit <= 0 {
		limit = 200
	}
	now := s.now()
	n := 0
	var firstErr error
	inputs, err := s.repo.ExpiredInputs(now, limit)
	if err != nil {
		return 0, err
	}
	for _, in := range inputs {
		err := s.repo.WithinTransaction(func(repo boostcontract.TaskRepository) error {
			input, err := repo.OpenInput(in.Target, in.TargetID)
			if err != nil || input == nil || input.ID != in.ID || input.Status != boostdomain.InputStatusWaiting {
				return err
			}
			input.Status = boostdomain.InputStatusExpired
			input.ValueCipher = nil
			if err := repo.SaveInput(input); err != nil {
				return err
			}
			if in.Target == boostdomain.InputTargetTask {
				t, err := repo.LockTask(in.TargetID)
				if err != nil || t == nil || t.Status != boostdomain.TaskStatusWaitInput {
					return err
				}
				return s.finishTerminal(repo, t, boostdomain.TaskStatusFail, boostdomain.ErrorInputTimeout, "验证码超时未提交")
			}
			b, err := repo.LockBinding(in.TargetID)
			if err != nil || b == nil || (b.Status != boostdomain.BindingStatusWaitInput && b.Status != boostdomain.BindingStatusWaitUser) {
				return err
			}
			b.Status = boostdomain.BindingStatusFail
			b.LastErrorCode = boostdomain.ErrorInputTimeout
			b.UserMessage = "验证超时，请重新绑定"
			return repo.SaveBinding(b)
		})
		if err != nil && firstErr == nil {
			firstErr = err
		}
		if err == nil {
			n++
		}
	}
	ids, err := s.repo.OverdueTasks(now, limit)
	if err != nil {
		return n, err
	}
	for _, id := range ids {
		err := s.repo.WithinTransaction(func(repo boostcontract.TaskRepository) error {
			t, err := repo.LockTask(id)
			if err != nil || t == nil || boostdomain.IsTerminalTask(t.Status) || t.DeadlineAt == nil || t.DeadlineAt.After(now) {
				return err
			}
			return s.finishTerminal(repo, t, boostdomain.TaskStatusFail, boostdomain.ErrorTimeout, "超过任务时限")
		})
		if err != nil && firstErr == nil {
			firstErr = err
		}
		if err == nil {
			n++
		}
	}
	return n, firstErr
}

// QueueTasks 后台模拟执行端用的任务队列。
func (s *TaskService) QueueTasks(limit int) ([]boostcontract.TaskRow, error) {
	return s.repo.QueueTasks(limit)
}
