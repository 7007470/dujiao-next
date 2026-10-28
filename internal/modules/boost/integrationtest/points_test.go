package integrationtest

import (
	"errors"
	"sync"
	"testing"
	"time"

	boostapp "github.com/dujiao-next/internal/modules/boost/application"
	boostcontract "github.com/dujiao-next/internal/modules/boost/contract"
	boostdomain "github.com/dujiao-next/internal/modules/boost/domain"

	"github.com/dujiao-next/internal/constants"
	fulfillmentdomain "github.com/dujiao-next/internal/modules/fulfillment/domain"
	orderapp "github.com/dujiao-next/internal/modules/order/application"
	orderrefund "github.com/dujiao-next/internal/modules/order/application/refund"
	orderdomain "github.com/dujiao-next/internal/modules/order/domain"
	"github.com/dujiao-next/internal/shared/money"

	"github.com/shopspring/decimal"
)

// 对应原型 engine.test.js「积分包：余额支付直接到账；在线支付重复回调只到账一次；超时取消退余额」，
// 这里跑的是真实的 Dujiao 下单 / 支付 / 取消代码。

func TestPointPackOnlinePaymentGrantsOnceOnDuplicateCallbacks(t *testing.T) {
	e := newPointsEnv(t)
	product, sku, _ := e.newPackProduct("p1000", 100, 1000, 100, days(365))
	order := e.buy(e.user.ID, product, sku, 2)

	_, cb := e.payOnline(order)
	// 重复回调（网关重试）
	for i := 0; i < 3; i++ {
		if _, err := e.payments.HandleCallback(cb); err != nil {
			t.Fatalf("duplicate callback: %v", err)
		}
	}
	// 补发任务也再跑一次
	if _, err := e.points.RetryUngranted(0, 10); err != nil {
		t.Fatalf("retry: %v", err)
	}
	if _, err := e.points.HandleOrderPaid(order.ID); err != nil {
		t.Fatalf("handle again: %v", err)
	}

	s := e.summary(e.user.ID)
	if s.Available != 2200 || s.Frozen != 0 {
		t.Fatalf("want 2200 available, got %+v", s)
	}
	if n := e.ledgerCount(e.user.ID, boostdomain.LedgerPurchase); n != 1 {
		t.Fatalf("purchase ledger rows = %d, want 1", n)
	}
	// 子订单完成、交付记录类型、父订单同步
	parent := e.reloadOrder(order.ID)
	if len(parent.Children) != 1 {
		t.Fatalf("want 1 child order, got %d", len(parent.Children))
	}
	child := parent.Children[0]
	if child.Status != constants.OrderStatusCompleted || parent.Status != constants.OrderStatusCompleted {
		t.Fatalf("order status parent=%s child=%s, want completed", parent.Status, child.Status)
	}
	var f fulfillmentdomain.Fulfillment
	e.must(e.db.Where("order_id = ?", child.ID).First(&f).Error)
	if f.Type != constants.FulfillmentTypeBoostPoints || f.Payload != "已到账 2200 积分" {
		t.Fatalf("fulfillment = %+v", f)
	}
	// 批次带到期时间
	var lot boostdomain.PointLot
	e.must(e.db.Where("user_id = ?", e.user.ID).First(&lot).Error)
	if lot.ExpiresAt == nil || lot.SourceType != boostdomain.LotSourceOrder {
		t.Fatalf("lot = %+v", lot)
	}
	e.checkInvariants()
}

func TestPointPackWalletPaymentGrantsImmediately(t *testing.T) {
	e := newPointsEnv(t)
	product, sku, _ := e.newPackProduct("p500", 50, 500, 0, nil)
	e.setBalance(e.user.ID, 80)
	order := e.buy(e.user.ID, product, sku, 1)
	e.payWallet(order)

	s := e.summary(e.user.ID)
	if s.Available != 500 {
		t.Fatalf("want 500, got %+v", s)
	}
	var lot boostdomain.PointLot
	e.must(e.db.Where("user_id = ?", e.user.ID).First(&lot).Error)
	if lot.ExpiresAt != nil {
		t.Fatalf("pack without valid_days must be permanent, got %v", lot.ExpiresAt)
	}
	e.checkInvariants()
}

