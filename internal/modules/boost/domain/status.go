// Package domain 定义代练模块的持久化模型和状态取值。
// 状态取值以 AGENTS.md §3 为准，新增取值必须先改文档。
package domain

// 游戏状态。
const (
	GameStatusDraft  = "draft"
	GameStatusActive = "active"
	GameStatusHidden = "hidden"
)

// 区服状态和平台。
const (
	ServerStatusActive   = "active"
	ServerStatusDisabled = "disabled"

	PlatformAll     = "all"
	PlatformAndroid = "android"
	PlatformIOS     = "ios"
	PlatformPC      = "pc"
)

// 功能状态。
const (
	FeatureStatusDraft    = "draft"
	FeatureStatusActive   = "active"
	FeatureStatusDisabled = "disabled"
)

// 积分批次来源和状态。
const (
	LotSourceOrder = "order"
	LotSourceGrant = "grant"
	LotSourcePromo = "promo"

	LotStatusActive    = "active"
	LotStatusExhausted = "exhausted"
	LotStatusExpired   = "expired"
	LotStatusRevoked   = "revoked"
)

// 积分流水类型。
const (
	LedgerPurchase    = "purchase"
	LedgerGrant       = "grant"
	LedgerHold        = "hold"
	LedgerRelease     = "release"
	LedgerConsume     = "consume"
	LedgerExpire      = "expire"
	LedgerRevoke      = "revoke"
	LedgerAdminAdjust = "admin_adjust"
)

// 绑定状态（AGENTS.md §3.3）。
const (
	BindingStatusPending    = "PENDING"
	BindingStatusProcessing = "PROCESSING"
	BindingStatusWaitInput  = "WAIT_INPUT"
	BindingStatusWaitUser   = "WAIT_USER"
	BindingStatusSuccess    = "SUCCESS"
	BindingStatusFail       = "FAIL"
	BindingStatusCancelled  = "CANCELLED"
)

// 任务状态（AGENTS.md §3.3）。
const (
	TaskStatusPending    = "PENDING"
	TaskStatusProcessing = "PROCESSING"
	TaskStatusWaitInput  = "WAIT_INPUT"
	TaskStatusSuccess    = "SUCCESS"
	TaskStatusFail       = "FAIL"
	TaskStatusCancelled  = "CANCELLED"
)

// 用户输入的目标、类型和状态。
const (
	InputTargetBinding = "binding"
	InputTargetTask    = "task"

	InputKindSMSCode   = "sms_code"
	InputKindEmailCode = "email_code"
	InputKindCaptcha   = "captcha"
	InputKindConfirm   = "confirm"

	InputStatusWaiting   = "waiting"
	InputStatusSubmitted = "submitted"
	InputStatusConsumed  = "consumed"
	InputStatusExpired   = "expired"
)

// 任务事件类型。
const (
	EventTypeState        = "state"
	EventTypeLog          = "log"
	EventTypeInputRequest = "input_request"
)
