package boosthttp

import (
	"errors"
	"strconv"
	"strings"

	boostapp "github.com/dujiao-next/internal/modules/boost/application"
	boostcontract "github.com/dujiao-next/internal/modules/boost/contract"
	boostdomain "github.com/dujiao-next/internal/modules/boost/domain"
	"github.com/dujiao-next/internal/platform/http/ginutil"
	"github.com/dujiao-next/internal/platform/http/response"

	"github.com/gin-gonic/gin"
)

// PointsHandler 处理积分相关请求（后台和用户端共用一个处理器，路由分开注册）。
type PointsHandler struct {
	points *boostapp.PointsService
	audit  AuditRecorder
}

// NewPointsHandler 创建积分处理器。audit 可以为 nil（测试）。
func NewPointsHandler(points *boostapp.PointsService, audit AuditRecorder) *PointsHandler {
	if points == nil {
		panic("boost points handler: points is nil")
	}
	return &PointsHandler{points: points, audit: audit}
}

func (h *PointsHandler) record(c *gin.Context, action, object string, detail map[string]interface{}) {
	(&AdminHandler{audit: h.audit}).record(c, action, object, detail)
}

func respondPoints(c *gin.Context, err error) {
	if _, ok := boostapp.AsValidationError(err); ok {
		respond(c, err)
		return
	}
	switch {
	case errors.Is(err, boostcontract.ErrPointsInsufficient):
		response.Error(c, response.CodeBadRequest, "可用积分不足")
	case errors.Is(err, boostcontract.ErrInvalid):
		ginutil.RespondError(c, response.CodeBadRequest, "error.bad_request", nil)
	default:
		respond(c, err)
	}
}

func pageQuery(c *gin.Context) (int, int) {
	return ginutil.ParsePaginationWithBounds(c, "page", "page_size", 20, 100)
}

// ---------------------------------------------------------------------------
// 后台：积分包
// ---------------------------------------------------------------------------

type packRequest struct {
	SKUID     uint   `json:"sku_id"`
	Name      string `json:"name"`
	Points    int64  `json:"points"`
	Bonus     int64  `json:"bonus"`
	ValidDays *int   `json:"valid_days"`
	Active    *bool  `json:"active"`
}

// ListPacks GET /boost/point-packs
func (h *PointsHandler) ListPacks(c *gin.Context) {
	packs, err := h.points.ListPacks()
	if err != nil {
		respondPoints(c, err)
		return
	}
	response.Success(c, packs)
}

// CreatePack POST /boost/point-packs
func (h *PointsHandler) CreatePack(c *gin.Context) {
	var req packRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		ginutil.RespondBindError(c, err)
		return
	}
	if req.SKUID == 0 {
		response.Error(c, response.CodeBadRequest, "请选择商品 SKU")
		return
	}
	pack, err := h.points.SavePack(boostapp.PackInput{SKUID: req.SKUID, Name: req.Name, Points: req.Points, Bonus: req.Bonus, ValidDays: req.ValidDays, Active: req.Active})
	if err != nil {
		respondPoints(c, err)
		return
	}
	h.record(c, "boost_point_pack_create", "boost_point_pack:"+strconv.FormatUint(uint64(pack.ID), 10), map[string]interface{}{
		"sku_id": pack.SKUID, "points": pack.Points, "bonus": pack.Bonus, "valid_days": pack.ValidDays,
	})
	response.Success(c, pack)
}

// UpdatePack PUT /boost/point-packs/:id
func (h *PointsHandler) UpdatePack(c *gin.Context) {
	id, ok := paramID(c, "id")
	if !ok {
		return
	}
	var req packRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		ginutil.RespondBindError(c, err)
		return
	}
	pack, err := h.points.SavePack(boostapp.PackInput{ID: id, SKUID: req.SKUID, Name: req.Name, Points: req.Points, Bonus: req.Bonus, ValidDays: req.ValidDays, Active: req.Active})
	if err != nil {
		respondPoints(c, err)
		return
	}
	h.record(c, "boost_point_pack_update", "boost_point_pack:"+strconv.FormatUint(uint64(pack.ID), 10), map[string]interface{}{
		"points": pack.Points, "bonus": pack.Bonus, "valid_days": pack.ValidDays, "active": pack.Active,
	})
	response.Success(c, pack)
}

