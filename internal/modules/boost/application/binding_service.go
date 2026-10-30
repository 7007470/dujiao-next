package application

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"
	"unicode/utf8"

	boostcontract "github.com/dujiao-next/internal/modules/boost/contract"
	boostdomain "github.com/dujiao-next/internal/modules/boost/domain"
)

// ---------------------------------------------------------------------------
// 绑定（用户侧）
// ---------------------------------------------------------------------------

// CreateBindingInput 用户添加游戏账号。Login / Password 只在内存里停留到加密为止。
type CreateBindingInput struct {
	UserID      uint
	GameID      uint
	ServerID    uint
	DisplayName string
	Login       string
	Password    string
}

// Credential 凭据明文结构（加密前 / 解密后）。只在执行端领取时解出。
type Credential struct {
	Login    string `json:"login"`
	Password string `json:"password"`
}

// MaskLogin 对应原型 maskLogin：邮箱保留前两位和域名，其它保留前三后二。
func MaskLogin(login string) string {
	if user, domain, ok := strings.Cut(login, "@"); ok {
		r := []rune(user)
		if len(r) > 2 {
			r = r[:2]
		}
		return string(r) + "***@" + domain
	}
	r := []rune(login)
	if len(r) <= 4 {
		if len(r) == 0 {
			return "***"
		}
		return string(r[:1]) + "***"
	}
	return string(r[:3]) + "****" + string(r[len(r)-2:])
}

func loginFingerprint(gameID uint, login string) string {
	sum := sha256.Sum256([]byte(fmt.Sprintf("%d:%s", gameID, strings.ToLower(strings.TrimSpace(login)))))
	return hex.EncodeToString(sum[:])
}

// startOfBeijingDay 北京时间当天 0 点（每日绑定次数按自然日算）。
func startOfBeijingDay(t time.Time) time.Time {
	local := t.In(beijing)
	return time.Date(local.Year(), local.Month(), local.Day(), 0, 0, 0, 0, beijing)
}

// CreateBinding 校验 → 加密凭据 → 绑定进入 PENDING 等执行端验证。
// 同一用户同一游戏同一登录名只能有一个有效绑定；每天最多 BindDailyLimit 次。
func (s *TaskService) CreateBinding(in CreateBindingInput) (*boostdomain.UserBinding, error) {
	if s.sealer == nil {
		return nil, invalid(boostcontract.ErrInvalid, "账号绑定暂未开放（服务端未配置凭据密钥）")
	}
	login := strings.TrimSpace(in.Login)
	name := strings.TrimSpace(in.DisplayName)
	switch {
	case login == "" || in.Password == "":
		return nil, invalid(boostcontract.ErrInvalid, "请填写登录名和密码")
	case utf8.RuneCountInString(login) > 64:
		return nil, invalid(boostcontract.ErrInvalid, "登录名最多 64 个字符")
	case len(in.Password) > 128:
		return nil, invalid(boostcontract.ErrInvalid, "密码最多 128 个字符")
	case utf8.RuneCountInString(name) > 20:
		return nil, invalid(boostcontract.ErrInvalid, "备注名最多 20 个字")
	}
	if name == "" {
		name = "未命名"
	}
	catalog := s.repo.Catalog()
	game, err := catalog.GetGame(in.GameID)
	if err != nil {
		return nil, err
	}
	if game == nil || game.Status != boostdomain.GameStatusActive {
		return nil, invalid(boostcontract.ErrNotFound, "游戏不存在或未上线")
	}
	server, err := catalog.GetServer(in.ServerID)
	if err != nil {
		return nil, err
	}
	if server == nil || server.GameID != game.ID || server.Status != boostdomain.ServerStatusActive {
		return nil, invalid(boostcontract.ErrInvalid, "请选择这个游戏已开放的区服")
	}

	plain, _ := json.Marshal(Credential{Login: login, Password: in.Password})
	cipher, keyVersion, err := s.sealer.Seal(string(plain))
	if err != nil {
		return nil, err
	}
	now := s.now()
	binding := &boostdomain.UserBinding{
		UserID:           in.UserID,
		GameID:           game.ID,
		ServerID:         server.ID,
		DisplayName:      name,
		LoginMasked:      MaskLogin(login),
		LoginFingerprint: loginFingerprint(game.ID, login),
		CredentialCipher: &cipher,
		KeyVersion:       keyVersion,
		Status:           boostdomain.BindingStatusPending,
		MaxAttempts:      s.BindMaxAttempts,
		AvailableAt:      now,
		UserMessage:      "排队等待验证",
		CreatedAt:        now,
		UpdatedAt:        now,
	}
	err = s.repo.WithinTransaction(func(repo boostcontract.TaskRepository) error {
		// 锁积分账户只是借它做"同一用户串行"，保证每日次数和重复检查可靠
		if _, err := repo.Points().LockAccount(in.UserID); err != nil {
			return err
		}
		count, err := repo.CountBindingsCreatedSince(in.UserID, startOfBeijingDay(now))
		if err != nil {
			return err
		}
		if s.BindDailyLimit > 0 && count >= s.BindDailyLimit {
			return invalid(boostcontract.ErrTaskLimit, "今天的绑定次数已用完，明天再试")
		}
		if existing, err := repo.FindLiveBinding(in.UserID, binding.LoginFingerprint); err != nil {
			return err
		} else if existing != nil {
			return invalid(boostcontract.ErrBindingDuplicate, "这个游戏账号已经绑定过")
		}
		return repo.CreateBinding(binding)
	})
	if errors.Is(err, boostcontract.ErrUniqueConflict) {
		return nil, invalid(boostcontract.ErrBindingDuplicate, "这个游戏账号已经绑定过")
	}
	if err != nil {
		return nil, err
	}
	return binding, nil
}

