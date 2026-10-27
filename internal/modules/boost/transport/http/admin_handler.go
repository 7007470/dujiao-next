// Package boosthttp 是代练模块的 HTTP 入口。
package boosthttp

import (
	"encoding/json"
	"errors"
	"net/http"
	"strconv"
	"strings"

	auditlogapp "github.com/dujiao-next/internal/modules/auditlog/application"
	boostapp "github.com/dujiao-next/internal/modules/boost/application"
	boostcontract "github.com/dujiao-next/internal/modules/boost/contract"
	boostdomain "github.com/dujiao-next/internal/modules/boost/domain"
	"github.com/dujiao-next/internal/modules/boost/domain/featuredef"
	"github.com/dujiao-next/internal/platform/http/ginutil"
	"github.com/dujiao-next/internal/platform/http/response"
	"github.com/dujiao-next/internal/shared/jsonmap"

	"github.com/gin-gonic/gin"
)

// AuditRecorder 记录后台写操作。生产环境传入 Dujiao 的权限审计服务（authz_audit_logs 表，后台"审计日志"页可查）。
type AuditRecorder interface {
	Record(input auditlogapp.AuthzRecord) error
}

// AdminHandler 处理后台游戏 / 区服 / 功能请求。
type AdminHandler struct {
	catalog *boostapp.CatalogService
	audit   AuditRecorder
}

// NewAdminHandler 创建后台处理器。audit 可以为 nil（测试）。
func NewAdminHandler(catalog *boostapp.CatalogService, audit AuditRecorder) *AdminHandler {
	if catalog == nil {
		panic("boost admin handler: catalog is nil")
	}
	return &AdminHandler{catalog: catalog, audit: audit}
}

func (h *AdminHandler) record(c *gin.Context, action, object string, detail map[string]interface{}) {
	if h.audit == nil {
		return
	}
	err := h.audit.Record(auditlogapp.AuthzRecord{
		OperatorAdminID:  c.GetUint("admin_id"),
		OperatorUsername: strings.TrimSpace(c.GetString("username")),
		Action:           action,
		Object:           object,
		Method:           c.Request.Method,
		RequestID:        strings.TrimSpace(c.GetString("request_id")),
		Detail:           jsonmap.JSON(detail),
	})
	if err != nil {
		// 审计写入失败不影响业务结果，但必须留下日志
		ginutil.RequestLog(c).Errorw("boost_admin_audit_failed", "action", action, "object", object, "error", err)
	}
}

// respond 把用例错误映射成统一响应。ValidationError 的中文提示直接给管理员看。
func respond(c *gin.Context, err error) {
	if ve, ok := boostapp.AsValidationError(err); ok {
		code := response.CodeBadRequest
		if errors.Is(err, boostcontract.ErrDefinition) {
			c.JSON(http.StatusOK, response.Response{
				StatusCode: code,
				Msg:        ve.Msg,
				Data:       gin.H{"errors": ve.Issues},
			})
			return
		}
		response.Error(c, code, ve.Msg)
		return
	}
	switch {
	case errors.Is(err, boostcontract.ErrNotFound):
		ginutil.RespondError(c, response.CodeNotFound, "error.boost_not_found", nil)
	case errors.Is(err, boostcontract.ErrVersionConflict):
		ginutil.RespondError(c, response.CodeBadRequest, "error.boost_version_conflict", nil)
	default:
		ginutil.RespondError(c, response.CodeInternal, "error.boost_internal", err)
	}
}

func paramID(c *gin.Context, key string) (uint, bool) {
	id, err := ginutil.ParseParamUint(c, key)
	if err != nil {
		ginutil.RespondError(c, response.CodeBadRequest, "error.bad_request", err)
		return 0, false
	}
	return id, true
}

// ---------------------------------------------------------------------------
// 游戏
// ---------------------------------------------------------------------------

type gameRequest struct {
	Version   *int   `json:"version"`
	Code      string `json:"code"`
	Name      string `json:"name"`
	ShortName string `json:"short_name"`
	Color     string `json:"color"`
	IconURL   string `json:"icon_url"`
	Sort      int    `json:"sort"`
}

type statusRequest struct {
	Status string `json:"status" binding:"required"`
}

// ListGames GET /boost/games
func (h *AdminHandler) ListGames(c *gin.Context) {
	games, err := h.catalog.ListGames(boostcontract.GameListFilter{Status: strings.TrimSpace(c.Query("status"))})
	if err != nil {
		respond(c, err)
		return
	}
	response.Success(c, games)
}

// GetGame GET /boost/games/:id
func (h *AdminHandler) GetGame(c *gin.Context) {
	id, ok := paramID(c, "id")
	if !ok {
		return
	}
	game, err := h.catalog.GetGame(id)
	if err != nil {
		respond(c, err)
		return
	}
	response.Success(c, game)
}

