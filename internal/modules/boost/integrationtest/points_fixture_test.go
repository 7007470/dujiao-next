package integrationtest

import (
	"context"
	"fmt"
	"testing"
	"time"

	boostapp "github.com/dujiao-next/internal/modules/boost/application"
	boostcontract "github.com/dujiao-next/internal/modules/boost/contract"
	boostdomain "github.com/dujiao-next/internal/modules/boost/domain"
	"github.com/dujiao-next/internal/modules/boost/infrastructure/gormstore"
	boostpointsgormuow "github.com/dujiao-next/internal/workflows/boostpoints/infrastructure/gormuow"

	"github.com/dujiao-next/internal/bootstrap/database/migrations"
	"github.com/dujiao-next/internal/constants"
	productdomain "github.com/dujiao-next/internal/modules/catalog/product/domain"
	productgormstore "github.com/dujiao-next/internal/modules/catalog/product/store/gormstore"
	userdomain "github.com/dujiao-next/internal/modules/identity/user/domain"
	userstore "github.com/dujiao-next/internal/modules/identity/user/infrastructure/gormstore"
	orderapp "github.com/dujiao-next/internal/modules/order/application"
	orderrefund "github.com/dujiao-next/internal/modules/order/application/refund"
	orderdomain "github.com/dujiao-next/internal/modules/order/domain"
	ordergormstore "github.com/dujiao-next/internal/modules/order/infrastructure/gormstore"
	paymentapp "github.com/dujiao-next/internal/modules/payment/application"
	paymentcontract "github.com/dujiao-next/internal/modules/payment/contract"
	paymentdomain "github.com/dujiao-next/internal/modules/payment/domain"
	"github.com/dujiao-next/internal/modules/payment/infrastructure/gateway/provider"
	paymentgormstore "github.com/dujiao-next/internal/modules/payment/infrastructure/gormstore"
	promotiongormstore "github.com/dujiao-next/internal/modules/promotion/infrastructure/gormstore"
	settingsapp "github.com/dujiao-next/internal/modules/settings/application"
	settingsstore "github.com/dujiao-next/internal/modules/settings/infrastructure/gormstore"
	walletapp "github.com/dujiao-next/internal/modules/wallet/application"
	walletdomain "github.com/dujiao-next/internal/modules/wallet/domain"
	walletgormstore "github.com/dujiao-next/internal/modules/wallet/infrastructure/gormstore"
	"github.com/dujiao-next/internal/platform/database/gormdb"
	"github.com/dujiao-next/internal/shared/jsonmap"
	"github.com/dujiao-next/internal/shared/money"

	"github.com/glebarez/sqlite"
	"github.com/shopspring/decimal"
	"gorm.io/gorm"
	gormlogger "gorm.io/gorm/logger"
)

const testSecret = "test-guest-credential-secret-with-32-bytes"

// pointsEnv 用真实的 Dujiao 订单 / 支付 / 退款服务 + 代练积分服务，跑在 SQLite 上（完整迁移）。
type pointsEnv struct {
	t        *testing.T
	db       *gorm.DB
	points   *boostapp.PointsService
	orders   *orderapp.OrderService
	payments *paymentapp.PaymentService
	refunds  *orderrefund.Service
	channel  *paymentdomain.PaymentChannel
	clock    time.Time
	user     userdomain.User
}

type nopOrderQueue struct{}

func (nopOrderQueue) Enabled() bool                                  { return true }
func (nopOrderQueue) EnqueueTimeoutCancel(uint, time.Duration) error { return nil }
func (nopOrderQueue) EnqueueStatusEmail(uint, string) error          { return nil }

type fakeGateway struct{}