// ---------------------------------------------------------------------------
// 后台：账户、流水、发放 / 扣减、撤回
// ---------------------------------------------------------------------------

// ListAccounts GET /boost/point-accounts?keyword=&user_id=
func (h *PointsHandler) ListAccounts(c *gin.Context) {
	page, size := pageQuery(c)
	userID, _ := ginutil.ParseQueryUint(c.Query("user_id"), false)
	rows, total, err := h.points.Accounts(boostcontract.AccountFilter{UserID: userID, Keyword: c.Query("keyword"), Page: page, PageSize: size})
	if err != nil {
		respondPoints(c, err)
		return
	}
	response.SuccessWithPage(c, rows, response.BuildPagination(page, size, total))
}

// GetUserPoints GET /boost/users/:id/points —— 概览 + 批次（含已关闭）
func (h *PointsHandler) GetUserPoints(c *gin.Context) {
	userID, ok := paramID(c, "id")
	if !ok {
		return
	}
	summary, err := h.points.Summary(userID)
	if err != nil {
		respondPoints(c, err)
		return
	}
	lots, err := h.points.Lots(userID, true)
	if err != nil {
		respondPoints(c, err)
		return
	}
	response.Success(c, gin.H{"summary": summary, "lots": lots})
}

// ListLedger GET /boost/point-ledger?user_id=&type=
func (h *PointsHandler) ListLedger(c *gin.Context) {
	page, size := pageQuery(c)
	userID, _ := ginutil.ParseQueryUint(c.Query("user_id"), false)
	rows, total, err := h.points.Ledger(boostcontract.LedgerFilter{UserID: userID, Type: strings.TrimSpace(c.Query("type")), Page: page, PageSize: size})
	if err != nil {
		respondPoints(c, err)
		return
	}
	response.SuccessWithPage(c, rows, response.BuildPagination(page, size, total))
}

type adjustRequest struct {
	UserID    uint   `json:"user_id" binding:"required"`
	Amount    int64  `json:"amount"`
	ValidDays *int   `json:"valid_days"`
	Reason    string `json:"reason"`
	RequestID string `json:"request_id"`
}

func (h *PointsHandler) adjust(c *gin.Context, deduct bool) {
	var req adjustRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		ginutil.RespondBindError(c, err)
		return
	}
	adminID := c.GetUint("admin_id")
	input := boostapp.AdminChangeInput{AdminID: adminID, UserID: req.UserID, Amount: req.Amount, ValidDays: req.ValidDays, Reason: req.Reason, RequestID: req.RequestID}
	var (
		entry *boostdomain.PointLedgerEntry
		err   error
	)
	action := "boost_points_grant"
	if deduct {
		action = "boost_points_deduct"
		entry, err = h.points.AdminDeduct(input)
	} else {
		entry, err = h.points.AdminGrant(input)
	}
	if err != nil {
		respondPoints(c, err)
		return
	}
	h.record(c, action, "user:"+strconv.FormatUint(uint64(req.UserID), 10), map[string]interface{}{
		"amount": req.Amount, "reason": strings.TrimSpace(req.Reason), "biz_key": entry.BizKey, "valid_days": req.ValidDays,
	})
	response.Success(c, entry)
}

// Grant POST /boost/points/grant
func (h *PointsHandler) Grant(c *gin.Context) { h.adjust(c, false) }

// Deduct POST /boost/points/deduct
func (h *PointsHandler) Deduct(c *gin.Context) { h.adjust(c, true) }

type revokeRequest struct {
	OrderNo string `json:"order_no" binding:"required"`
}

