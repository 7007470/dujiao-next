package application

import (
	"bytes"

	boostcontract "github.com/dujiao-next/internal/modules/boost/contract"
	boostdomain "github.com/dujiao-next/internal/modules/boost/domain"
	"github.com/dujiao-next/internal/modules/boost/domain/featuredef"
)

// DiscountRateResolver 返回用户当前会员等级的折扣率（百分比，100 = 不打折）。
// 未登录、没有等级、等级停用时返回 100。
type DiscountRateResolver interface {
	DiscountRate(userID uint) int
}

// StorefrontService 用户端只读用例：只暴露已上线的游戏、启用的区服和已上架的功能。
type StorefrontService struct {
	repo  boostcontract.CatalogRepository
	rates DiscountRateResolver
}

// NewStorefrontService 创建用户端用例。rates 为 nil 时一律不打折。
func NewStorefrontService(repo boostcontract.CatalogRepository, rates DiscountRateResolver) *StorefrontService {
	if repo == nil {
		panic("boost storefront service: repo is nil")
	}
	return &StorefrontService{repo: repo, rates: rates}
}

// PublicGame 用户端游戏列表项。
type PublicGame struct {
	boostdomain.Game
	Servers      []boostdomain.GameServer `json:"servers"`
	FeatureCount int                      `json:"feature_count"`
}

// PublicGameDetail 用户端游戏详情：区服和已上架的功能（含当前定义）。
type PublicGameDetail struct {
	PublicGame
	Features []boostdomain.Feature `json:"features"`
}

// ListGames 返回已上线的游戏，按 sort 排序。
func (s *StorefrontService) ListGames() ([]PublicGame, error) {
	summaries, err := s.repo.ListGames(boostcontract.GameListFilter{Status: boostdomain.GameStatusActive})
	if err != nil {
		return nil, err
	}
	out := make([]PublicGame, 0, len(summaries))
	for _, summary := range summaries {
		servers, err := s.activeServers(summary.ID)
		if err != nil {
			return nil, err
		}
		out = append(out, PublicGame{Game: summary.Game, Servers: servers, FeatureCount: int(summary.ActiveFeatureCount)})
	}
	return out, nil
}

// GetGame 按 code 返回已上线的游戏；不存在或未上线返回 ErrNotFound。
func (s *StorefrontService) GetGame(code string) (*PublicGameDetail, error) {
	game, err := s.repo.GetGameByCode(code)
	if err != nil {
		return nil, err
	}
	if game == nil || game.Status != boostdomain.GameStatusActive {
		return nil, boostcontract.ErrNotFound
	}
	servers, err := s.activeServers(game.ID)
	if err != nil {
		return nil, err
	}
	all, err := s.repo.ListFeatures(game.ID)
	if err != nil {
		return nil, err
	}
	features := make([]boostdomain.Feature, 0, len(all))
	for _, f := range all {
		if f.Status == boostdomain.FeatureStatusActive {
			features = append(features, f)
		}
	}
	return &PublicGameDetail{
		PublicGame: PublicGame{Game: *game, Servers: servers, FeatureCount: len(features)},
		Features:   features,
	}, nil
}

func (s *StorefrontService) activeServers(gameID uint) ([]boostdomain.GameServer, error) {
	all, err := s.repo.ListServers(gameID)
	if err != nil {
		return nil, err
	}
	out := make([]boostdomain.GameServer, 0, len(all))
	for _, server := range all {
		if server.Status == boostdomain.ServerStatusActive {
			out = append(out, server)
		}
	}
	return out, nil
}

// QuoteInput 用户端报价。UserID 为 0 表示未登录（不打折）。
type QuoteInput struct {
	FeatureID uint
	Params    []byte
	UserID    uint
}

// Quote 按功能的当前定义和用户的会员折扣编译报价。功能未上架或游戏未上线返回 ErrNotFound。
// 前端展示的价格以这里为准；下单时服务端会再算一次。
func (s *StorefrontService) Quote(input QuoteInput) (*featuredef.Plan, error) {
	feature, err := s.repo.GetFeature(input.FeatureID)
	if err != nil {
		return nil, err
	}
	if feature == nil || feature.Status != boostdomain.FeatureStatusActive {
		return nil, boostcontract.ErrNotFound
	}
	game, err := s.repo.GetGame(feature.GameID)
	if err != nil {
		return nil, err
	}
	if game == nil || game.Status != boostdomain.GameStatusActive {
		return nil, boostcontract.ErrNotFound
	}
	def, err := featuredef.ParseValue([]byte(feature.DefinitionJSON))
	if err != nil {
		return nil, err
	}
	params := featuredef.Value{}
	if len(bytes.TrimSpace(input.Params)) > 0 {
		parsed, err := featuredef.ParseValue(input.Params)
		if err != nil {
			return nil, invalid(boostcontract.ErrInvalid, "参数不是合法的 JSON")
		}
		params = parsed
	}
	rate := 100
	if input.UserID != 0 && s.rates != nil {
		rate = s.rates.DiscountRate(input.UserID)
	}
	plan := featuredef.Compile(def, params, rate)
	return &plan, nil
}
