package gormstore

import (
	"encoding/json"
	"strings"
	"time"

	boostcontract "github.com/dujiao-next/internal/modules/boost/contract"
	boostdomain "github.com/dujiao-next/internal/modules/boost/domain"
	"github.com/dujiao-next/internal/shared/money"

	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

// PointsStore 是 boostcontract.PointsRepository 的 GORM 实现。
type PointsStore struct {
	db *gorm.DB
}

// NewPointsStore 创建积分仓储。
func NewPointsStore(db *gorm.DB) *PointsStore {
	return &PointsStore{db: db}
}

// UsePointsTransaction 把调用方已打开的事务适配成积分仓储（workflows/boostpoints 用来和订单同事务）。
func UsePointsTransaction(tx *gorm.DB) *PointsStore {
	return &PointsStore{db: tx}
}

var _ boostcontract.PointsRepository = (*PointsStore)(nil)

// WithinTransaction 在事务里执行 fn。
func (s *PointsStore) WithinTransaction(fn func(repo boostcontract.PointsRepository) error) error {
	return s.db.Transaction(func(tx *gorm.DB) error {
		return fn(&PointsStore{db: tx})
	})
}

// ---- 账户 ----

// LockAccount 锁定账户行（PostgreSQL FOR UPDATE；SQLite 驱动忽略锁子句，靠单写事务串行）。
// 不存在时先插入空账户（并发插入靠主键冲突 + 忽略）。
func (s *PointsStore) LockAccount(userID uint) (*boostdomain.PointAccount, error) {
	now := time.Now()
	seed := boostdomain.PointAccount{UserID: userID, CreatedAt: now, UpdatedAt: now}
	if err := s.db.Clauses(clause.OnConflict{DoNothing: true}).Create(&seed).Error; err != nil {
		return nil, err
	}
	var account boostdomain.PointAccount
	if err := s.db.Clauses(clause.Locking{Strength: "UPDATE"}).Where("user_id = ?", userID).Take(&account).Error; err != nil {
		return nil, err
	}
	return &account, nil
}

func (s *PointsStore) GetAccount(userID uint) (*boostdomain.PointAccount, error) {
	return first[boostdomain.PointAccount](s.db.Where("user_id = ?", userID))
}

// SaveAccount 写回余额，版本号 +1（乐观锁再保险：版本不对说明锁失效，返回错误让事务回滚）。
func (s *PointsStore) SaveAccount(account *boostdomain.PointAccount) error {
	result := s.db.Model(&boostdomain.PointAccount{}).
		Where("user_id = ? AND version = ?", account.UserID, account.Version).
		Updates(map[string]interface{}{
			"available":  account.Available,
			"frozen":     account.Frozen,
			"version":    account.Version + 1,
			"updated_at": time.Now(),
		})
	if result.Error != nil {
		return result.Error
	}
	if result.RowsAffected != 1 {
		return errConcurrentAccountUpdate
	}
	account.Version++
	return nil
}

type accountUpdateError struct{}

func (accountUpdateError) Error() string { return "boost points: concurrent account update" }

var errConcurrentAccountUpdate error = accountUpdateError{}

func (s *PointsStore) ListAccounts(filter boostcontract.AccountFilter) ([]boostcontract.AccountRow, int64, error) {
	query := s.db.Table("boost_point_accounts AS a").
		Joins("LEFT JOIN users AS u ON u.id = a.user_id")
	if filter.UserID != 0 {
		query = query.Where("a.user_id = ?", filter.UserID)
	}
	if kw := strings.TrimSpace(filter.Keyword); kw != "" {
		like := "%" + strings.ToLower(kw) + "%"
		query = query.Where("LOWER(u.email) LIKE ? OR LOWER(u.display_name) LIKE ?", like, like)
	}
	var total int64
	if err := query.Count(&total).Error; err != nil {
		return nil, 0, err
	}
	page, size := normalizePage(filter.Page, filter.PageSize)
	type row struct {
		boostdomain.PointAccount
		Email       string
		DisplayName string
	}
	var rows []row
	if err := query.Select("a.*, u.email AS email, u.display_name AS display_name").
		Order("a.updated_at DESC, a.user_id DESC").
		Offset((page - 1) * size).Limit(size).
		Scan(&rows).Error; err != nil {
		return nil, 0, err
	}
	out := make([]boostcontract.AccountRow, len(rows))
	for i, r := range rows {
		out[i] = boostcontract.AccountRow{PointAccount: r.PointAccount, Email: r.Email, Nickname: r.DisplayName}
	}
	return out, total, nil
}

func normalizePage(page, size int) (int, int) {
	if page < 1 {
		page = 1
	}
	if size < 1 || size > 100 {
		size = 20
	}
	return page, size
}

// ---- 流水 ----

func (s *PointsStore) LedgerByKey(bizKey string) (*boostdomain.PointLedgerEntry, error) {
	return first[boostdomain.PointLedgerEntry](s.db.Where("biz_key = ?", bizKey))
}

func (s *PointsStore) CreateLedger(entry *boostdomain.PointLedgerEntry) error {
	return s.db.Create(entry).Error
}

func (s *PointsStore) ListLedger(filter boostcontract.LedgerFilter) ([]boostdomain.PointLedgerEntry, int64, error) {
	query := s.db.Model(&boostdomain.PointLedgerEntry{})
	if filter.UserID != 0 {
		query = query.Where("user_id = ?", filter.UserID)
	}
	if filter.Type != "" {
		query = query.Where("type = ?", filter.Type)
	}
	var total int64
	if err := query.Count(&total).Error; err != nil {
		return nil, 0, err
	}
	page, size := normalizePage(filter.Page, filter.PageSize)
	var rows []boostdomain.PointLedgerEntry
	if err := query.Order("id DESC").Offset((page - 1) * size).Limit(size).Find(&rows).Error; err != nil {
		return nil, 0, err
	}
	return rows, total, nil
}

// ---- 批次 ----

func (s *PointsStore) CreateLot(lot *boostdomain.PointLot) error { return s.db.Create(lot).Error }

func (s *PointsStore) SaveLot(lot *boostdomain.PointLot) error {
	return s.db.Model(&boostdomain.PointLot{}).Where("id = ?", lot.ID).Updates(map[string]interface{}{
		"remaining":      lot.Remaining,
		"status":         lot.Status,
		"suggest_refund": lot.SuggestRefund,
		"updated_at":     time.Now(),
	}).Error
}

func (s *PointsStore) GetLot(id uint) (*boostdomain.PointLot, error) {
	return first[boostdomain.PointLot](s.db.Where("id = ?", id))
}

func (s *PointsStore) GetLotBySource(sourceType, sourceRef string) (*boostdomain.PointLot, error) {
	return first[boostdomain.PointLot](s.db.Where("source_type = ? AND source_ref = ?", sourceType, sourceRef))
}

func (s *PointsStore) ActiveLotsFIFO(userID uint) ([]boostdomain.PointLot, error) {
	var lots []boostdomain.PointLot
	err := s.db.Where("user_id = ? AND status = ? AND remaining > 0", userID, boostdomain.LotStatusActive).
		Order("CASE WHEN expires_at IS NULL THEN 1 ELSE 0 END, expires_at ASC, id ASC").
		Find(&lots).Error
	return lots, err
}

func (s *PointsStore) DueLots(userID uint, now time.Time, limit int) ([]boostdomain.PointLot, error) {
	query := s.db.Where("status = ? AND expires_at IS NOT NULL AND expires_at <= ?", boostdomain.LotStatusActive, now)
	if userID != 0 {
		query = query.Where("user_id = ?", userID)
	}
	var lots []boostdomain.PointLot
	err := query.Order("expires_at ASC, id ASC").Limit(limit).Find(&lots).Error
	return lots, err
}

func (s *PointsStore) ListLots(userID uint, includeClosed bool) ([]boostdomain.PointLot, error) {
	query := s.db.Where("user_id = ?", userID)
	if !includeClosed {
		query = query.Where("status = ? AND remaining > 0", boostdomain.LotStatusActive)
	}
	var lots []boostdomain.PointLot
	err := query.Order("CASE WHEN expires_at IS NULL THEN 1 ELSE 0 END, expires_at ASC, id ASC").Limit(500).Find(&lots).Error
	return lots, err
}

// ---- 积分包 ----

// packRow 扫描用的平铺结构（GORM Scan 不支持多层匿名嵌入）。
type packRow struct {
	ID              uint         `gorm:"column:id"`
	ProductID       uint         `gorm:"column:product_id"`
	SKUID           uint         `gorm:"column:sku_id"`
	Name            string       `gorm:"column:name"`
	Points          int64        `gorm:"column:points"`
	Bonus           int64        `gorm:"column:bonus"`
	ValidDays       *int         `gorm:"column:valid_days"`
	Active          bool         `gorm:"column:active"`
	CreatedAt       time.Time    `gorm:"column:created_at"`
	UpdatedAt       time.Time    `gorm:"column:updated_at"`
	ProductSlug     string       `gorm:"column:product_slug"`
	TitleJSON       string       `gorm:"column:title_json"`
	ProductActive   bool         `gorm:"column:product_active"`
	FulfillmentType string       `gorm:"column:fulfillment_type"`
	SKUCode         string       `gorm:"column:sku_code"`
	SKUActive       bool         `gorm:"column:sku_active"`
	Price           money.Amount `gorm:"column:price"`
}

func defaultTitle(raw string) string {
	var m map[string]interface{}
	if json.Unmarshal([]byte(raw), &m) != nil {
		return ""
	}
	for _, key := range []string{"zh-CN", "zh-TW", "en-US"} {
		if v, ok := m[key].(string); ok && strings.TrimSpace(v) != "" {
			return v
		}
	}
	for _, v := range m {
		if str, ok := v.(string); ok && str != "" {
			return str
		}
	}
	return ""
}

// ListPacks 积分包和对应商品 / SKU。activeOnly 只返回积分包启用、商品上架、SKU 启用的。
func (s *PointsStore) ListPacks(activeOnly bool) ([]boostcontract.PackView, error) {
	query := s.db.Table("boost_point_packs AS p").
		Joins("LEFT JOIN products AS pr ON pr.id = p.product_id AND pr.deleted_at IS NULL").
		Joins("LEFT JOIN product_skus AS k ON k.id = p.sku_id AND k.deleted_at IS NULL")
	if activeOnly {
		query = query.Where("p.active = ? AND pr.is_active = ? AND k.is_active = ?", true, true, true)
	}
	var rows []packRow
	if err := query.Select("p.*, pr.slug AS product_slug, pr.title_json AS title_json, pr.is_active AS product_active, " +
		"pr.fulfillment_type AS fulfillment_type, k.sku_code AS sku_code, k.is_active AS sku_active, k.price_amount AS price").
		Order("p.points + p.bonus ASC, p.id ASC").
		Scan(&rows).Error; err != nil {
		return nil, err
	}
	out := make([]boostcontract.PackView, len(rows))
	for i, r := range rows {
		out[i] = boostcontract.PackView{
			PointPack: boostdomain.PointPack{
				ID: r.ID, ProductID: r.ProductID, SKUID: r.SKUID, Name: r.Name, Points: r.Points, Bonus: r.Bonus,
				ValidDays: r.ValidDays, Active: r.Active, CreatedAt: r.CreatedAt, UpdatedAt: r.UpdatedAt,
			},
			ProductSlug:     r.ProductSlug,
			ProductTitle:    defaultTitle(r.TitleJSON),
			ProductActive:   r.ProductActive,
			FulfillmentType: r.FulfillmentType,
			SKUCode:         r.SKUCode,
			SKUActive:       r.SKUActive,
			Price:           r.Price,
		}
	}
	return out, nil
}

func (s *PointsStore) GetPack(id uint) (*boostdomain.PointPack, error) {
	return first[boostdomain.PointPack](s.db.Where("id = ?", id))
}

func (s *PointsStore) GetPackBySKU(skuID uint) (*boostdomain.PointPack, error) {
	return first[boostdomain.PointPack](s.db.Where("sku_id = ?", skuID))
}

func (s *PointsStore) CreatePack(pack *boostdomain.PointPack) error { return s.db.Create(pack).Error }

func (s *PointsStore) SavePack(pack *boostdomain.PointPack) error {
	return s.db.Model(&boostdomain.PointPack{}).Where("id = ?", pack.ID).Updates(map[string]interface{}{
		"name":       pack.Name,
		"points":     pack.Points,
		"bonus":      pack.Bonus,
		"valid_days": pack.ValidDays,
		"active":     pack.Active,
		"updated_at": time.Now(),
	}).Error
}

// GetSKURef 读取 SKU 及其商品的交付类型（只读 Dujiao 原表）。
func (s *PointsStore) GetSKURef(skuID uint) (*boostcontract.SKURef, error) {
	var rows []struct {
		ProductID       uint   `gorm:"column:product_id"`
		SKUID           uint   `gorm:"column:sku_id"`
		FulfillmentType string `gorm:"column:fulfillment_type"`
	}
	err := s.db.Table("product_skus AS k").
		Joins("JOIN products AS pr ON pr.id = k.product_id AND pr.deleted_at IS NULL").
		Where("k.id = ? AND k.deleted_at IS NULL", skuID).
		Select("k.product_id AS product_id, k.id AS sku_id, pr.fulfillment_type AS fulfillment_type").
		Limit(1).Scan(&rows).Error
	if err != nil || len(rows) == 0 {
		return nil, err
	}
	r := rows[0]
	return &boostcontract.SKURef{ProductID: r.ProductID, SKUID: r.SKUID, FulfillmentType: r.FulfillmentType}, nil
}
