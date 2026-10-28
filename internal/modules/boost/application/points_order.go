package application

import (
	"errors"
	"fmt"
	"strings"
	"time"
	"unicode/utf8"

	boostcontract "github.com/dujiao-next/internal/modules/boost/contract"
	boostdomain "github.com/dujiao-next/internal/modules/boost/domain"
	"github.com/dujiao-next/internal/shared/money"

	"github.com/shopspring/decimal"
)

// FulfillmentTypeBoostPoints 积分包商品的交付类型（Dujiao products.fulfillment_type）。
const FulfillmentTypeBoostPoints = "boost_points"

// Dujiao 订单状态里和积分包有关的几个（与 constants 一致，这里不依赖 constants 包以免扩大耦合）。
const (
	orderStatusPaid      = "paid"
	orderStatusCompleted = "completed"
)

// ---------------------------------------------------------------------------
// 积分包（后台）
// ---------------------------------------------------------------------------

// PackInput 新建或修改积分包。新建时 SKUID 必填且之后不能改。
type PackInput struct {
	ID        uint
	SKUID     uint
	Name      string
	Points    int64
	Bonus     int64
	ValidDays *int
	Active    *bool
}

// ListPacks 后台积分包列表（含商品 / SKU 状态和售价）。
func (s *PointsService) ListPacks() ([]boostcontract.PackView, error) {
	return s.repo.ListPacks(false)
}

// ListActivePacks 用户端可购买的积分包。
func (s *PointsService) ListActivePacks() ([]boostcontract.PackView, error) {
	return s.repo.ListPacks(true)
}

// SavePack 新建或修改积分包。SKU 所属商品的交付类型必须是 boost_points，每个 SKU 只能有一个积分包。
func (s *PointsService) SavePack(in PackInput) (*boostdomain.PointPack, error) {
	name := strings.TrimSpace(in.Name)
	if utf8.RuneCountInString(name) > 64 {
		return nil, invalid(boostcontract.ErrInvalid, "名称最多 64 个字")
	}
	if in.Points <= 0 || in.Points > maxPointsPerChange || in.Bonus < 0 || in.Bonus > maxPointsPerChange {
		return nil, invalid(boostcontract.ErrPointsInvalid, "积分必须是正整数，赠送积分不能为负")
	}
	if in.ValidDays != nil && (*in.ValidDays < 0 || *in.ValidDays > 3650) {
		return nil, invalid(boostcontract.ErrInvalid, "有效天数需为 0–3650（0 = 永久）")
	}
	var validDays *int
	if in.ValidDays != nil && *in.ValidDays > 0 {
		v := *in.ValidDays
		validDays = &v
	}
	if name == "" {
		name = fmt.Sprintf("%d 积分", in.Points+in.Bonus)
	}
	var saved *boostdomain.PointPack
	err := s.repo.WithinTransaction(func(repo boostcontract.PointsRepository) error {
		var pack *boostdomain.PointPack
		if in.ID != 0 {
			existing, err := repo.GetPack(in.ID)
			if err != nil {
				return err
			}
			if existing == nil {
				return boostcontract.ErrNotFound
			}
			if in.SKUID != 0 && in.SKUID != existing.SKUID {
				return invalid(boostcontract.ErrCodeImmutable, "积分包对应的 SKU 创建后不能修改（历史订单按 SKU 发放）")
			}
			pack = existing
		} else {
			ref, err := repo.GetSKURef(in.SKUID)
			if err != nil {
				return err
			}
			if ref == nil {
				return invalid(boostcontract.ErrNotFound, "SKU %d 不存在", in.SKUID)
			}
			if ref.FulfillmentType != FulfillmentTypeBoostPoints {
				return invalid(boostcontract.ErrPackProductInvalid, "该商品的交付类型不是「代练积分」，请先在商品管理里修改")
			}
			dup, err := repo.GetPackBySKU(in.SKUID)
			if err != nil {
				return err
			}
			if dup != nil {
				return invalid(boostcontract.ErrPackDuplicate, "该 SKU 已经有积分包（#%d）", dup.ID)
			}
			pack = &boostdomain.PointPack{ProductID: ref.ProductID, SKUID: ref.SKUID, Active: true}
		}
		pack.Name = name
		pack.Points = in.Points
		pack.Bonus = in.Bonus
		pack.ValidDays = validDays
		if in.Active != nil {
			pack.Active = *in.Active
		}
		if in.ID != 0 {
			if err := repo.SavePack(pack); err != nil {
				return err
			}
		} else if err := repo.CreatePack(pack); err != nil {
			return err
		}
		saved = pack
		return nil
	})
	return saved, err
}

