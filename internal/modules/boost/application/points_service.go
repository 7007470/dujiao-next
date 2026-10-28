package application

import (
	"encoding/json"
	"fmt"
	"sort"
	"strings"
	"time"
	"unicode/utf8"

	boostcontract "github.com/dujiao-next/internal/modules/boost/contract"
	boostdomain "github.com/dujiao-next/internal/modules/boost/domain"
)

// PointsService 积分账本用例，规则与原型 boost.js §5 一致：
// 可用 = 有效批次剩余之和 = 流水合计；冻结 = 进行中任务冻结之和；都不为负；
// 每笔变化有唯一 biz_key，重复请求返回原流水；消耗按到期时间先进先出，永久积分最后用；
// 任务退回的积分回到原批次，批次已过期 / 已撤回则立即失效。
type PointsService struct {
	repo   boostcontract.PointsRepository
	orders boostcontract.OrderPoints // 积分包订单（可为 nil：只用账本的测试）
	now    func() time.Time
}

// NewPointsService 创建积分用例。orders 为 nil 时积分包订单相关方法返回 ErrOrderNotPointsOrder。
func NewPointsService(repo boostcontract.PointsRepository, orders boostcontract.OrderPoints) *PointsService {
	if repo == nil {
		panic("boost points service: repo is nil")
	}
	return &PointsService{repo: repo, orders: orders, now: time.Now}
}

// SetClock 替换时钟（测试用）。
func (s *PointsService) SetClock(now func() time.Time) {
	if now != nil {
		s.now = now
	}
}

// ExpiringWindow 用户端"即将到期"的窗口。
const ExpiringWindow = 30 * 24 * time.Hour

const maxPointsPerChange = 100_000_000

// ---------------------------------------------------------------------------
// 账本原语（都要求在事务里、已 LockAccount）
// ---------------------------------------------------------------------------

func encodeAllocations(list []boostcontract.LotAllocation) string {
	if len(list) == 0 {
		return "[]"
	}
	data, _ := json.Marshal(list)
	return string(data)
}

// DecodeAllocations 解析流水的批次分配。
func DecodeAllocations(raw string) []boostcontract.LotAllocation {
	var list []boostcontract.LotAllocation
	if strings.TrimSpace(raw) == "" {
		return nil
	}
	_ = json.Unmarshal([]byte(raw), &list)
	return list
}

type ledgerInput struct {
	Type        string
	DAvailable  int64
	DFrozen     int64
	BizKey      string
	Allocations []boostcontract.LotAllocation
	TaskID      *uint
	OrderID     *uint
	AdminID     *uint
	Remark      string
}

// applyLedger 改账户并写一条流水。余额变负返回 ErrPointsInsufficient（整个事务回滚）。
func (s *PointsService) applyLedger(repo boostcontract.PointsRepository, account *boostdomain.PointAccount, in ledgerInput) (*boostdomain.PointLedgerEntry, error) {
	account.Available += in.DAvailable
	account.Frozen += in.DFrozen
	if account.Available < 0 || account.Frozen < 0 {
		return nil, boostcontract.ErrPointsInsufficient
	}
	if err := repo.SaveAccount(account); err != nil {
		return nil, err
	}
	entry := &boostdomain.PointLedgerEntry{
		UserID:             account.UserID,
		Type:               in.Type,
		DeltaAvailable:     in.DAvailable,
		DeltaFrozen:        in.DFrozen,
		AvailableAfter:     account.Available,
		FrozenAfter:        account.Frozen,
		LotAllocationsJSON: encodeAllocations(in.Allocations),
		BizKey:             in.BizKey,
		TaskID:             in.TaskID,
		OrderID:            in.OrderID,
		OperatorAdminID:    in.AdminID,
		Remark:             truncateRunes(in.Remark, 120),
		CreatedAt:          s.now(),
	}
	if err := repo.CreateLedger(entry); err != nil {
		return nil, err
	}
	return entry, nil
}