// RevokeOrder POST /boost/points/revoke-order —— 积分包退款前撤回剩余积分，返回建议退款金额
func (h *PointsHandler) RevokeOrder(c *gin.Context) {
	var req revokeRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		ginutil.RespondBindError(c, err)
		return
	}
	result, err := h.points.RevokeForOrder(c.GetUint("admin_id"), req.OrderNo)
	if err != nil {
		respondPoints(c, err)
		return
	}
	if !result.Already {
		h.record(c, "boost_points_revoke_order", "order:"+result.OrderNo, map[string]interface{}{
			"total": result.Total, "revoked": result.Revoked, "suggest_refund": result.SuggestRefund.String(),
		})
	}
	response.Success(c, result)
}

// ---------------------------------------------------------------------------
// 用户端
// ---------------------------------------------------------------------------

// PublicPacks GET /public/boost/point-packs —— 可购买的积分包（售价以商品 SKU 为准）
func (h *PointsHandler) PublicPacks(c *gin.Context) {
	packs, err := h.points.ListActivePacks()
	if err != nil {
		respondPoints(c, err)
		return
	}
	type view struct {
		ID          uint   `json:"id"`
		Name        string `json:"name"`
		Points      int64  `json:"points"`
		Bonus       int64  `json:"bonus"`
		ValidDays   *int   `json:"valid_days,omitempty"`
		ProductID   uint   `json:"product_id"`
		ProductSlug string `json:"product_slug"`
		SKUID       uint   `json:"sku_id"`
		Price       string `json:"price"`
	}
	out := make([]view, 0, len(packs))
	for _, p := range packs {
		out = append(out, view{ID: p.ID, Name: p.Name, Points: p.Points, Bonus: p.Bonus, ValidDays: p.ValidDays,
			ProductID: p.ProductID, ProductSlug: p.ProductSlug, SKUID: p.SKUID, Price: p.Price.String()})
	}
	response.Success(c, out)
}

// MySummary GET /boost/points —— 我的积分概览
func (h *PointsHandler) MySummary(c *gin.Context) {
	userID, ok := ginutil.GetUserID(c)
	if !ok {
		return
	}
	summary, err := h.points.Summary(userID)
	if err != nil {
		respondPoints(c, err)
		return
	}
	response.Success(c, summary)
}

// userLedgerView 用户端流水：不暴露操作管理员、批次分配等内部字段。
type userLedgerView struct {
	ID             uint   `json:"id"`
	Type           string `json:"type"`
	DeltaAvailable int64  `json:"delta_available"`
	DeltaFrozen    int64  `json:"delta_frozen"`
	AvailableAfter int64  `json:"available_after"`
	FrozenAfter    int64  `json:"frozen_after"`
	TaskID         *uint  `json:"task_id,omitempty"`
	OrderID        *uint  `json:"order_id,omitempty"`
	Remark         string `json:"remark"`
	CreatedAt      string `json:"created_at"`
}

// MyLedger GET /boost/points/ledger —— 我的积分明细
func (h *PointsHandler) MyLedger(c *gin.Context) {
	userID, ok := ginutil.GetUserID(c)
	if !ok {
		return
	}
	page, size := pageQuery(c)
	rows, total, err := h.points.Ledger(boostcontract.LedgerFilter{UserID: userID, Type: strings.TrimSpace(c.Query("type")), Page: page, PageSize: size})
	if err != nil {
		respondPoints(c, err)
		return
	}
	out := make([]userLedgerView, 0, len(rows))
	for _, r := range rows {
		remark := r.Remark
		if r.OperatorAdminID != nil && (r.Type == boostdomain.LedgerGrant || r.Type == boostdomain.LedgerAdminAdjust) {
			remark = "平台调整：" + remark
		}
		out = append(out, userLedgerView{ID: r.ID, Type: r.Type, DeltaAvailable: r.DeltaAvailable, DeltaFrozen: r.DeltaFrozen,
			AvailableAfter: r.AvailableAfter, FrozenAfter: r.FrozenAfter, TaskID: r.TaskID, OrderID: r.OrderID, Remark: remark,
			CreatedAt: r.CreatedAt.UTC().Format("2006-01-02T15:04:05Z")})
	}
	response.SuccessWithPage(c, out, response.BuildPagination(page, size, total))
}