func TestPointPackTimeoutCancelGrantsNothing(t *testing.T) {
	e := newPointsEnv(t)
	product, sku, _ := e.newPackProduct("p100", 10, 100, 0, nil)
	order := e.buy(e.user.ID, product, sku, 1)
	past := time.Now().Add(-time.Minute)
	e.must(e.db.Model(&orderdomain.Order{}).Where("id = ? OR parent_id = ?", order.ID, order.ID).Update("expires_at", past).Error)
	if _, err := e.orders.CancelExpiredOrder(order.ID); err != nil {
		t.Fatalf("cancel expired: %v", err)
	}
	if n, err := e.points.RetryUngranted(0, 10); err != nil || n != 0 {
		t.Fatalf("retry after cancel: n=%d err=%v", n, err)
	}
	if s := e.summary(e.user.ID); s.Available != 0 {
		t.Fatalf("canceled order must not grant points: %+v", s)
	}
	if got := e.reloadOrder(order.ID).Status; got != constants.OrderStatusCanceled {
		t.Fatalf("status = %s", got)
	}
}

func TestPointPackGuestCannotBuy(t *testing.T) {
	e := newPointsEnv(t)
	product, sku, _ := e.newPackProduct("pguest", 10, 100, 0, nil)
	_, err := e.orders.CreateGuestOrder(orderapp.CreateGuestOrderInput{
		Email: "guest@example.test", OrderPassword: "Guest-Pass-123", Items: []orderapp.CreateOrderItem{{ProductID: product.ID, SKUID: sku.ID, Quantity: 1}},
	})
	if !errors.Is(err, orderapp.ErrProductPurchaseNotAllowed) {
		t.Fatalf("guest purchase err = %v, want ErrProductPurchaseNotAllowed", err)
	}
}

func TestGrantFailureIsRetriedByMaintenance(t *testing.T) {
	e := newPointsEnv(t)
	product, sku, pack := e.newPackProduct("pretry", 10, 100, 0, nil)
	// 模拟积分包配置被误删：支付成功但到账失败（不影响支付本身）
	e.must(e.db.Delete(&boostdomain.PointPack{}, pack.ID).Error)
	order := e.buy(e.user.ID, product, sku, 1)
	e.payOnline(order)
	if s := e.summary(e.user.ID); s.Available != 0 {
		t.Fatalf("should not grant without pack: %+v", s)
	}
	if got := e.reloadOrder(order.ID).Status; got != constants.OrderStatusPaid {
		t.Fatalf("order should stay paid, got %s", got)
	}
	// 管理员恢复配置后，定时补发到账
	_, err := e.points.SavePack(boostapp.PackInput{SKUID: sku.ID, Points: 100})
	e.must(err)
	e.clock = time.Now().Add(time.Minute) // 补发只处理支付超过宽限期的订单
	n, err := e.points.RetryUngranted(0, 10)
	if err != nil || n != 1 {
		t.Fatalf("retry: n=%d err=%v", n, err)
	}
	if s := e.summary(e.user.ID); s.Available != 100 {
		t.Fatalf("after retry: %+v", s)
	}
}

func TestPackRequiresBoostPointsProductAndUniqueSKU(t *testing.T) {
	e := newPointsEnv(t)
	_, sku, _ := e.newPackProduct("pdup", 10, 100, 0, nil)
	if _, err := e.points.SavePack(boostapp.PackInput{SKUID: sku.ID, Points: 1}); !errors.Is(err, boostcontract.ErrPackDuplicate) {
		t.Fatalf("dup sku err = %v", err)
	}
	// 普通商品不能配积分包
	e.must(e.db.Exec("UPDATE products SET fulfillment_type = ? WHERE id = ?", constants.FulfillmentTypeManual, sku.ProductID).Error)
	e.must(e.db.Exec("DELETE FROM boost_point_packs").Error)
	if _, err := e.points.SavePack(boostapp.PackInput{SKUID: sku.ID, Points: 1}); !errors.Is(err, boostcontract.ErrPackProductInvalid) {
		t.Fatalf("manual product err = %v", err)
	}
}