// CreateGame POST /boost/games
func (h *AdminHandler) CreateGame(c *gin.Context) {
	h.saveGame(c, 0)
}

// UpdateGame PUT /boost/games/:id
func (h *AdminHandler) UpdateGame(c *gin.Context) {
	id, ok := paramID(c, "id")
	if !ok {
		return
	}
	h.saveGame(c, id)
}

func (h *AdminHandler) saveGame(c *gin.Context, id uint) {
	var req gameRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		ginutil.RespondBindError(c, err)
		return
	}
	game, err := h.catalog.SaveGame(boostapp.GameInput{
		ID: id, Version: req.Version, Code: req.Code, Name: req.Name,
		ShortName: req.ShortName, Color: req.Color, IconURL: req.IconURL, Sort: req.Sort,
	})
	if err != nil {
		respond(c, err)
		return
	}
	action := "boost.game.update"
	if id == 0 {
		action = "boost.game.create"
	}
	h.record(c, action, "game:"+strconv.FormatUint(uint64(game.ID), 10), map[string]interface{}{"code": game.Code, "name": game.Name, "version": game.Version})
	response.Success(c, game)
}

// SetGameStatus POST /boost/games/:id/status
func (h *AdminHandler) SetGameStatus(c *gin.Context) {
	id, ok := paramID(c, "id")
	if !ok {
		return
	}
	var req statusRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		ginutil.RespondBindError(c, err)
		return
	}
	game, err := h.catalog.SetGameStatus(id, req.Status)
	if err != nil {
		respond(c, err)
		return
	}
	h.record(c, "boost.game.status", "game:"+strconv.FormatUint(uint64(game.ID), 10), map[string]interface{}{"status": game.Status})
	response.Success(c, game)
}

// ---------------------------------------------------------------------------
// 区服
// ---------------------------------------------------------------------------

type serverRequest struct {
	Code     string `json:"code"`
	Name     string `json:"name"`
	Platform string `json:"platform"`
	Sort     int    `json:"sort"`
}

// ListServers GET /boost/games/:id/servers
func (h *AdminHandler) ListServers(c *gin.Context) {
	id, ok := paramID(c, "id")
	if !ok {
		return
	}
	servers, err := h.catalog.ListServers(id)
	if err != nil {
		respond(c, err)
		return
	}
	response.Success(c, emptyIfNil(servers))
}

// CreateServer POST /boost/games/:id/servers
func (h *AdminHandler) CreateServer(c *gin.Context) {
	gameID, ok := paramID(c, "id")
	if !ok {
		return
	}
	h.saveServer(c, gameID, 0)
}

// UpdateServer PUT /boost/games/:id/servers/:server_id
func (h *AdminHandler) UpdateServer(c *gin.Context) {
	gameID, ok := paramID(c, "id")
	if !ok {
		return
	}
	serverID, ok := paramID(c, "server_id")
	if !ok {
		return
	}
	h.saveServer(c, gameID, serverID)
}

func (h *AdminHandler) saveServer(c *gin.Context, gameID, serverID uint) {
	var req serverRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		ginutil.RespondBindError(c, err)
		return
	}
	server, err := h.catalog.SaveServer(boostapp.ServerInput{
		ID: serverID, GameID: gameID, Code: req.Code, Name: req.Name, Platform: req.Platform, Sort: req.Sort,
	})
	if err != nil {
		respond(c, err)
		return
	}
	action := "boost.server.update"
	if serverID == 0 {
		action = "boost.server.create"
	}
	h.record(c, action, "server:"+strconv.FormatUint(uint64(server.ID), 10), map[string]interface{}{"game_id": server.GameID, "code": server.Code, "name": server.Name})
	response.Success(c, server)
}

// SetServerStatus POST /boost/games/:id/servers/:server_id/status
func (h *AdminHandler) SetServerStatus(c *gin.Context) {
	gameID, ok := paramID(c, "id")
	if !ok {
		return
	}
	serverID, ok := paramID(c, "server_id")
	if !ok {
		return
	}
	var req statusRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		ginutil.RespondBindError(c, err)
		return
	}
	servers, err := h.catalog.ListServers(gameID)
	if err != nil {
		respond(c, err)
		return
	}
	if !containsServer(servers, serverID) {
		respond(c, boostcontract.ErrNotFound)
		return
	}
	server, err := h.catalog.SetServerStatus(serverID, req.Status)
	if err != nil {
		respond(c, err)
		return
	}
	h.record(c, "boost.server.status", "server:"+strconv.FormatUint(uint64(server.ID), 10), map[string]interface{}{"status": server.Status})
	response.Success(c, server)
}

