package domain

import "time"

// Task 代练任务。创建时保存功能版本、快照和编译后的执行计划，之后后台改价、改关卡、下架都不影响它。
// 不变量：结算后 PointsConsumed + PointsReleased = PointsHeld；
// 同一绑定账号同时只有一个 PROCESSING 任务（部分唯一索引，见 gormstore）。
type Task struct {
	ID              uint       `gorm:"primarykey" json:"id"`
	TaskNo          string     `gorm:"type:varchar(32);not null;uniqueIndex" json:"task_no"`
	UserID          uint       `gorm:"not null;index;uniqueIndex:idx_boost_tasks_user_idempotency,priority:1" json:"user_id"`
	BindingID       uint       `gorm:"not null;index" json:"binding_id"`
	GameID          uint       `gorm:"not null;index:idx_boost_tasks_pull,priority:2" json:"game_id"`
	FeatureID       uint       `gorm:"not null;index" json:"feature_id"`
	FeatureVersion  int        `gorm:"not null" json:"feature_version"`
	SnapshotJSON    string     `gorm:"column:snapshot_json;type:text;not null" json:"snapshot_json"`
	ParamsJSON      string     `gorm:"column:params_json;type:text;not null" json:"params_json"`
	PlanJSON        string     `gorm:"column:plan_json;type:text;not null" json:"plan_json"`
	Summary         string     `gorm:"type:varchar(255)" json:"summary"`
	Units           int        `gorm:"not null" json:"units"`
	UnitsDone       int        `gorm:"not null" json:"units_done"`
	PointsHeld      int64      `gorm:"not null" json:"points_held"`
	PointsConsumed  int64      `gorm:"not null" json:"points_consumed"`
	PointsReleased  int64      `gorm:"not null" json:"points_released"`
	Status          string     `gorm:"type:varchar(16);not null;default:'PENDING';index:idx_boost_tasks_pull,priority:1" json:"status"`
	CancelRequested bool       `gorm:"not null" json:"cancel_requested"`
	ProgressPercent int        `gorm:"not null" json:"progress_percent"`
	ProgressText    string     `gorm:"type:varchar(255)" json:"progress_text"`
	ResultJSON      string     `gorm:"column:result_json;type:text" json:"result_json"`
	ErrorCode       string     `gorm:"type:varchar(32)" json:"error_code"`
	Attempt         int        `gorm:"not null" json:"attempt"`
	MaxAttempts     int        `gorm:"not null" json:"max_attempts"`
	AvailableAt     time.Time  `gorm:"not null;index:idx_boost_tasks_pull,priority:3" json:"available_at"`
	DeadlineAt      *time.Time `gorm:"index" json:"deadline_at,omitempty"`
	WorkerID        *uint      `gorm:"index" json:"worker_id,omitempty"`
	LeaseToken      *string    `gorm:"type:varchar(64)" json:"-"`
	LeaseExpiresAt  *time.Time `gorm:"index" json:"lease_expires_at,omitempty"`
	StartedAt       *time.Time `json:"started_at,omitempty"`
	FinishedAt      *time.Time `json:"finished_at,omitempty"`
	IdempotencyKey  string     `gorm:"type:varchar(64);not null;uniqueIndex:idx_boost_tasks_user_idempotency,priority:2" json:"-"`
	CreatedAt       time.Time  `gorm:"index" json:"created_at"`
	UpdatedAt       time.Time  `json:"updated_at"`
}

func (Task) TableName() string { return "boost_tasks" }

// TaskEvent 任务事件，(task_id, seq) 唯一用于 Worker 重复上报去重。Message 必须已脱敏。
type TaskEvent struct {
	ID        uint      `gorm:"primarykey" json:"id"`
	TaskID    uint      `gorm:"not null;uniqueIndex:idx_boost_task_events_task_seq,priority:1" json:"task_id"`
	Seq       int       `gorm:"not null;uniqueIndex:idx_boost_task_events_task_seq,priority:2" json:"seq"`
	Type      string    `gorm:"type:varchar(16);not null" json:"type"`
	Message   string    `gorm:"type:varchar(500)" json:"message"`
	DataJSON  string    `gorm:"column:data_json;type:text" json:"data_json"`
	CreatedAt time.Time `json:"created_at"`
}

func (TaskEvent) TableName() string { return "boost_task_events" }