// 对应原型「积分 FIFO：先用快到期的；到期定时失效；发放幂等」。
func TestFIFOExpiryAndIdempotentGrant(t *testing.T) {
	e := newPointsEnv(t)
	u := e.user.ID
	first := e.grant(u, 100, days(10), "r1")
	again := e.grant(u, 100, days(10), "r1")
	if first.ID != again.ID {
		t.Fatalf("same request id must return the same ledger row")
	}
	e.grant(u, 50, days(3), "r2")
	e.grant(u, 30, nil, "r3") // 永久，最后用
	if s := e.summary(u); s.Available != 180 {
		t.Fatalf("available = %d", s.Available)
	}

	// 冻结 60：先用 3 天到期的 50，再用 10 天的 10
	e.must(e.hold(u, 60, 1))
	var lots []boostdomain.PointLot
	e.must(e.db.Where("user_id = ?", u).Order("id").Find(&lots).Error)
	if lots[0].Remaining != 90 || lots[1].Remaining != 0 || lots[1].Status != boostdomain.LotStatusExhausted || lots[2].Remaining != 30 {
		t.Fatalf("fifo wrong: %+v", lots)
	}

	// 4 天后：3 天批次已用完，不产生到期；10 天批次还在
	e.clock = e.clock.Add(4 * 24 * time.Hour)
	n, err := e.points.ExpireDue(100)
	e.must(err)
	if n != 0 {
		t.Fatalf("expired %d lots, want 0", n)
	}
	// 11 天后：10 天批次剩 90 到期
	e.clock = e.clock.Add(7 * 24 * time.Hour)
	n, err = e.points.ExpireDue(100)
	e.must(err)
	if n != 1 {
		t.Fatalf("expired %d lots, want 1", n)
	}
	s := e.summary(u)
	if s.Available != 30 || s.Frozen != 60 {
		t.Fatalf("after expiry: %+v", s)
	}
	// 重复扫描不重复扣
	n, _ = e.points.ExpireDue(100)
	if n != 0 || e.summary(u).Available != 30 {
		t.Fatalf("expire must be idempotent")
	}
	e.checkInvariants()
}

// 退回到已过期批次：立即失效；退回到有效批次：回到原批次。
func TestSettleReturnsToOriginalLotsAndExpiredLotsDie(t *testing.T) {
	e := newPointsEnv(t)
	u := e.user.ID
	e.grant(u, 50, days(2), "short")
	e.grant(u, 100, nil, "forever")
	e.must(e.hold(u, 120, 7)) // 50（短期）+ 70（永久）
	// 冻结期间短期批次到期
	e.clock = e.clock.Add(3 * 24 * time.Hour)
	r := e.settle(u, 7, 120, 20) // 用掉 20，退回 100：先退永久批次 70，再退短期批次 30（已过期 → 失效）
	if r.Consumed != 20 || r.Released != 100 {
		t.Fatalf("settle = %+v", r)
	}
	s := e.summary(u)
	if s.Available != 100 || s.Frozen != 0 {
		t.Fatalf("after settle: %+v (want 30 永久剩余 + 70 退回 = 100)", s)
	}
	if n := e.ledgerCount(u, boostdomain.LedgerExpire); n != 1 {
		t.Fatalf("expire rows = %d, want 1（退回到已过期批次）", n)
	}
	// 只结算一次
	if again := e.settle(u, 7, 120, 0); !again.Already {
		t.Fatalf("second settle must be a no-op")
	}
	// 超量完成被截断
	e.must(e.hold(u, 10, 8))
	if r := e.settle(u, 8, 10, 999); r.Consumed != 10 || r.Released != 0 {
		t.Fatalf("overrun not clamped: %+v", r)
	}
	e.checkInvariants()
}

func TestHoldInsufficientAndIdempotent(t *testing.T) {
	e := newPointsEnv(t)
	u := e.user.ID
	e.grant(u, 100, nil, "g")
	if err := e.hold(u, 101, 1); !errors.Is(err, boostcontract.ErrPointsInsufficient) {
		t.Fatalf("err = %v", err)
	}
	e.must(e.hold(u, 60, 2))
	e.must(e.hold(u, 60, 2)) // 同一任务重复冻结
	if s := e.summary(u); s.Available != 40 || s.Frozen != 60 {
		t.Fatalf("summary = %+v", s)
	}
	// 过期积分不能被冻结（即使定时任务还没跑）
	e.grant(u, 500, days(1), "soon")
	e.clock = e.clock.Add(2 * 24 * time.Hour)
	if err := e.hold(u, 100, 3); !errors.Is(err, boostcontract.ErrPointsInsufficient) {
		t.Fatalf("expired points must not be spendable, err = %v", err)
	}
	e.checkInvariants()
}

// 并发冻结同一账户不超额。SQLite 下靠单写事务串行；PostgreSQL 靠 FOR UPDATE 行锁（见 docs/decisions/004）。
func TestConcurrentHoldsNeverOverdraw(t *testing.T) {
	e := newPointsEnv(t)
	u := e.user.ID
	e.grant(u, 100, nil, "g")
	var wg sync.WaitGroup
	var mu sync.Mutex
	ok := 0
	for i := 0; i < 20; i++ {
		wg.Add(1)
		go func(task uint) {
			defer wg.Done()
			if err := e.hold(u, 30, task); err == nil {
				mu.Lock()
				ok++
				mu.Unlock()
			}
		}(uint(100 + i))
	}
	wg.Wait()
	if ok != 3 {
		t.Fatalf("%d holds succeeded, want 3", ok)
	}
	if s := e.summary(u); s.Available != 10 || s.Frozen != 90 {
		t.Fatalf("summary = %+v", s)
	}
	e.checkInvariants()
}