// expireDueForUser 把该用户已到期的有效批次清零（写 expire 流水）。在扣点、冻结、查询前调用，
// 保证即使定时任务没跑，过期积分也不会被花掉。
func (s *PointsService) expireDueForUser(repo boostcontract.PointsRepository, account *boostdomain.PointAccount, now time.Time) (int, error) {
	lots, err := repo.DueLots(account.UserID, now, 1000)
	if err != nil {
		return 0, err
	}
	n := 0
	for i := range lots {
		lot := &lots[i]
		amount := lot.Remaining
		lot.Status = boostdomain.LotStatusExpired
		lot.Remaining = 0
		if err := repo.SaveLot(lot); err != nil {
			return n, err
		}
		if amount <= 0 {
			continue
		}
		if _, err := s.applyLedger(repo, account, ledgerInput{
			Type:        boostdomain.LedgerExpire,
			DAvailable:  -amount,
			BizKey:      fmt.Sprintf("expire:lot:%d", lot.ID),
			Allocations: []boostcontract.LotAllocation{{LotID: lot.ID, Amount: amount}},
			Remark:      "积分到期",
		}); err != nil {
			return n, err
		}
		n++
	}
	return n, nil
}

// takeFromLots 按 FIFO 从批次里扣 amount 点，返回分配明细。不够时返回 ErrPointsInsufficient。
func (s *PointsService) takeFromLots(repo boostcontract.PointsRepository, userID uint, amount int64) ([]boostcontract.LotAllocation, error) {
	lots, err := repo.ActiveLotsFIFO(userID)
	if err != nil {
		return nil, err
	}
	left := amount
	var out []boostcontract.LotAllocation
	for i := range lots {
		if left <= 0 {
			break
		}
		lot := &lots[i]
		take := lot.Remaining
		if take > left {
			take = left
		}
		lot.Remaining -= take
		if lot.Remaining == 0 {
			lot.Status = boostdomain.LotStatusExhausted
		}
		if err := repo.SaveLot(lot); err != nil {
			return nil, err
		}
		out = append(out, boostcontract.LotAllocation{LotID: lot.ID, Amount: take})
		left -= take
	}
	if left > 0 {
		return nil, &ValidationError{Kind: boostcontract.ErrPointsInsufficient, Msg: fmt.Sprintf("可用积分不足，还差 %d 点", left)}
	}
	return out, nil
}

type grantInput struct {
	UserID     uint
	Amount     int64
	SourceType string
	SourceRef  string
	ValidDays  *int
	Remark     string
	AdminID    *uint
	OrderID    *uint
}

// grantInTx 新建批次并写入账流水；biz_key = {source_type}:{source_ref}，重复返回原流水。
func (s *PointsService) grantInTx(repo boostcontract.PointsRepository, in grantInput) (*boostdomain.PointLedgerEntry, error) {
	bizKey := in.SourceType + ":" + in.SourceRef
	if existing, err := repo.LedgerByKey(bizKey); err != nil || existing != nil {
		return existing, err
	}
	if in.Amount <= 0 || in.Amount > maxPointsPerChange {
		return nil, invalid(boostcontract.ErrPointsInvalid, "积分必须是 1–%d 的整数", maxPointsPerChange)
	}
	account, err := repo.LockAccount(in.UserID)
	if err != nil {
		return nil, err
	}
	now := s.now()
	lot := &boostdomain.PointLot{
		UserID:     in.UserID,
		SourceType: in.SourceType,
		SourceRef:  in.SourceRef,
		Amount:     in.Amount,
		Remaining:  in.Amount,
		Status:     boostdomain.LotStatusActive,
	}
	if in.ValidDays != nil && *in.ValidDays > 0 {
		at := now.Add(time.Duration(*in.ValidDays) * 24 * time.Hour)
		lot.ExpiresAt = &at
	}
	if err := repo.CreateLot(lot); err != nil {
		return nil, err
	}
	typ := boostdomain.LedgerGrant
	if in.SourceType == boostdomain.LotSourceOrder {
		typ = boostdomain.LedgerPurchase
	}
	return s.applyLedger(repo, account, ledgerInput{
		Type:        typ,
		DAvailable:  in.Amount,
		BizKey:      bizKey,
		Allocations: []boostcontract.LotAllocation{{LotID: lot.ID, Amount: in.Amount}},
		AdminID:     in.AdminID,
		OrderID:     in.OrderID,
		Remark:      in.Remark,
	})
}