func containsServer(servers []boostdomain.GameServer, id uint) bool {
	for _, s := range servers {
		if s.ID == id {
			return true
		}
	}
	return false
}

// ---------------------------------------------------------------------------
// 功能
// ---------------------------------------------------------------------------

// featureView 是返回给后台的功能：definition 以 JSON 对象而不是字符串返回。
type featureView struct {
	ID         uint            `json:"id"`
	GameID     uint            `json:"game_id"`
	Code       string          `json:"code"`
	Name       string          `json:"name"`
	Status     string          `json:"status"`
	Sort       int             `json:"sort"`
	Version    int             `json:"version"`
	Definition json.RawMessage `json:"definition"`
	CreatedAt  string          `json:"created_at"`
	UpdatedAt  string          `json:"updated_at"`
}

func presentFeature(f *boostdomain.Feature) featureView {
	name := ""
	if def, err := featuredef.ParseValue([]byte(f.DefinitionJSON)); err == nil {
		name, _ = def.Get("name").Str()
	}
	return featureView{
		ID: f.ID, GameID: f.GameID, Code: f.Code, Name: name, Status: f.Status, Sort: f.Sort, Version: f.Version,
		Definition: json.RawMessage(f.DefinitionJSON),
		CreatedAt:  f.CreatedAt.Format("2006-01-02T15:04:05Z07:00"),
		UpdatedAt:  f.UpdatedAt.Format("2006-01-02T15:04:05Z07:00"),
	}
}

func presentFeatures(list []boostdomain.Feature) []featureView {
	out := make([]featureView, 0, len(list))
	for i := range list {
		out = append(out, presentFeature(&list[i]))
	}
	return out
}

type featureRequest struct {
	GameID     uint            `json:"game_id"`
	Version    *int            `json:"version"`
	Definition json.RawMessage `json:"definition"`
}

// definitionBytes 接受 JSON 对象或 JSON 字符串（编辑器原文），统一返回 JSON 原文。
func definitionBytes(raw json.RawMessage) []byte {
	trimmed := strings.TrimSpace(string(raw))
	if strings.HasPrefix(trimmed, `"`) {
		var text string
		if err := json.Unmarshal(raw, &text); err == nil {
			return []byte(text)
		}
	}
	return raw
}

// ListFeatures GET /boost/games/:id/features
func (h *AdminHandler) ListFeatures(c *gin.Context) {
	id, ok := paramID(c, "id")
	if !ok {
		return
	}
	list, err := h.catalog.ListFeatures(id)
	if err != nil {
		respond(c, err)
		return
	}
	response.Success(c, presentFeatures(list))
}

// GetFeature GET /boost/features/:id
func (h *AdminHandler) GetFeature(c *gin.Context) {
	id, ok := paramID(c, "id")
	if !ok {
		return
	}
	feature, err := h.catalog.GetFeature(id)
	if err != nil {
		respond(c, err)
		return
	}
	response.Success(c, presentFeature(feature))
}

// CreateFeature POST /boost/features
func (h *AdminHandler) CreateFeature(c *gin.Context) {
	h.saveFeature(c, 0)
}

// UpdateFeature PUT /boost/features/:id
func (h *AdminHandler) UpdateFeature(c *gin.Context) {
	id, ok := paramID(c, "id")
	if !ok {
		return
	}
	h.saveFeature(c, id)
}

func (h *AdminHandler) saveFeature(c *gin.Context, id uint) {
	adminID, ok := ginutil.GetAdminID(c)
	if !ok {
		return
	}
	var req featureRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		ginutil.RespondBindError(c, err)
		return
	}
	gameID := req.GameID
	if id != 0 && gameID == 0 {
		existing, err := h.catalog.GetFeature(id)
		if err != nil {
			respond(c, err)
			return
		}
		gameID = existing.GameID
	}
	result, err := h.catalog.SaveFeature(boostapp.FeatureInput{
		ID: id, GameID: gameID, Version: req.Version, Definition: definitionBytes(req.Definition), AdminID: &adminID,
	})
	if err != nil {
		respond(c, err)
		return
	}
	if result.Changed {
		action := "boost.feature.update"
		if id == 0 {
			action = "boost.feature.create"
		}
		h.record(c, action, "feature:"+strconv.FormatUint(uint64(result.Feature.ID), 10), map[string]interface{}{"game_id": result.Feature.GameID, "code": result.Feature.Code, "version": result.Feature.Version})
	}
	response.Success(c, gin.H{
		"feature":  presentFeature(result.Feature),
		"changed":  result.Changed,
		"warnings": result.Warnings,
	})
}