func (fakeGateway) Type() string {
	return constants.PaymentProviderOfficial + ":" + constants.PaymentChannelTypeWechat
}
func (fakeGateway) ValidateConfig(jsonmap.JSON, string) error { return nil }
func (fakeGateway) CreatePayment(context.Context, jsonmap.JSON, paymentcontract.GatewayCreateInput) (*paymentcontract.GatewayCreateResult, error) {
	return &paymentcontract.GatewayCreateResult{QRCodeURL: "weixin://wxpay/bizpayurl?pr=test"}, nil
}

func newPointsEnv(t *testing.T) *pointsEnv {
	t.Helper()
	dsn := fmt.Sprintf("file:boost_points_%d?mode=memory&cache=shared", time.Now().UnixNano())
	db, err := gorm.Open(sqlite.Open(dsn), &gorm.Config{Logger: gormlogger.Default.LogMode(gormlogger.Silent)})
	if err != nil {
		t.Fatalf("open sqlite: %v", err)
	}
	sqlDB, _ := db.DB()
	sqlDB.SetMaxOpenConns(1) // 与生产 SQLite 配置一致（单连接），也让事务真正串行
	gormdb.DB = db
	if err := migrations.AutoMigrate(); err != nil {
		t.Fatalf("migrate: %v", err)
	}

	orderStore := ordergormstore.New(db, testSecret)
	settings := settingsapp.NewService(settingsstore.New(db))
	walletRepo := walletgormstore.New(db)
	wallet := walletapp.NewService(walletapp.Options{Repository: walletRepo, Transactions: walletRepo})
	users := userstore.New(db)

	env := &pointsEnv{t: t, db: db, clock: time.Now()}
	env.points = boostapp.NewPointsService(gormstore.NewPointsStore(db), boostpointsgormuow.New(db, testSecret))
	env.points.SetClock(func() time.Time { return env.clock })
	env.orders = orderapp.NewOrderService(orderapp.OrderServiceOptions{
		OrderStore:      orderStore,
		UserStore:       users,
		ProductStore:    productgormstore.NewProductStore(db),
		ProductSKUStore: productgormstore.NewSKUStore(db),
		SettingService:  settings,
		WalletService:   wallet,
		PromotionRepo:   promotiongormstore.New(db),
		Queue:           nopOrderQueue{},
		ExpireMinutes:   15,
	})
	env.orders.SetRefundGuard(env.points)
	reg := provider.NewRegistry()
	reg.Register(constants.PaymentProviderOfficial, constants.PaymentChannelTypeWechat, fakeGateway{})
	env.payments = paymentapp.NewPaymentService(paymentapp.PaymentServiceOptions{
		OrderStore:              orderStore,
		ProductRepo:             productgormstore.NewProductStore(db),
		ProductSKURepo:          productgormstore.NewSKUStore(db),
		PaymentStore:            paymentgormstore.New(db, testSecret),
		ChannelStore:            paymentgormstore.NewChannelStore(db),
		WalletRepo:              walletRepo,
		UserStore:               users,
		WalletService:           wallet,
		SettingService:          settings,
		ExpireMinutes:           15,
		PaymentProviderRegistry: reg,
		BoostPoints:             env.points,
	})
	env.refunds = orderrefund.New(orderStore, users, nil, settings, wallet, paymentgormstore.New(db, testSecret))
	env.refunds.SetRefundGuard(env.points)

	now := time.Now()
	env.channel = &paymentdomain.PaymentChannel{
		Name: "微信", ProviderType: constants.PaymentProviderOfficial, ChannelType: constants.PaymentChannelTypeWechat,
		InteractionMode: constants.PaymentInteractionQR, FeeRate: money.FromDecimal(decimal.Zero), FixedFee: money.FromDecimal(decimal.Zero),
		ConfigJSON: jsonmap.JSON{}, IsActive: true, CreatedAt: now, UpdatedAt: now,
	}
	env.must(db.Create(env.channel).Error)
	env.user = env.newUser("buyer")
	return env
}

func (e *pointsEnv) must(err error) {
	e.t.Helper()
	if err != nil {
		e.t.Fatalf("unexpected error: %v", err)
	}
}