// HoldPoints 冻结任务积分（FIFO 扣批次，写 hold 流水）。必须和创建任务在同一事务里调用（S5）。
// 重复调用返回原流水。
func (s *PointsService) HoldPoints(repo boostcontract.PointsRepository, userID uint, amount int64, taskID uint) (*boostdomain.PointLedgerEntry, error) {
	bizKey := fmt.Sprintf("hold:task:%d", taskID)
	if existing, err := repo.LedgerByKey(bizKey); err != nil || existing != nil {
		return existing, err
	}
	if amount < 0 || amount > maxPointsPerChange {
		return nil, invalid(boostcontract.ErrPointsInvalid, "冻结积分数量不合法")
	}
	account, err := repo.LockAccount(userID)
	if err != nil {
		return nil, err
	}
	if _, err := s.expireDueForUser(repo, account, s.now()); err != nil {
		return nil, err
	}
	if account.Available < amount {
		return nil, &ValidationError{Kind: boostcontract.ErrPointsInsufficient, Msg: fmt.Sprintf("需要 %d 点，可用 %d 点", amount, account.Available)}
	}
	allocations, err := s.takeFromLots(repo, userID, amount)
	if err != nil {
		return nil, err
	}
	tid := taskID
	return s.applyLedger(repo, account, ledgerInput{
		Type:        boostdomain.LedgerHold,
		DAvailable:  -amount,
		DFrozen:     amount,
		BizKey:      bizKey,
		Allocations: allocations,
		TaskID:      &tid,
	})
}

// SettleResult 任务结算结果。
type SettleResult struct {
	Consumed int64
	Released int64
	Already  bool
}

// SettleTaskPoints 任务终态结算：consumed 从冻结里扣掉（截断到 [0, held]），其余按冻结时批次的逆序退回；
// 退回的批次已过期或已撤回则立即失效（写 expire / revoke 流水）。只结算一次。
func (s *PointsService) SettleTaskPoints(repo boostcontract.PointsRepository, userID, taskID uint, held, consumed int64, reason string) (*SettleResult, error) {
	consumeKey := fmt.Sprintf("consume:task:%d", taskID)
	releaseKey := fmt.Sprintf("release:task:%d", taskID)
	hold, err := repo.LedgerByKey(fmt.Sprintf("hold:task:%d", taskID))
	if err != nil {
		return nil, err
	}
	if hold == nil {
		return nil, invalid(boostcontract.ErrInvalid, "任务 %d 没有冻结记录", taskID)
	}
	account, err := repo.LockAccount(userID)
	if err != nil {
		return nil, err
	}
	// 账户锁之后再查，避免并发重复结算
	for _, key := range []string{consumeKey, releaseKey} {
		existing, err := repo.LedgerByKey(key)
		if err != nil {
			return nil, err
		}
		if existing != nil {
			return &SettleResult{Already: true}, nil
		}
	}
	if consumed < 0 {
		consumed = 0
	}
	if consumed > held {
		consumed = held
	}
	release := held - consumed
	tid := taskID
	if held == 0 {
		// 0 点任务也留一条记录，保证"只结算一次"
		if _, err := s.applyLedger(repo, account, ledgerInput{Type: boostdomain.LedgerRelease, BizKey: releaseKey, TaskID: &tid, Remark: reason}); err != nil {
			return nil, err
		}
		return &SettleResult{}, nil
	}
	if consumed > 0 {
		if _, err := s.applyLedger(repo, account, ledgerInput{Type: boostdomain.LedgerConsume, DFrozen: -consumed, BizKey: consumeKey, TaskID: &tid, Remark: reason}); err != nil {
			return nil, err
		}
	}
	if release > 0 {
		holdAlloc := DecodeAllocations(hold.LotAllocationsJSON)
		var back []boostcontract.LotAllocation
		left := release
		// 消耗视为从最早到期的批次扣，所以退回从最后分配的批次开始
		for i := len(holdAlloc) - 1; i >= 0 && left > 0; i-- {
			amt := holdAlloc[i].Amount
			if amt > left {
				amt = left
			}
			back = append(back, boostcontract.LotAllocation{LotID: holdAlloc[i].LotID, Amount: amt})
			left -= amt
		}
		if _, err := s.applyLedger(repo, account, ledgerInput{Type: boostdomain.LedgerRelease, DAvailable: release, DFrozen: -release, BizKey: releaseKey, Allocations: back, TaskID: &tid, Remark: reason}); err != nil {
			return nil, err
		}
		now := s.now()
		for _, a := range back {
			lot, err := repo.GetLot(a.LotID)
			if err != nil {
				return nil, err
			}
			if lot == nil {
				return nil, fmt.Errorf("boost points: lot %d missing", a.LotID)
			}
			lot.Remaining += a.Amount
			if lot.Status == boostdomain.LotStatusExhausted {
				lot.Status = boostdomain.LotStatusActive
			}
			dead := lot.Status == boostdomain.LotStatusExpired || lot.Status == boostdomain.LotStatusRevoked ||
				(lot.ExpiresAt != nil && !lot.ExpiresAt.After(now))
			typ := boostdomain.LedgerExpire
			if dead {
				if lot.Status == boostdomain.LotStatusRevoked {
					typ = boostdomain.LedgerRevoke
				} else {
					lot.Status = boostdomain.LotStatusExpired
				}
				lot.Remaining -= a.Amount
			}
			if err := repo.SaveLot(lot); err != nil {
				return nil, err
			}
			if dead {
				if _, err := s.applyLedger(repo, account, ledgerInput{
					Type:        typ,
					DAvailable:  -a.Amount,
					BizKey:      fmt.Sprintf("%s:lot:%d:task:%d", typ, lot.ID, taskID),
					Allocations: []boostcontract.LotAllocation{{LotID: lot.ID, Amount: a.Amount}},
					Remark:      "退回的积分所在批次已失效",
				}); err != nil {
					return nil, err
				}
			}
		}
	}
	return &SettleResult{Consumed: consumed, Released: release}, nil
}