// ---------------------------------------------------------------------------
// 支付成功 → 到账
// ---------------------------------------------------------------------------

// GrantResult 一个订单的到账结果。
type GrantResult struct {
	OrderID    uint  `json:"order_id"`
	Points     int64 `json:"points"`
	Duplicated bool  `json:"duplicated"`
}

// HandleOrderPaid 支付成功后调用（Dujiao enqueueOrderPaidAsync）。orderID 可以是父订单，逐个处理带积分包的子订单。
// 只到账一次：每个订单项的批次 (order, order_item:{id}) 唯一，订单写交付记录后不再处理。
func (s *PointsService) HandleOrderPaid(orderID uint) ([]GrantResult, error) {
	if s.orders == nil {
		return nil, boostcontract.ErrOrderNotPointsOrder
	}
	ids, err := s.orders.PointsOrderIDs(orderID)
	if err != nil {
		return nil, err
	}
	var out []GrantResult
	var firstErr error
	for _, id := range ids {
		res, err := s.grantForOrder(id)
		if err != nil {
			if firstErr == nil {
				firstErr = fmt.Errorf("order %d: %w", id, err)
			}
			continue
		}
		out = append(out, *res)
	}
	return out, firstErr
}

// GrantForPaidOrder 实现 Dujiao 支付服务的 BoostPointsGranter：不含积分包的订单什么都不做。
func (s *PointsService) GrantForPaidOrder(orderID uint) error {
	if s.orders == nil {
		return nil
	}
	_, err := s.HandleOrderPaid(orderID)
	return err
}

func (s *PointsService) grantForOrder(orderID uint) (*GrantResult, error) {
	result := &GrantResult{OrderID: orderID}
	err := s.orders.WithinOrder(orderID, func(order *boostcontract.PointsOrder, tx boostcontract.OrderPointsTx) error {
		if order.HasFulfillment {
			result.Duplicated = true
			return nil
		}
		if order.Status != orderStatusPaid {
			return invalid(boostcontract.ErrOrderNotPaid, "订单状态 %s 不能发放积分", order.Status)
		}
		if order.UserID == 0 {
			return invalid(boostcontract.ErrOrderGuest, "游客订单不能发放积分")
		}
		repo := tx.Points()
		var total int64
		for _, item := range order.Items {
			if !item.IsPoints {
				continue
			}
			pack, err := repo.GetPackBySKU(item.SKUID)
			if err != nil {
				return err
			}
			if pack == nil {
				return invalid(boostcontract.ErrPackMissing, "SKU %d 没有配置积分包", item.SKUID)
			}
			qty := int64(item.Quantity)
			if qty <= 0 {
				return invalid(boostcontract.ErrInvalid, "订单项数量不合法")
			}
			amount := (pack.Points + pack.Bonus) * qty
			oid := order.ID
			if _, err := s.grantInTx(repo, grantInput{
				UserID:     order.UserID,
				Amount:     amount,
				SourceType: boostdomain.LotSourceOrder,
				SourceRef:  fmt.Sprintf("order_item:%d", item.ID),
				ValidDays:  pack.ValidDays,
				Remark:     "购买积分包 " + order.OrderNo,
				OrderID:    &oid,
			}); err != nil {
				return err
			}
			total += amount
		}
		if total == 0 {
			return boostcontract.ErrOrderNotPointsOrder
		}
		result.Points = total
		return tx.CompleteWithFulfillment(order.ID, fmt.Sprintf("已到账 %d 积分", total), total, s.now())
	})
	if err != nil {
		return nil, err
	}
	return result, nil
}

