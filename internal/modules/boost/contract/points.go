package contract

import (
	"errors"
	"time"

	boostdomain "github.com/dujiao-next/internal/modules/boost/domain"
	"github.com/dujiao-next/internal/shared/money"
)

// 积分相关的业务错误。
var (
	ErrPointsInsufficient   = errors.New("boost: points insufficient")
	ErrPointsInvalid        = errors.New("boost: invalid points amount")
	ErrPackMissing          = errors.New("boost: no point pack for sku")
	ErrPackDuplicate        = errors.New("boost: sku already has a point pack")
	ErrPackProductInvalid   = errors.New("boost: product is not a boost_points product")
	ErrOrderNotPointsOrder  = errors.New("boost: order has no point pack items")
	ErrOrderNotPaid         = errors.New("boost: order is not paid")
	ErrOrderGuest           = errors.New("boost: guest orders cannot receive points")
	ErrPointsNotGranted     = errors.New("boost: points not granted yet")
	ErrPointsRevokeRequired = errors.New("boost: remaining points must be revoked before refund")
)

// LotAllocation 一笔流水涉及的批次和数量（流水 lot_allocations_json 的元素）。
type LotAllocation struct {
	LotID  uint  `json:"lot_id"`
	Amount int64 `json:"amount"`
}

// LedgerFilter 流水分页筛选。
type LedgerFilter struct {
	UserID   uint
	Type     string
	Page     int
	PageSize int
}

// AccountFilter 后台积分账户列表筛选。
type AccountFilter struct {
	UserID   uint
	Keyword  string // 邮箱 / 昵称模糊匹配
	Page     int
	PageSize int
}

// AccountRow 后台积分账户列表行。
type AccountRow struct {
	boostdomain.PointAccount
	Email    string `json:"email"`
	Nickname string `json:"nickname"`
}

// PackView 积分包和它对应的 Dujiao 商品 / SKU（售价以 SKU 为准）。
type PackView struct {
	boostdomain.PointPack
	ProductSlug     string       `json:"product_slug"`
	ProductTitle    string       `json:"product_title"` // 默认语言标题
	ProductActive   bool         `json:"product_active"`
	FulfillmentType string       `json:"fulfillment_type"`
	SKUCode         string       `json:"sku_code"`
	SKUActive       bool         `json:"sku_active"`
	Price           money.Amount `json:"price"`
}

// SKURef 新建积分包时校验的商品 / SKU 事实。
type SKURef struct {
	ProductID       uint
	SKUID           uint
	FulfillmentType string
}

// PointsRepository 积分账本的数据端口。写操作必须在 WithinTransaction 里、先 LockAccount 再改，
// 这样同一用户的所有积分变动在数据库层串行（PostgreSQL 行锁；SQLite 本身单写）。
type PointsRepository interface {
	WithinTransaction(fn func(repo PointsRepository) error) error

	// LockAccount 锁定用户积分账户，不存在时先创建空账户。
	LockAccount(userID uint) (*boostdomain.PointAccount, error)
	GetAccount(userID uint) (*boostdomain.PointAccount, error)
	SaveAccount(account *boostdomain.PointAccount) error
	ListAccounts(filter AccountFilter) ([]AccountRow, int64, error)

	LedgerByKey(bizKey string) (*boostdomain.PointLedgerEntry, error)
	CreateLedger(entry *boostdomain.PointLedgerEntry) error
	ListLedger(filter LedgerFilter) ([]boostdomain.PointLedgerEntry, int64, error)

	CreateLot(lot *boostdomain.PointLot) error
	SaveLot(lot *boostdomain.PointLot) error
	GetLot(id uint) (*boostdomain.PointLot, error)
	GetLotBySource(sourceType, sourceRef string) (*boostdomain.PointLot, error)
	// ActiveLotsFIFO 返回有剩余的有效批次：先到期的在前，永久批次最后，同到期按 id。
	ActiveLotsFIFO(userID uint) ([]boostdomain.PointLot, error)
	// DueLots 返回已到期但还没处理的有效批次（userID 为 0 表示所有用户）。
	DueLots(userID uint, now time.Time, limit int) ([]boostdomain.PointLot, error)
	ListLots(userID uint, includeClosed bool) ([]boostdomain.PointLot, error)

	ListPacks(activeOnly bool) ([]PackView, error)
	GetPack(id uint) (*boostdomain.PointPack, error)
	GetPackBySKU(skuID uint) (*boostdomain.PointPack, error)
	CreatePack(pack *boostdomain.PointPack) error
	SavePack(pack *boostdomain.PointPack) error
	GetSKURef(skuID uint) (*SKURef, error)
}

// PointsOrderItem 积分包订单项。
type PointsOrderItem struct {
	ID        uint
	SKUID     uint
	Quantity  int
	IsPoints  bool // fulfillment_type = boost_points
	ProductID uint
}

// PointsOrder 发放 / 撤回积分需要的订单事实（单个订单，即 Dujiao 的子订单）。
type PointsOrder struct {
	ID             uint
	OrderNo        string
	ParentID       *uint
	UserID         uint
	Status         string
	TotalAmount    money.Amount
	RefundedAmount money.Amount
	HasFulfillment bool
	Items          []PointsOrderItem
}

// OrderPointsTx 一个已锁定订单的事务：订单、交付记录和积分账本在同一个数据库事务里。
type OrderPointsTx interface {
	Points() PointsRepository
	// CompleteWithFulfillment 写交付记录（payload 只写"已到账 N 积分"）、订单置完成，并同步父订单状态。
	CompleteWithFulfillment(orderID uint, payload string, points int64, now time.Time) error
}

// OrderPoints 是积分包订单的端口，实现在 workflows/boostpoints（订单 + 积分同事务）。
type OrderPoints interface {
	// PointsOrderIDs 返回 orderID 自己或其子订单中带积分包商品的订单 ID。
	PointsOrderIDs(orderID uint) ([]uint, error)
	// ResolveOrderNo 把订单号解析成订单 ID；不存在返回 0。
	ResolveOrderNo(orderNo string) (uint, error)
	// LoadOrder 只读加载订单（不加锁）。
	LoadOrder(orderID uint) (*PointsOrder, error)
	// WithinOrder 开事务、锁定订单后执行 fn。
	WithinOrder(orderID uint, fn func(order *PointsOrder, tx OrderPointsTx) error) error
	// FindUngranted 已支付超过 paidBefore、还没有交付记录的积分包订单（用于补发）。
	FindUngranted(paidBefore time.Time, limit int) ([]uint, error)
}