// ---------------------------------------------------------------------------
// 管理员发放 / 扣减
// ---------------------------------------------------------------------------

// AdminChangeInput 管理员发放或扣减积分。RequestID 由前端每次打开对话框生成，用于防重复提交。
type AdminChangeInput struct {
	AdminID   uint
	UserID    uint
	Amount    int64
	ValidDays *int // 只对发放有效；nil 或 0 = 永久
	Reason    string
	RequestID string
}

func (in AdminChangeInput) validate() error {
	if in.AdminID == 0 || in.UserID == 0 {
		return invalid(boostcontract.ErrInvalid, "缺少管理员或用户")
	}
	if in.Amount <= 0 || in.Amount > maxPointsPerChange {
		return invalid(boostcontract.ErrPointsInvalid, "积分必须是 1–%d 的整数", maxPointsPerChange)
	}
	reason := strings.TrimSpace(in.Reason)
	if reason == "" || utf8.RuneCountInString(reason) > 100 {
		return invalid(boostcontract.ErrInvalid, "必须填写原因（最多 100 字）")
	}
	id := strings.TrimSpace(in.RequestID)
	if id == "" || len(id) > 40 { // source_ref 是 varchar(64)，前缀 admin:{id}: 占一部分
		return invalid(boostcontract.ErrInvalid, "缺少请求号")
	}
	if in.ValidDays != nil && (*in.ValidDays < 0 || *in.ValidDays > 3650) {
		return invalid(boostcontract.ErrInvalid, "有效天数需为 0–3650")
	}
	return nil
}

// AdminGrant 管理员发放积分（新批次）。同一管理员同一请求号只发一次。
func (s *PointsService) AdminGrant(in AdminChangeInput) (*boostdomain.PointLedgerEntry, error) {
	if err := in.validate(); err != nil {
		return nil, err
	}
	var out *boostdomain.PointLedgerEntry
	err := s.repo.WithinTransaction(func(repo boostcontract.PointsRepository) error {
		admin := in.AdminID
		entry, err := s.grantInTx(repo, grantInput{
			UserID:     in.UserID,
			Amount:     in.Amount,
			SourceType: boostdomain.LotSourceGrant,
			SourceRef:  fmt.Sprintf("admin:%d:%s", in.AdminID, strings.TrimSpace(in.RequestID)),
			ValidDays:  in.ValidDays,
			Remark:     strings.TrimSpace(in.Reason),
			AdminID:    &admin,
		})
		out = entry
		return err
	})
	return out, err
}

// AdminDeduct 管理员扣减可用积分（FIFO）。冻结中的积分不能扣。
func (s *PointsService) AdminDeduct(in AdminChangeInput) (*boostdomain.PointLedgerEntry, error) {
	if err := in.validate(); err != nil {
		return nil, err
	}
	bizKey := fmt.Sprintf("admin_adjust:%d:%s", in.AdminID, strings.TrimSpace(in.RequestID))
	var out *boostdomain.PointLedgerEntry
	err := s.repo.WithinTransaction(func(repo boostcontract.PointsRepository) error {
		if existing, err := repo.LedgerByKey(bizKey); err != nil || existing != nil {
			out = existing
			return err
		}
		account, err := repo.LockAccount(in.UserID)
		if err != nil {
			return err
		}
		if _, err := s.expireDueForUser(repo, account, s.now()); err != nil {
			return err
		}
		if account.Available < in.Amount {
			return &ValidationError{Kind: boostcontract.ErrPointsInsufficient, Msg: fmt.Sprintf("可用积分只有 %d 点（冻结中的不能扣）", account.Available)}
		}
		allocations, err := s.takeFromLots(repo, in.UserID, in.Amount)
		if err != nil {
			return err
		}
		admin := in.AdminID
		out, err = s.applyLedger(repo, account, ledgerInput{
			Type:        boostdomain.LedgerAdminAdjust,
			DAvailable:  -in.Amount,
			BizKey:      bizKey,
			Allocations: allocations,
			AdminID:     &admin,
			Remark:      strings.TrimSpace(in.Reason),
		})
		return err
	})
	return out, err
}

