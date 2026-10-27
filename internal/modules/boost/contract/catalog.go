// Package contract 定义代练模块的端口和错误。
package contract

import (
	"errors"

	boostdomain "github.com/dujiao-next/internal/modules/boost/domain"
)

// 后台目录管理（游戏 / 区服 / 功能）的业务错误。transport 层据此映射错误码和提示。
var (
	ErrInvalid          = errors.New("boost: invalid parameters")
	ErrNotFound         = errors.New("boost: not found")
	ErrDuplicate        = errors.New("boost: duplicate code")
	ErrCodeImmutable    = errors.New("boost: code cannot be changed")
	ErrVersionConflict  = errors.New("boost: version conflict")
	ErrDefinition       = errors.New("boost: feature definition invalid")
	ErrGameNotReady     = errors.New("boost: game needs an active server and an active feature")
	ErrFeatureWrongGame = errors.New("boost: feature belongs to another game")
)

// GameListFilter 后台游戏列表筛选。
type GameListFilter struct {
	Status string
}

// GameSummary 后台游戏列表行，附带区服数和功能数。
type GameSummary struct {
	boostdomain.Game
	ServerCount        int64 `json:"server_count"`
	ActiveServerCount  int64 `json:"active_server_count"`
	FeatureCount       int64 `json:"feature_count"`
	ActiveFeatureCount int64 `json:"active_feature_count"`
}

// CatalogRepository 是后台目录管理的数据端口。所有写操作都应在 WithinTransaction 里调用。
type CatalogRepository interface {
	ListGames(filter GameListFilter) ([]GameSummary, error)
	GetGame(id uint) (*boostdomain.Game, error)
	GetGameByCode(code string) (*boostdomain.Game, error)
	CreateGame(game *boostdomain.Game) error
	SaveGame(game *boostdomain.Game) error

	ListServers(gameID uint) ([]boostdomain.GameServer, error)
	GetServer(id uint) (*boostdomain.GameServer, error)
	GetServerByCode(gameID uint, code string) (*boostdomain.GameServer, error)
	CreateServer(server *boostdomain.GameServer) error
	SaveServer(server *boostdomain.GameServer) error
	CountServers(gameID uint, status string) (int64, error)

	ListFeatures(gameID uint) ([]boostdomain.Feature, error)
	GetFeature(id uint) (*boostdomain.Feature, error)
	GetFeatureByCode(gameID uint, code string) (*boostdomain.Feature, error)
	CreateFeature(feature *boostdomain.Feature) error
	SaveFeature(feature *boostdomain.Feature) error
	CountFeatures(gameID uint, status string) (int64, error)
	MaxFeatureSort(gameID uint) (int, error)

	CreateFeatureVersion(version *boostdomain.FeatureVersion) error
	ListFeatureVersions(featureID uint) ([]boostdomain.FeatureVersion, error)
	GetFeatureVersion(featureID uint, version int) (*boostdomain.FeatureVersion, error)

	WithinTransaction(fn func(repo CatalogRepository) error) error
}