// SetFeatureStatus POST /boost/features/:id/status
func (h *AdminHandler) SetFeatureStatus(c *gin.Context) {
	id, ok := paramID(c, "id")
	if !ok {
		return
	}
	var req statusRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		ginutil.RespondBindError(c, err)
		return
	}
	feature, err := h.catalog.SetFeatureStatus(id, req.Status)
	if err != nil {
		respond(c, err)
		return
	}
	h.record(c, "boost.feature.status", "feature:"+strconv.FormatUint(uint64(feature.ID), 10), map[string]interface{}{"status": feature.Status})
	response.Success(c, presentFeature(feature))
}

type moveRequest struct {
	Delta int `json:"delta" binding:"required"`
}

// MoveFeature POST /boost/features/:id/move
func (h *AdminHandler) MoveFeature(c *gin.Context) {
	id, ok := paramID(c, "id")
	if !ok {
		return
	}
	var req moveRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		ginutil.RespondBindError(c, err)
		return
	}
	list, err := h.catalog.MoveFeature(id, req.Delta)
	if err != nil {
		respond(c, err)
		return
	}
	h.record(c, "boost.feature.move", "feature:"+strconv.FormatUint(uint64(id), 10), map[string]interface{}{"delta": req.Delta})
	response.Success(c, presentFeatures(list))
}

type versionView struct {
	FeatureID  uint            `json:"feature_id"`
	Version    int             `json:"version"`
	AdminID    *uint           `json:"admin_id,omitempty"`
	CreatedAt  string          `json:"created_at"`
	Definition json.RawMessage `json:"definition,omitempty"`
}

// ListFeatureVersions GET /boost/features/:id/versions（不含定义原文，列表轻量）
func (h *AdminHandler) ListFeatureVersions(c *gin.Context) {
	id, ok := paramID(c, "id")
	if !ok {
		return
	}
	versions, err := h.catalog.ListFeatureVersions(id)
	if err != nil {
		respond(c, err)
		return
	}
	out := make([]versionView, 0, len(versions))
	for _, v := range versions {
		out = append(out, versionView{FeatureID: v.FeatureID, Version: v.Version, AdminID: v.AdminID, CreatedAt: v.CreatedAt.Format("2006-01-02T15:04:05Z07:00")})
	}
	response.Success(c, out)
}

// GetFeatureVersion GET /boost/features/:id/versions/:version
func (h *AdminHandler) GetFeatureVersion(c *gin.Context) {
	id, ok := paramID(c, "id")
	if !ok {
		return
	}
	version, err := strconv.Atoi(c.Param("version"))
	if err != nil || version <= 0 {
		ginutil.RespondError(c, response.CodeBadRequest, "error.bad_request", err)
		return
	}
	v, err := h.catalog.GetFeatureVersion(id, version)
	if err != nil {
		respond(c, err)
		return
	}
	response.Success(c, versionView{
		FeatureID: v.FeatureID, Version: v.Version, AdminID: v.AdminID,
		CreatedAt: v.CreatedAt.Format("2006-01-02T15:04:05Z07:00"), Definition: json.RawMessage(v.DefinitionJSON),
	})
}

type validateRequest struct {
	Definition json.RawMessage `json:"definition"`
}

// ValidateFeature POST /boost/features/validate —— 编辑器实时校验，不保存。
// 始终返回 200 + 校验结果（ok / errors / warnings），由前端标出出错位置。
func (h *AdminHandler) ValidateFeature(c *gin.Context) {
	var req validateRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		ginutil.RespondBindError(c, err)
		return
	}
	response.Success(c, h.catalog.ValidateDefinition(definitionBytes(req.Definition)))
}

type previewRequest struct {
	Definition   json.RawMessage `json:"definition"`
	Params       json.RawMessage `json:"params"`
	DiscountRate int             `json:"discount_rate"`
}

// PreviewFeature POST /boost/features/preview —— 按编辑器里的定义和试填参数编译报价，不保存。
func (h *AdminHandler) PreviewFeature(c *gin.Context) {
	var req previewRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		ginutil.RespondBindError(c, err)
		return
	}
	if req.DiscountRate < 0 || req.DiscountRate > 100 {
		response.Error(c, response.CodeBadRequest, "discount_rate 必须在 0–100 之间")
		return
	}
	plan, err := h.catalog.Preview(boostapp.PreviewInput{
		Definition: definitionBytes(req.Definition), Params: req.Params, DiscountRate: req.DiscountRate,
	})
	if err != nil {
		respond(c, err)
		return
	}
	response.Success(c, plan)
}

func emptyIfNil[T any](items []T) []T {
	if items == nil {
		return []T{}
	}
	return items
}
