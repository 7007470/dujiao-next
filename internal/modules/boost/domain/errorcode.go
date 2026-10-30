package domain

// ErrorCodeInfo 执行失败错误码的服务端权威定义（docs/game-business-design.md §3.3）。
// 执行端上报的 retryable 只对表里没有的错误码生效。
type ErrorCodeInfo struct {
	Retryable bool
	System    bool   // true = 系统原因，false = 用户原因（决定 refund_policy 怎么算）
	BindFail  bool   // true = 同时把绑定账号置为 FAIL
	Label     string // 给用户看的中文说明
}

// 错误码。
const (
	ErrorNetwork             = "NETWORK"
	ErrorGameMaintenance     = "GAME_MAINTENANCE"
	ErrorWorkerCrash         = "WORKER_CRASH"
	ErrorLeaseExpired        = "LEASE_EXPIRED"
	ErrorLoginFailed         = "LOGIN_FAILED"
	ErrorAccountBanned       = "ACCOUNT_BANNED"
	ErrorPasswordChanged     = "PASSWORD_CHANGED"
	ErrorInputTimeout        = "INPUT_TIMEOUT"
	ErrorStageLocked         = "STAGE_LOCKED"
	ErrorNotUnlocked         = "NOT_UNLOCKED"
	ErrorInsufficientStamina = "INSUFFICIENT_STAMINA"
	ErrorUserKicked          = "USER_KICKED"
	ErrorTimeout             = "TIMEOUT"
)

var errorCodes = map[string]ErrorCodeInfo{
	ErrorNetwork:             {Retryable: true, System: true, Label: "网络异常"},
	ErrorGameMaintenance:     {Retryable: true, System: true, Label: "游戏维护"},
	ErrorWorkerCrash:         {Retryable: true, System: true, Label: "执行端崩溃"},
	ErrorLeaseExpired:        {Retryable: true, System: true, Label: "执行端失联"},
	ErrorLoginFailed:         {System: true, BindFail: true, Label: "登录失败"},
	ErrorAccountBanned:       {System: true, BindFail: true, Label: "账号被封禁"},
	ErrorPasswordChanged:     {System: true, BindFail: true, Label: "密码已修改"},
	ErrorInputTimeout:        {System: true, Label: "验证码超时"},
	ErrorStageLocked:         {Label: "关卡未解锁"},
	ErrorNotUnlocked:         {Label: "功能未解锁"},
	ErrorInsufficientStamina: {Label: "体力不足"},
	ErrorUserKicked:          {Label: "用户上线顶号"},
	ErrorTimeout:             {Label: "超过任务时限"},
}

// LookupErrorCode 返回错误码定义；ok=false 表示未知错误码。
func LookupErrorCode(code string) (ErrorCodeInfo, bool) {
	info, ok := errorCodes[code]
	return info, ok
}

// ErrorCodes 返回全部已知错误码（后台模拟执行端的下拉框用）。
func ErrorCodes() []string {
	return []string{
		ErrorNetwork, ErrorGameMaintenance, ErrorWorkerCrash, ErrorLeaseExpired,
		ErrorLoginFailed, ErrorAccountBanned, ErrorPasswordChanged, ErrorInputTimeout,
		ErrorStageLocked, ErrorNotUnlocked, ErrorInsufficientStamina, ErrorUserKicked, ErrorTimeout,
	}
}

// 退款策略（功能定义 refund_policy，见 docs/boost-feature-schema.md §5）。
const (
	RefundProrate          = "prorate"
	RefundFullOnSystemFail = "full_on_system_fail"
	RefundNone             = "none"
)

// IsTerminalTask 任务是否已结束。
func IsTerminalTask(status string) bool {
	return status == TaskStatusSuccess || status == TaskStatusFail || status == TaskStatusCancelled
}
