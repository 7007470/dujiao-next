package integrationtest

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"testing"
	"time"

	boostapp "github.com/dujiao-next/internal/modules/boost/application"
	boostdomain "github.com/dujiao-next/internal/modules/boost/domain"
	"github.com/dujiao-next/internal/modules/boost/infrastructure/credential"
	"github.com/dujiao-next/internal/modules/boost/infrastructure/gormstore"

	"github.com/dujiao-next/internal/bootstrap/database/migrations"
	"github.com/dujiao-next/internal/constants"
	userdomain "github.com/dujiao-next/internal/modules/identity/user/domain"
	"github.com/dujiao-next/internal/platform/database/gormdb"

	"github.com/glebarez/sqlite"
	"gorm.io/gorm"
	gormlogger "gorm.io/gorm/logger"
)

const testCredentialKey = "boost-credential-test-key-0123456789abcdef"

// rateByUser 按用户返回会员折扣率（没登记的不打折）。
type rateByUser map[uint]int

func (r rateByUser) DiscountRate(userID uint) int {
	if v, ok := r[userID]; ok {
		return v
	}
	return 100
}

// taskEnv 原型 seed() 的 Go 版本：两个上线的游戏（starfall 4 个功能、ironcrest 2 个功能），
// 用户 vip 有 9 折，用户 plain 不打折；用完整迁移跑 SQLite。
type taskEnv struct {
	t       *testing.T
	db      *gorm.DB
	catalog *boostapp.CatalogService
	points  *boostapp.PointsService
	tasks   *boostapp.TaskService
	clock   time.Time
	rates   rateByUser

	starfall, ironcrest     uint
	starfallCN, ironcrestS1 uint
	features                map[string]uint // main_stage / daily / abyss / idle_farm / farm / march
	vip, plain              userdomain.User
}

func newTaskEnv(t *testing.T) *taskEnv {
	t.Helper()
	dsn := fmt.Sprintf("file:boost_task_%d?mode=memory&cache=shared", time.Now().UnixNano())
	db, err := gorm.Open(sqlite.Open(dsn), &gorm.Config{Logger: gormlogger.Default.LogMode(gormlogger.Silent)})
	if err != nil {
		t.Fatalf("open sqlite: %v", err)
	}
	sqlDB, _ := db.DB()
	sqlDB.SetMaxOpenConns(1)
	gormdb.DB = db
	if err := migrations.AutoMigrate(); err != nil {
		t.Fatalf("migrate: %v", err)
	}
	sealer, err := credential.New(map[int]string{1: testCredentialKey}, 1)
	if err != nil {
		t.Fatal(err)
	}
	e := &taskEnv{t: t, db: db, clock: time.Date(2026, 10, 23, 1, 0, 0, 0, time.UTC), rates: rateByUser{}, features: map[string]uint{}}
	clock := func() time.Time { return e.clock }
	e.catalog = boostapp.NewCatalogService(gormstore.NewCatalogStore(db))
	e.points = boostapp.NewPointsService(gormstore.NewPointsStore(db), nil)
	e.points.SetClock(clock)
	e.tasks = boostapp.NewTaskService(gormstore.NewTaskStore(db), e.points, e.rates, sealer)
	e.tasks.SetClock(clock)

	defs := seedDefinitions(t)
	e.starfall, e.starfallCN = e.liveGame("starfall", "星落幻境", "cn-sky", defs, "starfall_main", "starfall_daily", "starfall_abyss", "starfall_idle")
	e.ironcrest, e.ironcrestS1 = e.liveGame("ironcrest", "铁冠王座", "s1", defs, "ironcrest_farm", "ironcrest_march")

	e.vip = e.newUser("vip")
	e.plain = e.newUser("plain")
	e.rates[e.vip.ID] = 90
	e.grant(e.vip.ID, 1000, "seed")
	return e
}

func seedDefinitions(t *testing.T) map[string]json.RawMessage {
	t.Helper()
	data, err := os.ReadFile("testdata/seed_defs.json")
	if err != nil {
		t.Fatal(err)
	}
	var defs map[string]json.RawMessage
	if err := json.Unmarshal(data, &defs); err != nil {
		t.Fatal(err)
	}
	return defs
}

func (e *taskEnv) liveGame(code, name, server string, defs map[string]json.RawMessage, keys ...string) (uint, uint) {
	e.t.Helper()
	game, err := e.catalog.SaveGame(boostapp.GameInput{Code: code, Name: name})
	e.must(err)
	sv, err := e.catalog.SaveServer(boostapp.ServerInput{GameID: game.ID, Code: server, Name: server})
	e.must(err)
	for _, key := range keys {
		saved, err := e.catalog.SaveFeature(boostapp.FeatureInput{GameID: game.ID, Definition: defs[key]})
		e.must(err)
		_, err = e.catalog.SetFeatureStatus(saved.Feature.ID, boostdomain.FeatureStatusActive)
		e.must(err)
		e.features[saved.Feature.Code] = saved.Feature.ID
	}
	_, err = e.catalog.SetGameStatus(game.ID, boostdomain.GameStatusActive)
	e.must(err)
	return game.ID, sv.ID
}

func (e *taskEnv) must(err error) {
	e.t.Helper()
	if err != nil {
		e.t.Fatalf("unexpected error: %v", err)
	}
}

func (e *taskEnv) newUser(name string) userdomain.User {
	e.t.Helper()
	now := time.Now()
	u := userdomain.User{Email: fmt.Sprintf("%s-%d@example.test", name, now.UnixNano()), PasswordHash: "x", Status: constants.UserStatusActive, CreatedAt: now, UpdatedAt: now}
	e.must(e.db.Create(&u).Error)
	return u
}

