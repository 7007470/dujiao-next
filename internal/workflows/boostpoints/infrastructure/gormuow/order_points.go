// Package gormuow 把 Dujiao 订单 / 交付记录和代练积分账本放进同一个 GORM 事务：
// 积分包订单支付成功后，"写积分批次 + 流水 + 交付记录 + 订单完成"要么全成功，要么全回滚。
package gormuow

import (
	"strings"
	"time"

	boostapp "github.com/dujiao-next/internal/modules/boost/application"
	boostcontract "github.com/dujiao-next/internal/modules/boost/contract"
	boostgormstore "github.com/dujiao-next/internal/modules/boost/infrastructure/gormstore"
	fulfillmentdomain "github.com/dujiao-next/internal/modules/fulfillment/domain"
	orderapp "github.com/dujiao-next/internal/modules/order/application"
	ordercontract "github.com/dujiao-next/internal/modules/order/contract"
	orderdomain "github.com/dujiao-next/internal/modules/order/domain"
	ordergormstore "github.com/dujiao-next/internal/modules/order/infrastructure/gormstore"

	"github.com/dujiao-next/internal/constants"

	"gorm.io/gorm"
)

// OrderPoints 实现 boostcontract.OrderPoints。
type OrderPoints struct {
	db     *gorm.DB
	secret string // 订单仓储的游客凭据密钥（UseTransaction 要求）
}

// New 创建适配器。guestCredentialSecret 与 Dujiao 订单仓储一致（config app.secret_key）。
func New(db *gorm.DB, guestCredentialSecret string) *OrderPoints {
	return &OrderPoints{db: db, secret: guestCredentialSecret}
}

var _ boostcontract.OrderPoints = (*OrderPoints)(nil)

func isPointsItem(item orderdomain.OrderItem) bool {
	return strings.TrimSpace(item.FulfillmentType) == boostapp.FulfillmentTypeBoostPoints
}

func hasPointsItems(order *orderdomain.Order) bool {
	for _, item := range order.Items {
		if isPointsItem(item) {
			return true
		}
	}
	return false
}

func (a *OrderPoints) orders(db *gorm.DB) ordercontract.Store {
	return ordergormstore.UseTransaction(db, a.secret).Orders()
}

// PointsOrderIDs 返回 orderID 自己（单订单 / 子订单）或其子订单中含积分包的订单 ID。
func (a *OrderPoints) PointsOrderIDs(orderID uint) ([]uint, error) {
	order, err := a.orders(a.db).GetByID(orderID)
	if err != nil || order == nil {
		return nil, err
	}
	var ids []uint
	if order.ParentID == nil && len(order.Children) > 0 {
		for i := range order.Children {
			if hasPointsItems(&order.Children[i]) {
				ids = append(ids, order.Children[i].ID)
			}
		}
		return ids, nil
	}
	if hasPointsItems(order) {
		ids = append(ids, order.ID)
	}
	return ids, nil
}

// ResolveOrderNo 订单号 → ID。
func (a *OrderPoints) ResolveOrderNo(orderNo string) (uint, error) {
	var ids []uint
	err := a.db.Model(&orderdomain.Order{}).Where("order_no = ? AND deleted_at IS NULL", orderNo).Limit(1).Pluck("id", &ids).Error
	if err != nil || len(ids) == 0 {
		return 0, err
	}
	return ids[0], nil
}

func toPointsOrder(order *orderdomain.Order, hasFulfillment bool) *boostcontract.PointsOrder {
	out := &boostcontract.PointsOrder{
		ID:             order.ID,
		OrderNo:        order.OrderNo,
		ParentID:       order.ParentID,
		UserID:         order.UserID,
		Status:         order.Status,
		TotalAmount:    order.TotalAmount,
		RefundedAmount: order.RefundedAmount,
		HasFulfillment: hasFulfillment,
	}
	for _, item := range order.Items {
		out.Items = append(out.Items, boostcontract.PointsOrderItem{
			ID:        item.ID,
			SKUID:     item.SKUID,
			ProductID: item.ProductID,
			Quantity:  item.Quantity,
			IsPoints:  isPointsItem(item),
		})
	}
	return out
}