// RetryUngranted 补发：已支付超过 grace、还没到账的积分包订单（支付回调里同步到账失败时兜底）。
func (s *PointsService) RetryUngranted(grace time.Duration, limit int) (int, error) {
	if s.orders == nil {
		return 0, nil
	}
	ids, err := s.orders.FindUngranted(s.now().Add(-grace), limit)
	if err != nil {
		return 0, err
	}
	n := 0
	var firstErr error
	for _, id := range ids {
		if res, err := s.grantForOrder(id); err != nil {
			if firstErr == nil {
				firstErr = fmt.Errorf("order %d: %w", id, err)
			}
		} else if !res.Duplicated {
			n++
		}
	}
	return n, firstErr
}

// ---------------------------------------------------------------------------
// 退款前撤回
// ---------------------------------------------------------------------------

// RevokeResult 积分包订单撤回结果（按订单号汇总）。
type RevokeResult struct {
	OrderNo       string       `json:"order_no"`
	Total         int64        `json:"total"`   // 到账总数
	Used          int64        `json:"used"`    // 已用（含已过期）
	Revoked       int64        `json:"revoked"` // 本次撤回
	SuggestRefund money.Amount `json:"suggest_refund"`
	Already       bool         `json:"already"`
}

// RevokeForOrder 撤回积分包订单的剩余积分，按未使用比例给出建议退款金额（向下取整到分）。
// 只撤剩余：已用掉的不追回；冻结中的不在批次剩余里，所以不受影响（任务结算退回时因批次已撤回而失效）。
// 重复调用返回原建议金额。
func (s *PointsService) RevokeForOrder(adminID uint, orderNo string) (*RevokeResult, error) {
	if s.orders == nil {
		return nil, boostcontract.ErrOrderNotPointsOrder
	}
	orderNo = strings.TrimSpace(orderNo)
	rootID, err := s.orders.ResolveOrderNo(orderNo)
	if err != nil {
		return nil, err
	}
	if rootID == 0 {
		return nil, boostcontract.ErrNotFound
	}
	ids, err := s.orders.PointsOrderIDs(rootID)
	if err != nil {
		return nil, err
	}
	if len(ids) == 0 {
		return nil, invalid(boostcontract.ErrOrderNotPointsOrder, "订单 %s 不含积分包", orderNo)
	}
	out := &RevokeResult{OrderNo: orderNo, SuggestRefund: money.FromDecimal(decimal.Zero), Already: true}
	suggest := decimal.Zero
	for _, id := range ids {
		err := s.orders.WithinOrder(id, func(order *boostcontract.PointsOrder, tx boostcontract.OrderPointsTx) error {
			repo := tx.Points()
			var lots []*boostdomain.PointLot
			for _, item := range order.Items {
				if !item.IsPoints {
					continue
				}
				lot, err := repo.GetLotBySource(boostdomain.LotSourceOrder, fmt.Sprintf("order_item:%d", item.ID))
				if err != nil {
					return err
				}
				if lot == nil {
					return invalid(boostcontract.ErrPointsNotGranted, "订单 %s 的积分还没到账，不能撤回", order.OrderNo)
				}
				lots = append(lots, lot)
			}
			var amount, remaining int64
			allRevoked := true
			for _, lot := range lots {
				amount += lot.Amount
				if lot.Status != boostdomain.LotStatusRevoked {
					allRevoked = false
					// 剩余里可能有已到期但还没扫描的，到期的部分算"已用"，不退钱
					if lot.Status == boostdomain.LotStatusActive && (lot.ExpiresAt == nil || lot.ExpiresAt.After(s.now())) {
						remaining += lot.Remaining
					}
				}
			}
			out.Total += amount
			if allRevoked {
				for _, lot := range lots {
					if lot.SuggestRefund != nil {
						suggest = suggest.Add(lot.SuggestRefund.Decimal)
					}
				}
				return nil
			}
			out.Already = false
			if order.UserID == 0 {
				return boostcontract.ErrOrderGuest
			}
			account, err := repo.LockAccount(order.UserID)
			if err != nil {
				return err
			}
			// 先把到期的批次按到期处理，剩余数字才准确
			if _, err := s.expireDueForUser(repo, account, s.now()); err != nil {
				return err
			}
			paid := order.TotalAmount.Decimal.Sub(order.RefundedAmount.Decimal)
			if paid.IsNegative() {
				paid = decimal.Zero
			}
			orderSuggest := decimal.Zero
			if amount > 0 {
				orderSuggest = paid.Mul(decimal.NewFromInt(remaining)).Div(decimal.NewFromInt(amount)).RoundFloor(2)
			}
			admin := adminID
			oid := order.ID
			// 建议金额按批次剩余比例拆到各批次上（只有一个批次时就是全部），保证重复调用汇总一致
			allocated := decimal.Zero
			for i, item := range lots {
				lot, err := repo.GetLot(item.ID) // 重新读，expireDueForUser 可能改过
				if err != nil {
					return err
				}
				if lot.Status == boostdomain.LotStatusRevoked {
					continue
				}
				left := int64(0)
				if lot.Status == boostdomain.LotStatusActive {
					left = lot.Remaining
				}
				share := decimal.Zero
				if i == len(lots)-1 {
					share = orderSuggest.Sub(allocated)
				} else if remaining > 0 {
					share = orderSuggest.Mul(decimal.NewFromInt(left)).Div(decimal.NewFromInt(remaining)).RoundFloor(2)
				}
				allocated = allocated.Add(share)
				if left > 0 {
					if _, err := s.applyLedger(repo, account, ledgerInput{
						Type:        boostdomain.LedgerRevoke,
						DAvailable:  -left,
						BizKey:      fmt.Sprintf("revoke:order_item:%s", strings.TrimPrefix(lot.SourceRef, "order_item:")),
						Allocations: []boostcontract.LotAllocation{{LotID: lot.ID, Amount: left}},
						OrderID:     &oid,
						AdminID:     &admin,
						Remark:      "积分包退款撤回 " + order.OrderNo,
					}); err != nil {
						return err
					}
				}
				amt := money.FromDecimal(share)
				lot.Remaining = 0
				lot.Status = boostdomain.LotStatusRevoked
				lot.SuggestRefund = &amt
				if err := repo.SaveLot(lot); err != nil {
					return err
				}
				out.Revoked += left
			}
			suggest = suggest.Add(orderSuggest)
			return nil
		})
		if err != nil {
			return nil, err
		}
	}
	out.Used = out.Total - out.Revoked
	if out.Already {
		out.Used = 0 // 已撤回时不再重算用量，前端只看建议金额
	}
	out.SuggestRefund = money.FromDecimal(suggest)
	return out, nil
}