// ---------------------------------------------------------------------------
// 到期
// ---------------------------------------------------------------------------

// ExpireDue 处理所有到期批次，返回失效的批次数。按用户分组，每个用户一个事务。
func (s *PointsService) ExpireDue(limit int) (int, error) {
	if limit <= 0 {
		limit = 500
	}
	now := s.now()
	lots, err := s.repo.DueLots(0, now, limit)
	if err != nil {
		return 0, err
	}
	users := map[uint]struct{}{}
	for _, lot := range lots {
		users[lot.UserID] = struct{}{}
	}
	ids := make([]uint, 0, len(users))
	for id := range users {
		ids = append(ids, id)
	}
	sort.Slice(ids, func(i, j int) bool { return ids[i] < ids[j] })
	total := 0
	for _, userID := range ids {
		err := s.repo.WithinTransaction(func(repo boostcontract.PointsRepository) error {
			account, err := repo.LockAccount(userID)
			if err != nil {
				return err
			}
			n, err := s.expireDueForUser(repo, account, now)
			total += n
			return err
		})
		if err != nil {
			return total, err
		}
	}
	return total, nil
}

// ---------------------------------------------------------------------------
// 查询
// ---------------------------------------------------------------------------

// ExpiringLot 即将到期的批次。
type ExpiringLot struct {
	LotID      uint      `json:"lot_id"`
	Amount     int64     `json:"amount"`
	ExpiresAt  time.Time `json:"expires_at"`
	SourceType string    `json:"source_type"`
}

// PointsSummary 用户积分概览。
type PointsSummary struct {
	Available    int64         `json:"available"`
	Frozen       int64         `json:"frozen"`
	ExpiringSoon []ExpiringLot `json:"expiring_soon"`
}

// Summary 返回用户积分概览（先把该用户已到期的批次处理掉）。
func (s *PointsService) Summary(userID uint) (*PointsSummary, error) {
	if userID == 0 {
		return nil, boostcontract.ErrInvalid
	}
	now := s.now()
	due, err := s.repo.DueLots(userID, now, 1)
	if err != nil {
		return nil, err
	}
	if len(due) > 0 {
		if err := s.repo.WithinTransaction(func(repo boostcontract.PointsRepository) error {
			account, err := repo.LockAccount(userID)
			if err != nil {
				return err
			}
			_, err = s.expireDueForUser(repo, account, now)
			return err
		}); err != nil {
			return nil, err
		}
	}
	account, err := s.repo.GetAccount(userID)
	if err != nil {
		return nil, err
	}
	out := &PointsSummary{ExpiringSoon: []ExpiringLot{}}
	if account != nil {
		out.Available, out.Frozen = account.Available, account.Frozen
	}
	lots, err := s.repo.ActiveLotsFIFO(userID)
	if err != nil {
		return nil, err
	}
	for _, lot := range lots {
		if lot.ExpiresAt != nil && lot.ExpiresAt.Sub(now) <= ExpiringWindow {
			out.ExpiringSoon = append(out.ExpiringSoon, ExpiringLot{LotID: lot.ID, Amount: lot.Remaining, ExpiresAt: *lot.ExpiresAt, SourceType: lot.SourceType})
		}
	}
	return out, nil
}

// Ledger 分页查询流水。
func (s *PointsService) Ledger(filter boostcontract.LedgerFilter) ([]boostdomain.PointLedgerEntry, int64, error) {
	return s.repo.ListLedger(filter)
}

// Lots 查询用户批次。includeClosed 为 false 时只返回有剩余的有效批次。
func (s *PointsService) Lots(userID uint, includeClosed bool) ([]boostdomain.PointLot, error) {
	if userID == 0 {
		return nil, boostcontract.ErrInvalid
	}
	return s.repo.ListLots(userID, includeClosed)
}

// Accounts 后台积分账户列表。
func (s *PointsService) Accounts(filter boostcontract.AccountFilter) ([]boostcontract.AccountRow, int64, error) {
	return s.repo.ListAccounts(filter)
}