// LoadOrder 只读加载订单。
func (a *OrderPoints) LoadOrder(orderID uint) (*boostcontract.PointsOrder, error) {
	order, err := a.orders(a.db).GetByID(orderID)
	if err != nil || order == nil {
		return nil, err
	}
	return toPointsOrder(order, order.Fulfillment != nil), nil
}

// WithinOrder 开事务、锁定订单行和交付记录，再读一次订单（含订单项），然后执行 fn。
func (a *OrderPoints) WithinOrder(orderID uint, fn func(order *boostcontract.PointsOrder, tx boostcontract.OrderPointsTx) error) error {
	return a.db.Transaction(func(db *gorm.DB) error {
		orderTx := ordergormstore.UseTransaction(db, a.secret)
		locked, err := orderTx.Orders().GetByIDForUpdate(orderID)
		if err != nil {
			return err
		}
		if locked == nil {
			return boostcontract.ErrNotFound
		}
		_, found, err := orderTx.Fulfillments().FindByOrderIDForUpdate(orderID)
		if err != nil {
			return err
		}
		full, err := orderTx.Orders().GetByID(orderID)
		if err != nil {
			return err
		}
		if full == nil {
			return boostcontract.ErrNotFound
		}
		full.Status = locked.Status // 以加锁读到的状态为准
		return fn(toPointsOrder(full, found), &orderPointsTx{db: db, orders: orderTx})
	})
}

// FindUngranted 已支付超过 paidBefore、还没有交付记录、含积分包的单个订单（子订单或无子订单的订单）。
func (a *OrderPoints) FindUngranted(paidBefore time.Time, limit int) ([]uint, error) {
	if limit <= 0 {
		limit = 100
	}
	var ids []uint
	err := a.db.Table("orders AS o").
		Joins("JOIN order_items AS oi ON oi.order_id = o.id AND oi.deleted_at IS NULL").
		Joins("LEFT JOIN fulfillments AS f ON f.order_id = o.id AND f.deleted_at IS NULL").
		Where("o.deleted_at IS NULL AND o.status = ? AND o.paid_at IS NOT NULL AND o.paid_at <= ?", constants.OrderStatusPaid, paidBefore).
		Where("oi.fulfillment_type = ? AND f.id IS NULL", boostapp.FulfillmentTypeBoostPoints).
		Distinct("o.id").Order("o.id ASC").Limit(limit).
		Pluck("o.id", &ids).Error
	return ids, err
}

type orderPointsTx struct {
	db     *gorm.DB
	orders ordercontract.Transaction
}

func (tx *orderPointsTx) Points() boostcontract.PointsRepository {
	return boostgormstore.UsePointsTransaction(tx.db)
}

// CompleteWithFulfillment 写交付记录、订单置完成，并同步父订单状态（与 Dujiao 自动交付 CreateAuto 的写法一致）。
func (tx *orderPointsTx) CompleteWithFulfillment(orderID uint, payload string, _ int64, now time.Time) error {
	fulfillment := &fulfillmentdomain.Fulfillment{
		OrderID:     orderID,
		Type:        boostapp.FulfillmentTypeBoostPoints,
		Status:      constants.FulfillmentStatusDelivered,
		Payload:     payload,
		DeliveredAt: &now,
		CreatedAt:   now,
		UpdatedAt:   now,
	}
	if err := tx.orders.Fulfillments().Create(fulfillment); err != nil {
		return err
	}
	orders := tx.orders.Orders()
	if err := orders.UpdateFields(orderID, map[string]interface{}{
		"status":     constants.OrderStatusCompleted,
		"updated_at": now,
	}); err != nil {
		return err
	}
	order, err := orders.GetByIDForUpdate(orderID)
	if err != nil {
		return err
	}
	if order != nil && order.ParentID != nil {
		if _, err := orderapp.SyncParentStatus(orders, *order.ParentID, now); err != nil {
			return err
		}
	}
	return nil
}