func TestAdminGrantDeductIdempotentAndValidated(t *testing.T) {
	e := newPointsEnv(t)
	u := e.user.ID
	in := boostapp.AdminChangeInput{AdminID: 9, UserID: u, Amount: 200, Reason: "活动补偿", RequestID: "dlg-1"}
	a, err := e.points.AdminGrant(in)
	e.must(err)
	b, err := e.points.AdminGrant(in)
	e.must(err)
	if a.ID != b.ID || a.OperatorAdminID == nil || *a.OperatorAdminID != 9 {
		t.Fatalf("grant not idempotent / missing operator: %+v %+v", a, b)
	}
	// 另一个管理员同一请求号是不同的操作
	in2 := in
	in2.AdminID = 10
	_, err = e.points.AdminGrant(in2)
	e.must(err)

	d := boostapp.AdminChangeInput{AdminID: 9, UserID: u, Amount: 150, Reason: "误发收回", RequestID: "dlg-2"}
	x, err := e.points.AdminDeduct(d)
	e.must(err)
	y, err := e.points.AdminDeduct(d)
	e.must(err)
	if x.ID != y.ID {
		t.Fatalf("deduct not idempotent")
	}
	if s := e.summary(u); s.Available != 250 {
		t.Fatalf("available = %d", s.Available)
	}
	// 冻结中的不能扣
	e.must(e.hold(u, 200, 1))
	if _, err := e.points.AdminDeduct(boostapp.AdminChangeInput{AdminID: 9, UserID: u, Amount: 100, Reason: "x", RequestID: "dlg-3"}); !errors.Is(err, boostcontract.ErrPointsInsufficient) {
		t.Fatalf("deduct frozen err = %v", err)
	}
	// 参数校验
	for _, bad := range []boostapp.AdminChangeInput{
		{AdminID: 9, UserID: u, Amount: 0, Reason: "x", RequestID: "a"},
		{AdminID: 9, UserID: u, Amount: 1, Reason: " ", RequestID: "b"},
		{AdminID: 9, UserID: u, Amount: 1, Reason: "x", RequestID: ""},
	} {
		if _, err := e.points.AdminGrant(bad); err == nil {
			t.Fatalf("invalid input accepted: %+v", bad)
		}
	}
	e.checkInvariants()
}

// 对应原型「积分包退款：先撤回剩余积分（只撤剩余），按比例建议退款，退款幂等」，退款走 Dujiao 真实退款服务。
func TestRefundRequiresRevokeAndSuggestsProportionalAmount(t *testing.T) {
	e := newPointsEnv(t)
	product, sku, _ := e.newPackProduct("prefund", 99, 900, 100, nil) // 99 元 1000 点
	order := e.buy(e.user.ID, product, sku, 1)
	e.payOnline(order)
	child := e.reloadOrder(order.ID).Children[0]

	// 用掉 333 点（冻结后结算完成）
	e.must(e.hold(e.user.ID, 333, 1))
	e.settle(e.user.ID, 1, 333, 333)
	// 另有 100 点还冻结在进行中的任务里
	e.must(e.hold(e.user.ID, 100, 2))

	refundAmount := money.FromDecimal(decimal.NewFromInt(10))
	_, _, err := e.refunds.AdminManualRefund(orderrefund.AdminManualRefundInput{OrderID: child.ID, Amount: refundAmount, Remark: "test"})
	if !errors.Is(err, orderrefund.ErrOrderRefundBlocked) {
		t.Fatalf("manual refund before revoke: %v", err)
	}
	if _, _, _, err := e.refunds.AdminRefundToWallet(orderrefund.AdminRefundToWalletInput{OrderID: child.ID, Amount: refundAmount}); !errors.Is(err, orderrefund.ErrOrderRefundBlocked) {
		t.Fatalf("wallet refund before revoke: %v", err)
	}
	// 父订单也拦截（退款按父订单号发起也不行）
	if _, err := e.orders.UpdateOrderStatus(child.ID, constants.OrderStatusRefunded); !errors.Is(err, orderapp.ErrOrderRefundBlocked) {
		t.Fatalf("status patch to refunded: %v", err)
	}

	res, err := e.points.RevokeForOrder(1, order.OrderNo)
	e.must(err)
	// 剩余 = 1000 - 333 - 100(冻结) = 567；建议 = floor(99 × 567 / 1000, 分) = 56.13
	if res.Total != 1000 || res.Revoked != 567 || res.Used != 433 || res.SuggestRefund.String() != "56.13" {
		t.Fatalf("revoke = %+v suggest=%s", res, res.SuggestRefund.String())
	}
	again, err := e.points.RevokeForOrder(1, order.OrderNo)
	e.must(err)
	if !again.Already || again.SuggestRefund.String() != "56.13" {
		t.Fatalf("second revoke = %+v", again)
	}
	if s := e.summary(e.user.ID); s.Available != 0 || s.Frozen != 100 {
		t.Fatalf("after revoke: %+v", s)
	}
	// 冻结中的 100 点之后退回 → 批次已撤回，立即失效
	e.settle(e.user.ID, 2, 100, 0)
	if s := e.summary(e.user.ID); s.Available != 0 || s.Frozen != 0 {
		t.Fatalf("release into revoked lot must vanish: %+v", s)
	}

	// 撤回后可以退款
	_, record, err := e.refunds.AdminManualRefund(orderrefund.AdminManualRefundInput{OrderID: child.ID, Amount: res.SuggestRefund, Remark: "按建议金额"})
	e.must(err)
	if record == nil || record.Amount.String() != "56.13" {
		t.Fatalf("refund record = %+v", record)
	}
	e.checkInvariants()
}