func (e *pointsEnv) newUser(name string) userdomain.User {
	e.t.Helper()
	now := time.Now()
	u := userdomain.User{Email: fmt.Sprintf("%s-%d@example.test", name, now.UnixNano()), PasswordHash: "x", Status: constants.UserStatusActive, CreatedAt: now, UpdatedAt: now}
	e.must(e.db.Create(&u).Error)
	return u
}

func (e *pointsEnv) setBalance(userID uint, yuan int64) {
	e.t.Helper()
	now := time.Now()
	e.must(e.db.Where("user_id = ?", userID).Delete(&walletdomain.Account{}).Error)
	e.must(e.db.Create(&walletdomain.Account{UserID: userID, Balance: money.FromDecimal(decimal.NewFromInt(yuan)), CreatedAt: now, UpdatedAt: now}).Error)
}

// newPackProduct 建一个 boost_points 商品 + SKU + 积分包。
func (e *pointsEnv) newPackProduct(slug string, price int64, points, bonus int64, validDays *int) (productdomain.Product, productdomain.ProductSKU, *boostdomain.PointPack) {
	e.t.Helper()
	now := time.Now()
	product := productdomain.Product{
		CategoryID: 1, Slug: slug, TitleJSON: jsonmap.JSON{"zh-CN": "积分包 " + slug},
		PriceAmount: money.FromDecimal(decimal.NewFromInt(price)), PurchaseType: constants.ProductPurchaseMember,
		FulfillmentType: constants.FulfillmentTypeBoostPoints, IsActive: true, CreatedAt: now, UpdatedAt: now,
	}
	e.must(e.db.Create(&product).Error)
	sku := productdomain.ProductSKU{
		ProductID: product.ID, SKUCode: productdomain.DefaultSKUCode, PriceAmount: money.FromDecimal(decimal.NewFromInt(price)),
		IsActive: true, CreatedAt: now, UpdatedAt: now,
	}
	e.must(e.db.Create(&sku).Error)
	pack, err := e.points.SavePack(boostapp.PackInput{SKUID: sku.ID, Name: slug, Points: points, Bonus: bonus, ValidDays: validDays})
	e.must(err)
	return product, sku, pack
}

// buy 走 Dujiao 原生下单。返回父订单（Dujiao 总是建父订单 + 每个商品一个子订单）。
func (e *pointsEnv) buy(userID uint, product productdomain.Product, sku productdomain.ProductSKU, qty int) *orderdomain.Order {
	e.t.Helper()
	order, err := e.orders.CreateOrder(orderapp.CreateOrderInput{
		UserID: userID, Items: []orderapp.CreateOrderItem{{ProductID: product.ID, SKUID: sku.ID, Quantity: qty}}, SkipRiskControl: true,
	})
	e.must(err)
	return order
}

// payOnline 创建在线支付并模拟一次成功回调，返回支付记录（可用来重复回调）。
func (e *pointsEnv) payOnline(order *orderdomain.Order) (*paymentdomain.Payment, paymentapp.PaymentCallbackInput) {
	e.t.Helper()
	created, err := e.payments.CreatePayment(paymentapp.CreatePaymentInput{OrderID: order.ID, ChannelID: e.channel.ID, Context: context.Background()})
	e.must(err)
	cb := paymentapp.PaymentCallbackInput{
		PaymentID: created.Payment.ID, OrderNo: order.OrderNo, ChannelID: e.channel.ID,
		Status: constants.PaymentStatusSuccess, Amount: created.Payment.Amount, Currency: created.Payment.Currency, ProviderRef: "ref-" + order.OrderNo,
	}
	_, err = e.payments.HandleCallback(cb)
	e.must(err)
	return created.Payment, cb
}

func (e *pointsEnv) payWallet(order *orderdomain.Order) {
	e.t.Helper()
	result, err := e.payments.CreatePayment(paymentapp.CreatePaymentInput{OrderID: order.ID, UseBalance: true, Context: context.Background()})
	e.must(err)
	if !result.OrderPaid {
		e.t.Fatalf("wallet payment should pay the order in full")
	}
}

