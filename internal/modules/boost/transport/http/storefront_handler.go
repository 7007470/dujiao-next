package boosthttp

import (
	"encoding/json"
	"strings"

	boostapp "github.com/dujiao-next/internal/modules/boost/application"
	boostdomain "github.com/dujiao-next/internal/modules/boost/domain"
	"github.com/dujiao-next/internal/platform/http/ginutil"
	"github.com/dujiao-next/internal/platform/http/response"

	"github.com/gin-gonic/gin"
)

// StorefrontHandler 处理用户端代练请求（游戏、功能、报价）。
type StorefrontHandler struct {
	store *boostapp.StorefrontService
}

// NewStorefrontHandler 创建用户端处理器。
func NewStorefrontHandler(store *boostapp.StorefrontService) *StorefrontHandler {
	if store == nil {
		panic("boost storefront handler: store is nil")
	}
	return &StorefrontHandler{store: store}
}

// publicFeature 用户端功能：只给渲染表单需要的字段。
type publicFeature struct {
	ID         uint            `json:"id"`
	Code       string          `json:"code"`
	Name       string          `json:"name"`
	Version    int             `json:"version"`
	Definition json.RawMessage `json:"definition"`
}

func presentPublicFeatures(list []boostdomain.Feature) []publicFeature {
	out := make([]publicFeature, 0, len(list))
	for i := range list {
		view := presentFeature(&list[i])
		out = append(out, publicFeature{ID: view.ID, Code: view.Code, Name: view.Name, Version: view.Version, Definition: view.Definition})
	}
	return out
}

// ListGames GET /public/boost/games
func (h *StorefrontHandler) ListGames(c *gin.Context) {
	games, err := h.store.ListGames()
	if err != nil {
		respond(c, err)
		return
	}
	response.Success(c, games)
}

// GetGame GET /public/boost/games/:code
func (h *StorefrontHandler) GetGame(c *gin.Context) {
	game, err := h.store.GetGame(strings.TrimSpace(c.Param("code")))
	if err != nil {
		respond(c, err)
		return
	}
	response.Success(c, gin.H{
		"game":     game.PublicGame,
		"features": presentPublicFeatures(game.Features),
	})
}

type quoteRequest struct {
	FeatureID uint            `json:"feature_id" binding:"required"`
	Params    json.RawMessage `json:"params"`
}

// PublicQuote POST /public/boost/quote —— 未登录报价，不打折。
func (h *StorefrontHandler) PublicQuote(c *gin.Context) {
	h.quote(c, 0)
}

// UserQuote POST /boost/quote —— 登录用户报价，按会员等级折扣。
func (h *StorefrontHandler) UserQuote(c *gin.Context) {
	userID, ok := ginutil.GetUserID(c)
	if !ok {
		return
	}
	h.quote(c, userID)
}

func (h *StorefrontHandler) quote(c *gin.Context, userID uint) {
	var req quoteRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		ginutil.RespondBindError(c, err)
		return
	}
	plan, err := h.store.Quote(boostapp.QuoteInput{FeatureID: req.FeatureID, Params: req.Params, UserID: userID})
	if err != nil {
		respond(c, err)
		return
	}
	response.Success(c, plan)
}