func TestRevokeRejectsUngrantedAndNonPointsOrders(t *testing.T) {
	e := newPointsEnv(t)
	product, sku, _ := e.newPackProduct("pung", 10, 100, 0, nil)
	order := e.buy(e.user.ID, product, sku, 1)
	if _, err := e.points.RevokeForOrder(1, order.OrderNo); !errors.Is(err, boostcontract.ErrPointsNotGranted) {
		t.Fatalf("revoke unpaid order: %v", err)
	}
	if _, err := e.points.RevokeForOrder(1, "NOPE"); !errors.Is(err, boostcontract.ErrNotFound) {
		t.Fatalf("revoke missing order: %v", err)
	}
	// 普通订单退款不受影响
	plain := orderdomain.Order{OrderNo: "PLAIN1", UserID: e.user.ID, Status: constants.OrderStatusPaid, Currency: "CNY"}
	e.must(e.db.Create(&plain).Error)
	if err := e.points.EnsureRefundable(plain.ID); err != nil {
		t.Fatalf("plain order should be refundable: %v", err)
	}
}

// 600 步随机操作后不变量仍成立（对应原型的随机压力测试，范围限于积分账本）。
func TestRandomLedgerOperationsKeepInvariants(t *testing.T) {
	e := newPointsEnv(t)
	users := []uint{e.user.ID, e.newUser("b").ID, e.newUser("c").ID}
	seed := uint64(20261027)
	rnd := func(n int) int {
		seed ^= seed << 13
		seed ^= seed >> 7
		seed ^= seed << 17
		return int(seed % uint64(n))
	}
	type held struct {
		user uint
		amt  int64
	}
	open := map[uint]held{}
	var nextTask uint = 1
	for step := 0; step < 600; step++ {
		u := users[rnd(len(users))]
		switch rnd(6) {
		case 0, 1:
			var vd *int
			if rnd(2) == 0 {
				vd = days(1 + rnd(5))
			}
			e.grant(u, int64(1+rnd(200)), vd, "rand-"+time.Duration(step).String())
		case 2:
			amt := int64(rnd(150))
			if err := e.hold(u, amt, nextTask); err == nil {
				open[nextTask] = held{u, amt}
			} else if !errors.Is(err, boostcontract.ErrPointsInsufficient) {
				t.Fatalf("hold: %v", err)
			}
			nextTask++
		case 3:
			for id, h := range open {
				e.settle(h.user, id, h.amt, int64(rnd(int(h.amt)+20)))
				delete(open, id)
				break
			}
		case 4:
			_, err := e.points.AdminDeduct(boostapp.AdminChangeInput{AdminID: 1, UserID: u, Amount: int64(1 + rnd(50)), Reason: "rand", RequestID: "d-" + time.Duration(step).String()})
			if err != nil && !errors.Is(err, boostcontract.ErrPointsInsufficient) {
				t.Fatalf("deduct: %v", err)
			}
		case 5:
			e.clock = e.clock.Add(time.Duration(rnd(36)) * time.Hour)
			_, err := e.points.ExpireDue(1000)
			e.must(err)
		}
		if step%50 == 0 {
			e.checkInvariants()
		}
	}
	for id, h := range open {
		e.settle(h.user, id, h.amt, 0)
	}
	e.checkInvariants()
}