// EnsureRefundable 退款前检查（Dujiao 退款服务调用）：订单里的积分包必须已经撤回剩余积分。
// 已支付但还没到账的也拒绝（否则退款后还会补发）。不含积分包的订单直接通过。
func (s *PointsService) EnsureRefundable(orderID uint) error {
	if s.orders == nil {
		return nil
	}
	ids, err := s.orders.PointsOrderIDs(orderID)
	if err != nil {
		return err
	}
	for _, id := range ids {
		order, err := s.orders.LoadOrder(id)
		if err != nil {
			return err
		}
		if order == nil {
			continue
		}
		for _, item := range order.Items {
			if !item.IsPoints {
				continue
			}
			lot, err := s.repo.GetLotBySource(boostdomain.LotSourceOrder, fmt.Sprintf("order_item:%d", item.ID))
			if err != nil {
				return err
			}
			if lot == nil {
				if order.Status == orderStatusPaid || order.Status == orderStatusCompleted {
					return boostcontract.ErrPointsRevokeRequired
				}
				continue
			}
			if lot.Status != boostdomain.LotStatusRevoked {
				return boostcontract.ErrPointsRevokeRequired
			}
		}
	}
	return nil
}

// IsPointsError 判断是否是积分业务错误（transport 用来决定是否直接展示提示）。
func IsPointsError(err error) bool {
	for _, kind := range []error{
		boostcontract.ErrPointsInsufficient, boostcontract.ErrPointsInvalid, boostcontract.ErrPackMissing,
		boostcontract.ErrPackDuplicate, boostcontract.ErrPackProductInvalid, boostcontract.ErrOrderNotPointsOrder,
		boostcontract.ErrOrderNotPaid, boostcontract.ErrOrderGuest, boostcontract.ErrPointsNotGranted,
		boostcontract.ErrPointsRevokeRequired,
	} {
		if errors.Is(err, kind) {
			return true
		}
	}
	return false
}
