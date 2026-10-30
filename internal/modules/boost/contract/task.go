package contract

import (
	"errors"
	"time"

	boostdomain "github.com/dujiao-next/internal/modules/boost/domain"
)

// 绑定和任务的业务错误。
var (
	ErrInvalidTransition = errors.New("boost: invalid state transition")
	ErrTaskLimit         = errors.New("boost: active task limit reached")
	ErrBindingBusy       = errors.New("boost: binding has unfinished tasks")
	ErrBindingDuplicate  = errors.New("boost: game account already bound")
	ErrInputExpired      = errors.New("boost: input expired")
	ErrParamsInvalid     = errors.New("boost: task params invalid")
	// ErrUniqueConflict 由仓储在违反唯一约束时返回（幂等键、指纹、同绑定只有一个执行中任务、事件 seq 等）。
	ErrUniqueConflict = errors.New("boost: unique constraint conflict")
	ErrSimulatorOff   = errors.New("boost: dev simulator disabled")
)

// CredentialSealer 加密保存游戏账号凭据、会话和验证码（AES-256-GCM，独立密钥，记录密钥版本）。
// 密文自带版本前缀（"v{版本}:"），所以 Open 不需要另传版本；Seal 同时返回版本号供写入 key_version 列。
// 实现在 infrastructure/credential。
type CredentialSealer interface {
	Seal(plaintext string) (cipher string, keyVersion int, err error)
	Open(cipher string) (string, error)
}

// BindingFilter 绑定列表筛选。UserID 为 0 表示全部用户（后台）。
type BindingFilter struct {
	UserID         uint
	GameID         uint
	Status         string
	IncludeDeleted bool
	Page           int
	PageSize       int
}

// BindingRow 绑定列表行（附游戏 / 区服名称和用户邮箱）。
type BindingRow struct {
	boostdomain.UserBinding
	GameName   string `json:"game_name"`
	GameCode   string `json:"game_code"`
	ServerName string `json:"server_name"`
	UserEmail  string `json:"user_email,omitempty"`
}

// TaskFilter 任务列表筛选。UserID 为 0 表示全部用户（后台）。
type TaskFilter struct {
	UserID   uint
	GameID   uint
	Status   string
	TaskNo   string
	Page     int
	PageSize int
}

// TaskRow 任务列表行。
type TaskRow struct {
	boostdomain.Task
	GameName    string `json:"game_name"`
	GameCode    string `json:"game_code"`
	BindingName string `json:"binding_name"`
	LoginMasked string `json:"login_masked"`
	UserEmail   string `json:"user_email,omitempty"`
}

// TaskRepository 绑定、输入、任务、事件的数据端口。写操作在 WithinTransaction 里调用；
// Points() / Catalog() 返回同一个数据库句柄（事务内即同一事务）上的积分和目录仓储，
// 保证"创建任务 + 冻结积分"、"结算 + 改任务状态"在同一事务。
type TaskRepository interface {
	WithinTransaction(fn func(repo TaskRepository) error) error
	Points() PointsRepository
	Catalog() CatalogRepository

	CreateBinding(binding *boostdomain.UserBinding) error
	SaveBinding(binding *boostdomain.UserBinding) error
	GetBinding(id uint) (*boostdomain.UserBinding, error)
	LockBinding(id uint) (*boostdomain.UserBinding, error)
	ListBindings(filter BindingFilter) ([]BindingRow, int64, error)
	CountBindingsCreatedSince(userID uint, since time.Time) (int64, error)
	// FindLiveBinding 同一用户未删除、未失败、未取消的同指纹绑定。
	FindLiveBinding(userID uint, fingerprint string) (*boostdomain.UserBinding, error)

	CreateTask(task *boostdomain.Task) error
	SaveTask(task *boostdomain.Task) error
	GetTask(id uint) (*boostdomain.Task, error)
	GetTaskByNo(taskNo string) (*boostdomain.Task, error)
	LockTask(id uint) (*boostdomain.Task, error)
	GetTaskByIdempotency(userID uint, key string) (*boostdomain.Task, error)
	ListTasks(filter TaskFilter) ([]TaskRow, int64, error)
	// CountActiveTasks 用户某功能未结束的任务数（max_active_per_user 按功能计，见 boost-feature-schema.md §2）。
	CountActiveTasks(userID, featureID uint) (int64, error)
	CountUnfinishedTasksForBinding(bindingID uint) (int64, error)
	CountProcessingTasksForBinding(bindingID uint) (int64, error)
	// OverdueTasks 未结束且 deadline_at <= now 的任务 ID。
	OverdueTasks(now time.Time, limit int) ([]uint, error)
	// QueueTasks 后台模拟执行端用：PENDING / PROCESSING / WAIT_INPUT 的任务，按创建时间。
	QueueTasks(limit int) ([]TaskRow, error)

	CreateInput(input *boostdomain.Input) error
	SaveInput(input *boostdomain.Input) error
	// OpenInput 目标当前 waiting 或 submitted 的输入；没有返回 nil。
	OpenInput(target string, targetID uint) (*boostdomain.Input, error)
	// ExpiredInputs 已过期仍 waiting 的输入。
	ExpiredInputs(now time.Time, limit int) ([]boostdomain.Input, error)

	CreateEvent(event *boostdomain.TaskEvent) error
	MaxEventSeq(taskID uint) (int, error)
	EventExists(taskID uint, seq int) (bool, error)
	ListEvents(taskID uint, limit int) ([]boostdomain.TaskEvent, error)
}