// BindingView 绑定详情：绑定本身 + 等待中的输入。
type BindingView struct {
	boostcontract.BindingRow
	Input *InputView `json:"input"`
}

// ListBindings 绑定列表；用户端附带等待中的输入，方便直接显示"请输入验证码"。
func (s *TaskService) ListBindings(filter boostcontract.BindingFilter) ([]BindingView, int64, error) {
	rows, total, err := s.repo.ListBindings(filter)
	if err != nil {
		return nil, 0, err
	}
	out := make([]BindingView, len(rows))
	for i, row := range rows {
		out[i] = BindingView{BindingRow: row}
		if row.Status == boostdomain.BindingStatusWaitInput || row.Status == boostdomain.BindingStatusWaitUser {
			input, err := s.repo.OpenInput(boostdomain.InputTargetBinding, row.ID)
			if err != nil {
				return nil, 0, err
			}
			if input != nil && input.Status == boostdomain.InputStatusWaiting {
				out[i].Input = presentInput(input)
			}
		}
	}
	return out, total, nil
}

// DeleteBinding 解绑：有未结束任务时拒绝；凭据和会话立即销毁（置空），记录软删除。
func (s *TaskService) DeleteBinding(userID, bindingID uint) error {
	return s.repo.WithinTransaction(func(repo boostcontract.TaskRepository) error {
		b, err := repo.LockBinding(bindingID)
		if err != nil {
			return err
		}
		if b == nil || b.UserID != userID || b.DeletedAt != nil {
			return boostcontract.ErrNotFound
		}
		busy, err := repo.CountUnfinishedTasksForBinding(b.ID)
		if err != nil {
			return err
		}
		if busy > 0 {
			return invalid(boostcontract.ErrBindingBusy, "还有未结束的任务，不能解绑")
		}
		now := s.now()
		if b.Status != boostdomain.BindingStatusSuccess && b.Status != boostdomain.BindingStatusFail && b.Status != boostdomain.BindingStatusCancelled {
			b.Status = boostdomain.BindingStatusCancelled
		}
		if err := s.expireOpenInput(repo, boostdomain.InputTargetBinding, b.ID); err != nil {
			return err
		}
		b.CredentialCipher = nil
		b.SessionRefCipher = nil
		b.LeaseToken = nil
		b.LeaseExpiresAt = nil
		b.WorkerID = nil
		b.DeletedAt = &now
		b.UserMessage = "已解绑，凭据已销毁"
		return repo.SaveBinding(b)
	})
}