func (e *pointsEnv) reloadOrder(id uint) orderdomain.Order {
	e.t.Helper()
	var o orderdomain.Order
	e.must(e.db.Preload("Children").First(&o, id).Error)
	return o
}

func (e *pointsEnv) summary(userID uint) *boostapp.PointsSummary {
	e.t.Helper()
	s, err := e.points.Summary(userID)
	e.must(err)
	return s
}

func (e *pointsEnv) ledgerCount(userID uint, typ string) int64 {
	e.t.Helper()
	var n int64
	e.must(e.db.Model(&boostdomain.PointLedgerEntry{}).Where("user_id = ? AND type = ?", userID, typ).Count(&n).Error)
	return n
}

// checkInvariants 校验 AGENTS.md §3.2：可用 = 有效批次剩余之和 = 流水合计；冻结 = 流水冻结合计；都不为负。
func (e *pointsEnv) checkInvariants() {
	e.t.Helper()
	var accounts []boostdomain.PointAccount
	e.must(e.db.Find(&accounts).Error)
	for _, a := range accounts {
		var lotSum, ledgerA, ledgerF int64
		e.must(e.db.Model(&boostdomain.PointLot{}).Where("user_id = ? AND status = ?", a.UserID, boostdomain.LotStatusActive).
			Select("COALESCE(SUM(remaining),0)").Scan(&lotSum).Error)
		e.must(e.db.Model(&boostdomain.PointLedgerEntry{}).Where("user_id = ?", a.UserID).Select("COALESCE(SUM(delta_available),0)").Scan(&ledgerA).Error)
		e.must(e.db.Model(&boostdomain.PointLedgerEntry{}).Where("user_id = ?", a.UserID).Select("COALESCE(SUM(delta_frozen),0)").Scan(&ledgerF).Error)
		if a.Available < 0 || a.Frozen < 0 || a.Available != lotSum || a.Available != ledgerA || a.Frozen != ledgerF {
			e.t.Fatalf("invariant broken for user %d: available=%d frozen=%d lots=%d ledgerA=%d ledgerF=%d", a.UserID, a.Available, a.Frozen, lotSum, ledgerA, ledgerF)
		}
		var neg int64
		e.must(e.db.Model(&boostdomain.PointLot{}).Where("user_id = ? AND (remaining < 0 OR remaining > amount)", a.UserID).Count(&neg).Error)
		if neg > 0 {
			e.t.Fatalf("lot remaining out of range for user %d", a.UserID)
		}
	}
}

func (e *pointsEnv) grant(userID uint, amount int64, validDays *int, req string) *boostdomain.PointLedgerEntry {
	e.t.Helper()
	entry, err := e.points.AdminGrant(boostapp.AdminChangeInput{AdminID: 1, UserID: userID, Amount: amount, ValidDays: validDays, Reason: "测试发放", RequestID: req})
	e.must(err)
	return entry
}

// hold / settle 走 S5 将用到的事务入口。
func (e *pointsEnv) hold(userID uint, amount int64, taskID uint) error {
	return gormstore.NewPointsStore(e.db).WithinTransaction(func(repo boostcontract.PointsRepository) error {
		_, err := e.points.HoldPoints(repo, userID, amount, taskID)
		return err
	})
}

func (e *pointsEnv) settle(userID, taskID uint, held, consumed int64) *boostapp.SettleResult {
	e.t.Helper()
	var out *boostapp.SettleResult
	e.must(gormstore.NewPointsStore(e.db).WithinTransaction(func(repo boostcontract.PointsRepository) error {
		r, err := e.points.SettleTaskPoints(repo, userID, taskID, held, consumed, "测试结算")
		out = r
		return err
	}))
	return out
}

func days(n int) *int { return &n }
