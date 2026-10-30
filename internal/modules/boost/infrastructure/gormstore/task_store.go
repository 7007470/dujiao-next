package gormstore

import (
	"errors"
	"strings"
	"time"

	boostcontract "github.com/dujiao-next/internal/modules/boost/contract"
	boostdomain "github.com/dujiao-next/internal/modules/boost/domain"

	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

// TaskStore 是 boostcontract.TaskRepository 的 GORM 实现。
type TaskStore struct {
	db *gorm.DB
}

// NewTaskStore 创建绑定 / 任务仓储。
func NewTaskStore(db *gorm.DB) *TaskStore {
	return &TaskStore{db: db}
}

var _ boostcontract.TaskRepository = (*TaskStore)(nil)

// WithinTransaction 在事务里执行 fn；Points() / Catalog() 在同一事务上。
func (s *TaskStore) WithinTransaction(fn func(repo boostcontract.TaskRepository) error) error {
	return s.db.Transaction(func(tx *gorm.DB) error {
		return fn(&TaskStore{db: tx})
	})
}

func (s *TaskStore) Points() boostcontract.PointsRepository   { return &PointsStore{db: s.db} }
func (s *TaskStore) Catalog() boostcontract.CatalogRepository { return NewCatalogStore(s.db) }

// mapUnique 把唯一约束冲突统一成 ErrUniqueConflict（SQLite / PostgreSQL 的报错文本都含 unique / duplicate）。
func mapUnique(err error) error {
	if err == nil {
		return nil
	}
	if errors.Is(err, gorm.ErrDuplicatedKey) {
		return boostcontract.ErrUniqueConflict
	}
	msg := strings.ToLower(err.Error())
	if strings.Contains(msg, "unique") || strings.Contains(msg, "duplicate") {
		return boostcontract.ErrUniqueConflict
	}
	return err
}

// ---- 绑定 ----

func (s *TaskStore) CreateBinding(b *boostdomain.UserBinding) error {
	return mapUnique(s.db.Create(b).Error)
}

// SaveBinding 整行写回（含置空的凭据列）。
func (s *TaskStore) SaveBinding(b *boostdomain.UserBinding) error {
	b.UpdatedAt = time.Now()
	return mapUnique(s.db.Select("*").Omit("created_at").Save(b).Error)
}

func (s *TaskStore) GetBinding(id uint) (*boostdomain.UserBinding, error) {
	return first[boostdomain.UserBinding](s.db.Where("id = ?", id))
}

// LockBinding PostgreSQL 行锁；SQLite 靠单写事务。
func (s *TaskStore) LockBinding(id uint) (*boostdomain.UserBinding, error) {
	return first[boostdomain.UserBinding](s.db.Clauses(clause.Locking{Strength: "UPDATE"}).Where("id = ?", id))
}

func (s *TaskStore) ListBindings(filter boostcontract.BindingFilter) ([]boostcontract.BindingRow, int64, error) {
	query := s.db.Table("boost_user_bindings AS b").
		Joins("LEFT JOIN boost_games AS g ON g.id = b.game_id").
		Joins("LEFT JOIN boost_game_servers AS sv ON sv.id = b.server_id").
		Joins("LEFT JOIN users AS u ON u.id = b.user_id")
	if filter.UserID != 0 {
		query = query.Where("b.user_id = ?", filter.UserID)
	}
	if filter.GameID != 0 {
		query = query.Where("b.game_id = ?", filter.GameID)
	}
	if filter.Status != "" {
		query = query.Where("b.status = ?", filter.Status)
	}
	if !filter.IncludeDeleted {
		query = query.Where("b.deleted_at IS NULL")
	}
	var total int64
	if err := query.Count(&total).Error; err != nil {
		return nil, 0, err
	}
	page, size := normalizePage(filter.Page, filter.PageSize)
	type row struct {
		boostdomain.UserBinding
		GameName   string
		GameCode   string
		ServerName string
		UserEmail  string
	}
	var rows []row
	if err := query.Select("b.*, g.name AS game_name, g.code AS game_code, sv.name AS server_name, u.email AS user_email").
		Order("b.id DESC").Offset((page - 1) * size).Limit(size).Scan(&rows).Error; err != nil {
		return nil, 0, err
	}
	out := make([]boostcontract.BindingRow, len(rows))
	for i, r := range rows {
		b := r.UserBinding
		b.CredentialCipher, b.SessionRefCipher, b.LeaseToken = nil, nil, nil // 列表不带密文
		out[i] = boostcontract.BindingRow{UserBinding: b, GameName: r.GameName, GameCode: r.GameCode, ServerName: r.ServerName, UserEmail: r.UserEmail}
	}
	return out, total, nil
}

func (s *TaskStore) CountBindingsCreatedSince(userID uint, since time.Time) (int64, error) {
	var n int64
	err := s.db.Model(&boostdomain.UserBinding{}).Where("user_id = ? AND created_at >= ?", userID, since).Count(&n).Error
	return n, err
}

func (s *TaskStore) FindLiveBinding(userID uint, fingerprint string) (*boostdomain.UserBinding, error) {
	return first[boostdomain.UserBinding](s.db.Where(
		"user_id = ? AND login_fingerprint = ? AND deleted_at IS NULL AND status NOT IN ?",
		userID, fingerprint, []string{boostdomain.BindingStatusFail, boostdomain.BindingStatusCancelled}))
}

// ---- 任务 ----

func (s *TaskStore) CreateTask(t *boostdomain.Task) error {
	return mapUnique(s.db.Create(t).Error)
}

func (s *TaskStore) SaveTask(t *boostdomain.Task) error {
	t.UpdatedAt = time.Now()
	return mapUnique(s.db.Select("*").Omit("created_at").Save(t).Error)
}

func (s *TaskStore) GetTask(id uint) (*boostdomain.Task, error) {
	return first[boostdomain.Task](s.db.Where("id = ?", id))
}

func (s *TaskStore) GetTaskByNo(taskNo string) (*boostdomain.Task, error) {
	return first[boostdomain.Task](s.db.Where("task_no = ?", taskNo))
}

func (s *TaskStore) LockTask(id uint) (*boostdomain.Task, error) {
	return first[boostdomain.Task](s.db.Clauses(clause.Locking{Strength: "UPDATE"}).Where("id = ?", id))
}

func (s *TaskStore) GetTaskByIdempotency(userID uint, key string) (*boostdomain.Task, error) {
	return first[boostdomain.Task](s.db.Where("user_id = ? AND idempotency_key = ?", userID, key))
}

type taskRow struct {
	boostdomain.Task
	GameName    string
	GameCode    string
	BindingName string
	LoginMasked string
	UserEmail   string
}

func (s *TaskStore) taskQuery() *gorm.DB {
	return s.db.Table("boost_tasks AS t").
		Joins("LEFT JOIN boost_games AS g ON g.id = t.game_id").
		Joins("LEFT JOIN boost_user_bindings AS b ON b.id = t.binding_id").
		Joins("LEFT JOIN users AS u ON u.id = t.user_id")
}

func scanTasks(query *gorm.DB) ([]boostcontract.TaskRow, error) {
	var rows []taskRow
	if err := query.Select("t.*, g.name AS game_name, g.code AS game_code, b.display_name AS binding_name, b.login_masked AS login_masked, u.email AS user_email").
		Scan(&rows).Error; err != nil {
		return nil, err
	}
	out := make([]boostcontract.TaskRow, len(rows))
	for i, r := range rows {
		task := r.Task
		task.LeaseToken = nil
		out[i] = boostcontract.TaskRow{Task: task, GameName: r.GameName, GameCode: r.GameCode, BindingName: r.BindingName, LoginMasked: r.LoginMasked, UserEmail: r.UserEmail}
	}
	return out, nil
}

func (s *TaskStore) ListTasks(filter boostcontract.TaskFilter) ([]boostcontract.TaskRow, int64, error) {
	query := s.taskQuery()
	if filter.UserID != 0 {
		query = query.Where("t.user_id = ?", filter.UserID)
	}
	if filter.GameID != 0 {
		query = query.Where("t.game_id = ?", filter.GameID)
	}
	switch filter.Status {
	case "":
	case "active":
		query = query.Where("t.status IN ?", []string{boostdomain.TaskStatusPending, boostdomain.TaskStatusProcessing, boostdomain.TaskStatusWaitInput})
	default:
		query = query.Where("t.status = ?", filter.Status)
	}
	if no := strings.TrimSpace(filter.TaskNo); no != "" {
		query = query.Where("t.task_no = ?", no)
	}
	var total int64
	if err := query.Count(&total).Error; err != nil {
		return nil, 0, err
	}
	page, size := normalizePage(filter.Page, filter.PageSize)
	rows, err := scanTasks(query.Order("t.id DESC").Offset((page - 1) * size).Limit(size))
	return rows, total, err
}

func (s *TaskStore) QueueTasks(limit int) ([]boostcontract.TaskRow, error) {
	return scanTasks(s.taskQuery().
		Where("t.status IN ?", []string{boostdomain.TaskStatusPending, boostdomain.TaskStatusProcessing, boostdomain.TaskStatusWaitInput}).
		Order("t.created_at ASC, t.id ASC").Limit(limit))
}

var activeTaskStatuses = []string{boostdomain.TaskStatusPending, boostdomain.TaskStatusProcessing, boostdomain.TaskStatusWaitInput}

func (s *TaskStore) CountActiveTasks(userID, featureID uint) (int64, error) {
	var n int64
	err := s.db.Model(&boostdomain.Task{}).Where("user_id = ? AND feature_id = ? AND status IN ?", userID, featureID, activeTaskStatuses).Count(&n).Error
	return n, err
}

func (s *TaskStore) CountUnfinishedTasksForBinding(bindingID uint) (int64, error) {
	var n int64
	err := s.db.Model(&boostdomain.Task{}).Where("binding_id = ? AND status IN ?", bindingID, activeTaskStatuses).Count(&n).Error
	return n, err
}

func (s *TaskStore) CountProcessingTasksForBinding(bindingID uint) (int64, error) {
	var n int64
	err := s.db.Model(&boostdomain.Task{}).Where("binding_id = ? AND status = ?", bindingID, boostdomain.TaskStatusProcessing).Count(&n).Error
	return n, err
}

func (s *TaskStore) OverdueTasks(now time.Time, limit int) ([]uint, error) {
	var ids []uint
	err := s.db.Model(&boostdomain.Task{}).
		Where("status IN ? AND deadline_at IS NOT NULL AND deadline_at <= ?", activeTaskStatuses, now).
		Order("id ASC").Limit(limit).Pluck("id", &ids).Error
	return ids, err
}

// ---- 用户输入 ----

func (s *TaskStore) CreateInput(input *boostdomain.Input) error {
	return mapUnique(s.db.Create(input).Error)
}

func (s *TaskStore) SaveInput(input *boostdomain.Input) error {
	input.UpdatedAt = time.Now()
	return mapUnique(s.db.Select("*").Omit("created_at").Save(input).Error)
}

func (s *TaskStore) OpenInput(target string, targetID uint) (*boostdomain.Input, error) {
	return first[boostdomain.Input](s.db.Where("target = ? AND target_id = ? AND status IN ?", target, targetID,
		[]string{boostdomain.InputStatusWaiting, boostdomain.InputStatusSubmitted}).Order("id DESC"))
}

func (s *TaskStore) ExpiredInputs(now time.Time, limit int) ([]boostdomain.Input, error) {
	var list []boostdomain.Input
	err := s.db.Where("status = ? AND expires_at <= ?", boostdomain.InputStatusWaiting, now).Order("id ASC").Limit(limit).Find(&list).Error
	return list, err
}

// ---- 事件 ----

func (s *TaskStore) CreateEvent(event *boostdomain.TaskEvent) error {
	return mapUnique(s.db.Create(event).Error)
}

func (s *TaskStore) MaxEventSeq(taskID uint) (int, error) {
	var seq *int
	if err := s.db.Model(&boostdomain.TaskEvent{}).Where("task_id = ?", taskID).Select("MAX(seq)").Scan(&seq).Error; err != nil {
		return 0, err
	}
	if seq == nil {
		return 0, nil
	}
	return *seq, nil
}

func (s *TaskStore) EventExists(taskID uint, seq int) (bool, error) {
	var n int64
	err := s.db.Model(&boostdomain.TaskEvent{}).Where("task_id = ? AND seq = ?", taskID, seq).Count(&n).Error
	return n > 0, err
}

func (s *TaskStore) ListEvents(taskID uint, limit int) ([]boostdomain.TaskEvent, error) {
	var list []boostdomain.TaskEvent
	err := s.db.Where("task_id = ?", taskID).Order("seq DESC").Limit(limit).Find(&list).Error
	return list, err
}