// ConfirmInGame 用户在游戏里点了"允许登录"：WAIT_USER → PENDING，执行端重新领取。
func (s *TaskService) ConfirmInGame(userID, bindingID uint) error {
	return s.repo.WithinTransaction(func(repo boostcontract.TaskRepository) error {
		b, err := repo.LockBinding(bindingID)
		if err != nil {
			return err
		}
		if b == nil || b.UserID != userID || b.DeletedAt != nil {
			return boostcontract.ErrNotFound
		}
		if b.Status != boostdomain.BindingStatusWaitUser {
			return invalid(boostcontract.ErrInvalidTransition, "当前不需要在游戏内确认")
		}
		input, err := repo.OpenInput(boostdomain.InputTargetBinding, b.ID)
		if err != nil {
			return err
		}
		now := s.now()
		if input == nil || input.Status != boostdomain.InputStatusWaiting || !input.ExpiresAt.After(now) {
			return invalid(boostcontract.ErrInputExpired, "确认已超时，请重新绑定")
		}
		input.Status = boostdomain.InputStatusSubmitted
		if err := repo.SaveInput(input); err != nil {
			return err
		}
		b.Status = boostdomain.BindingStatusPending
		b.AvailableAt = now
		b.UserMessage = "已确认，继续验证"
		return repo.SaveBinding(b)
	})
}

// SubmitInput 用户提交验证码：加密保存，目标回到 PENDING 等执行端再次领取（领取时解密下发后立即清空）。
// target = binding / task。
func (s *TaskService) SubmitInput(userID uint, target string, targetID uint, value string) error {
	value = strings.TrimSpace(value)
	if value == "" || utf8.RuneCountInString(value) > 64 {
		return invalid(boostcontract.ErrInvalid, "请输入 1–64 个字符")
	}
	if s.sealer == nil {
		return invalid(boostcontract.ErrInvalid, "服务端未配置凭据密钥")
	}
	return s.repo.WithinTransaction(func(repo boostcontract.TaskRepository) error {
		now := s.now()
		var (
			binding *boostdomain.UserBinding
			task    *boostdomain.Task
			err     error
		)
		switch target {
		case boostdomain.InputTargetBinding:
			binding, err = repo.LockBinding(targetID)
			if err != nil {
				return err
			}
			if binding == nil || binding.UserID != userID || binding.DeletedAt != nil {
				return boostcontract.ErrNotFound
			}
			if binding.Status != boostdomain.BindingStatusWaitInput {
				return invalid(boostcontract.ErrInvalidTransition, "当前不需要输入")
			}
		case boostdomain.InputTargetTask:
			task, err = repo.LockTask(targetID)
			if err != nil {
				return err
			}
			if task == nil || task.UserID != userID {
				return boostcontract.ErrNotFound
			}
			if task.Status != boostdomain.TaskStatusWaitInput {
				return invalid(boostcontract.ErrInvalidTransition, "当前不需要输入")
			}
		default:
			return invalid(boostcontract.ErrInvalid, "未知的输入目标")
		}
		input, err := repo.OpenInput(target, targetID)
		if err != nil {
			return err
		}
		if input == nil || input.Status != boostdomain.InputStatusWaiting || !input.ExpiresAt.After(now) {
			return invalid(boostcontract.ErrInputExpired, "输入已过期")
		}
		cipher, _, err := s.sealer.Seal(value)
		if err != nil {
			return err
		}
		input.ValueCipher = &cipher
		input.Status = boostdomain.InputStatusSubmitted
		if err := repo.SaveInput(input); err != nil {
			return err
		}
		if binding != nil {
			binding.Status = boostdomain.BindingStatusPending
			binding.AvailableAt = now
			binding.UserMessage = "已提交，继续验证"
			return repo.SaveBinding(binding)
		}
		task.Status = boostdomain.TaskStatusPending
		task.AvailableAt = now
		task.ProgressText = "已提交验证码，等待继续执行"
		if err := repo.SaveTask(task); err != nil {
			return err
		}
		return s.addEvent(repo, task, nil, boostdomain.EventTypeState, "用户已提交输入，重新排队")
	})
}

// SubmitTaskInputByNo 用户端按任务号提交。
func (s *TaskService) SubmitTaskInputByNo(userID uint, taskNo, value string) error {
	task, err := s.repo.GetTaskByNo(strings.TrimSpace(taskNo))
	if err != nil {
		return err
	}
	if task == nil || task.UserID != userID {
		return boostcontract.ErrNotFound
	}
	return s.SubmitInput(userID, boostdomain.InputTargetTask, task.ID, value)
}

func (s *TaskService) expireOpenInput(repo boostcontract.TaskRepository, target string, targetID uint) error {
	input, err := repo.OpenInput(target, targetID)
	if err != nil || input == nil {
		return err
	}
	input.Status = boostdomain.InputStatusExpired
	input.ValueCipher = nil
	return repo.SaveInput(input)
}
