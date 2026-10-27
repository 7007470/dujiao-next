// Package memberrate 把 Dujiao 的会员等级折扣率提供给代练报价。
package memberrate

import (
	"math"

	userdomain "github.com/dujiao-next/internal/modules/identity/user/domain"
	memberleveldomain "github.com/dujiao-next/internal/modules/memberlevel/domain"
)

// UserReader 读取用户（取会员等级）。
type UserReader interface {
	GetByID(id uint) (*userdomain.User, error)
}

// LevelReader 读取会员等级。
type LevelReader interface {
	GetByID(id uint) (*memberleveldomain.MemberLevel, error)
}

// Resolver 实现 boostapp.DiscountRateResolver。规则与商品会员价一致（memberlevel ResolveMemberPrice 的折扣率分支）：
// 没有等级、等级停用、折扣率 ≤ 0 或 ≥ 100 都按不打折处理。折扣率向下取整到整数百分比。
type Resolver struct {
	users  UserReader
	levels LevelReader
}

// New 创建折扣率解析器。
func New(users UserReader, levels LevelReader) *Resolver {
	return &Resolver{users: users, levels: levels}
}

// DiscountRate 返回用户的会员折扣率（1–100）。
func (r *Resolver) DiscountRate(userID uint) int {
	if r == nil || r.users == nil || r.levels == nil || userID == 0 {
		return 100
	}
	user, err := r.users.GetByID(userID)
	if err != nil || user == nil || user.MemberLevelID == 0 {
		return 100
	}
	level, err := r.levels.GetByID(user.MemberLevelID)
	if err != nil || level == nil || !level.IsActive {
		return 100
	}
	rate := level.DiscountRate.Decimal.InexactFloat64()
	if rate <= 0 || rate >= 100 {
		return 100
	}
	return int(math.Max(1, math.Floor(rate)))
}