func (e *taskEnv) grant(userID uint, amount int64, req string) {
	e.t.Helper()
	_, err := e.points.AdminGrant(boostapp.AdminChangeInput{AdminID: 1, UserID: userID, Amount: amount, Reason: "测试", RequestID: req})
	e.must(err)
}

func (e *taskEnv) account(userID uint) boostdomain.PointAccount {
	e.t.Helper()
	var a boostdomain.PointAccount
	if err := e.db.Where("user_id = ?", userID).Take(&a).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return boostdomain.PointAccount{UserID: userID}
		}
		e.t.Fatal(err)
	}
	return a
}

func (e *taskEnv) advance(d time.Duration) { e.clock = e.clock.Add(d) }

// bind 提交一个绑定（不验证）。
func (e *taskEnv) bind(userID, gameID uint, login string) *boostdomain.UserBinding {
	e.t.Helper()
	server := e.starfallCN
	if gameID == e.ironcrest {
		server = e.ironcrestS1
	}
	b, err := e.tasks.CreateBinding(boostapp.CreateBindingInput{UserID: userID, GameID: gameID, ServerID: server, Login: login, Password: "Secret-Pass-001", DisplayName: "主号"})
	e.must(err)
	return b
}

var bindSeq int

// verifiedBinding 提交并让执行端验证成功。
func (e *taskEnv) verifiedBinding(userID, gameID uint) *boostdomain.UserBinding {
	e.t.Helper()
	bindSeq++
	b := e.bind(userID, gameID, fmt.Sprintf("player%d@example.test", bindSeq))
	_, err := e.tasks.Start(boostapp.Exec{Kind: boostdomain.InputTargetBinding, ID: b.ID})
	e.must(err)
	_, err = e.tasks.Finish(boostapp.Exec{Kind: boostdomain.InputTargetBinding, ID: b.ID}, boostapp.FinishInput{SessionRef: "sess"})
	e.must(err)
	return e.binding(b.ID)
}

func (e *taskEnv) binding(id uint) *boostdomain.UserBinding {
	e.t.Helper()
	var b boostdomain.UserBinding
	e.must(e.db.Take(&b, id).Error)
	return &b
}

func (e *taskEnv) task(id uint) *boostdomain.Task {
	e.t.Helper()
	var task boostdomain.Task
	e.must(e.db.Take(&task, id).Error)
	return &task
}

func sweep(entries ...[2]interface{}) []byte {
	stages := make([]map[string]interface{}, len(entries))
	for i, en := range entries {
		stages[i] = map[string]interface{}{"item": en[0], "count": en[1]}
	}
	raw, _ := json.Marshal(map[string]interface{}{"mode": "sweep", "stages": stages})
	return raw
}

func (e *taskEnv) create(userID, bindingID uint, feature string, params string) *boostdomain.Task {
	e.t.Helper()
	res, err := e.tasks.CreateTask(boostapp.CreateTaskInput{UserID: userID, BindingID: bindingID, FeatureID: e.features[feature], Params: []byte(params)})
	e.must(err)
	return res.Task
}

func (e *taskEnv) start(taskID uint) *boostapp.StartedItem {
	e.t.Helper()
	item, err := e.tasks.Start(boostapp.Exec{Kind: boostdomain.InputTargetTask, ID: taskID})
	e.must(err)
	return item
}

func taskExec(id uint) boostapp.Exec { return boostapp.Exec{Kind: boostdomain.InputTargetTask, ID: id} }

func intp(n int) *int { return &n }

// steps 按位置生成每一步的完成数：steps(10, 8) = 第 1 步 10 次、第 2 步 8 次。
func steps(done ...int64) []boostapp.StepDone {
	out := make([]boostapp.StepDone, len(done))
	for i, d := range done {
		out[i] = boostapp.StepDone{Seq: i + 1, Done: d}
	}
	return out
}

// checkTaskInvariants AGENTS.md §3.2 / §3.3：积分不变量；冻结 = 未结束任务冻结之和；
// 已结束任务 消耗 + 退回 = 冻结；同一绑定最多一个执行中任务。
func (e *taskEnv) checkTaskInvariants() {
	e.t.Helper()
	(&pointsEnv{t: e.t, db: e.db}).checkInvariants()
	var accounts []boostdomain.PointAccount
	e.must(e.db.Find(&accounts).Error)
	for _, a := range accounts {
		var held int64
		e.must(e.db.Model(&boostdomain.Task{}).Where("user_id = ? AND status IN ?", a.UserID,
			[]string{boostdomain.TaskStatusPending, boostdomain.TaskStatusProcessing, boostdomain.TaskStatusWaitInput}).
			Select("COALESCE(SUM(points_held),0)").Scan(&held).Error)
		if held != a.Frozen {
			e.t.Fatalf("user %d frozen=%d but unfinished tasks hold %d", a.UserID, a.Frozen, held)
		}
	}
	var bad int64
	e.must(e.db.Model(&boostdomain.Task{}).Where("status IN ? AND points_consumed + points_released <> points_held",
		[]string{boostdomain.TaskStatusSuccess, boostdomain.TaskStatusFail, boostdomain.TaskStatusCancelled}).Count(&bad).Error)
	if bad > 0 {
		e.t.Fatalf("%d finished tasks violate consumed + released = held", bad)
	}
	var dup int64
	e.must(e.db.Raw("SELECT COUNT(*) FROM (SELECT binding_id FROM boost_tasks WHERE status = 'PROCESSING' GROUP BY binding_id HAVING COUNT(*) > 1) x").Scan(&dup).Error)
	if dup > 0 {
		e.t.Fatalf("a binding has more than one PROCESSING task")
	}
}

func wantCode(t *testing.T, err error, kind error) {
	t.Helper()
	if !errors.Is(err, kind) {
		t.Fatalf("err = %v, want %v", err, kind)
	}
}
