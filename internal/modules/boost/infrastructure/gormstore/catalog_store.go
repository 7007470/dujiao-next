package gormstore

import (
	"errors"

	boostcontract "github.com/dujiao-next/internal/modules/boost/contract"
	boostdomain "github.com/dujiao-next/internal/modules/boost/domain"

	"gorm.io/gorm"
)

// CatalogStore 是 boostcontract.CatalogRepository 的 GORM 实现。
type CatalogStore struct {
	db *gorm.DB
}

// NewCatalogStore 创建目录仓储。
func NewCatalogStore(db *gorm.DB) *CatalogStore {
	return &CatalogStore{db: db}
}

var _ boostcontract.CatalogRepository = (*CatalogStore)(nil)

// WithinTransaction 在事务里执行 fn；fn 返回错误时整体回滚。
func (s *CatalogStore) WithinTransaction(fn func(repo boostcontract.CatalogRepository) error) error {
	return s.db.Transaction(func(tx *gorm.DB) error {
		return fn(NewCatalogStore(tx))
	})
}

func first[T any](query *gorm.DB) (*T, error) {
	var item T
	if err := query.Take(&item).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, nil
		}
		return nil, err
	}
	return &item, nil
}

// ---- 游戏 ----

func (s *CatalogStore) ListGames(filter boostcontract.GameListFilter) ([]boostcontract.GameSummary, error) {
	query := s.db.Model(&boostdomain.Game{})
	if filter.Status != "" {
		query = query.Where("status = ?", filter.Status)
	}
	var games []boostdomain.Game
	if err := query.Order("sort ASC, id ASC").Find(&games).Error; err != nil {
		return nil, err
	}
	out := make([]boostcontract.GameSummary, 0, len(games))
	if len(games) == 0 {
		return out, nil
	}
	ids := make([]uint, len(games))
	for i := range games {
		ids[i] = games[i].ID
	}
	type countRow struct {
		GameID uint
		Status string
		N      int64
	}
	var servers, features []countRow
	if err := s.db.Model(&boostdomain.GameServer{}).Select("game_id, status, COUNT(*) AS n").
		Where("game_id IN ?", ids).Group("game_id, status").Scan(&servers).Error; err != nil {
		return nil, err
	}
	if err := s.db.Model(&boostdomain.Feature{}).Select("game_id, status, COUNT(*) AS n").
		Where("game_id IN ?", ids).Group("game_id, status").Scan(&features).Error; err != nil {
		return nil, err
	}
	index := make(map[uint]*boostcontract.GameSummary, len(games))
	for i := range games {
		out = append(out, boostcontract.GameSummary{Game: games[i]})
	}
	for i := range out {
		index[out[i].ID] = &out[i]
	}
	for _, row := range servers {
		item := index[row.GameID]
		item.ServerCount += row.N
		if row.Status == boostdomain.ServerStatusActive {
			item.ActiveServerCount += row.N
		}
	}
	for _, row := range features {
		item := index[row.GameID]
		item.FeatureCount += row.N
		if row.Status == boostdomain.FeatureStatusActive {
			item.ActiveFeatureCount += row.N
		}
	}
	return out, nil
}

func (s *CatalogStore) GetGame(id uint) (*boostdomain.Game, error) {
	if id == 0 {
		return nil, nil
	}
	return first[boostdomain.Game](s.db.Where("id = ?", id))
}

func (s *CatalogStore) GetGameByCode(code string) (*boostdomain.Game, error) {
	return first[boostdomain.Game](s.db.Where("code = ?", code))
}

func (s *CatalogStore) CreateGame(game *boostdomain.Game) error { return s.db.Create(game).Error }
func (s *CatalogStore) SaveGame(game *boostdomain.Game) error   { return s.db.Save(game).Error }

// ---- 区服 ----

func (s *CatalogStore) ListServers(gameID uint) ([]boostdomain.GameServer, error) {
	var servers []boostdomain.GameServer
	err := s.db.Where("game_id = ?", gameID).Order("sort ASC, id ASC").Find(&servers).Error
	return servers, err
}

func (s *CatalogStore) GetServer(id uint) (*boostdomain.GameServer, error) {
	if id == 0 {
		return nil, nil
	}
	return first[boostdomain.GameServer](s.db.Where("id = ?", id))
}

func (s *CatalogStore) GetServerByCode(gameID uint, code string) (*boostdomain.GameServer, error) {
	return first[boostdomain.GameServer](s.db.Where("game_id = ? AND code = ?", gameID, code))
}

func (s *CatalogStore) CreateServer(server *boostdomain.GameServer) error {
	return s.db.Create(server).Error
}

func (s *CatalogStore) SaveServer(server *boostdomain.GameServer) error {
	return s.db.Save(server).Error
}

func (s *CatalogStore) CountServers(gameID uint, status string) (int64, error) {
	var n int64
	err := s.db.Model(&boostdomain.GameServer{}).Where("game_id = ? AND status = ?", gameID, status).Count(&n).Error
	return n, err
}

// ---- 功能 ----

func (s *CatalogStore) ListFeatures(gameID uint) ([]boostdomain.Feature, error) {
	var features []boostdomain.Feature
	err := s.db.Where("game_id = ?", gameID).Order("sort ASC, id ASC").Find(&features).Error
	return features, err
}

func (s *CatalogStore) GetFeature(id uint) (*boostdomain.Feature, error) {
	if id == 0 {
		return nil, nil
	}
	return first[boostdomain.Feature](s.db.Where("id = ?", id))
}

func (s *CatalogStore) GetFeatureByCode(gameID uint, code string) (*boostdomain.Feature, error) {
	return first[boostdomain.Feature](s.db.Where("game_id = ? AND code = ?", gameID, code))
}

func (s *CatalogStore) CreateFeature(feature *boostdomain.Feature) error {
	return s.db.Create(feature).Error
}

func (s *CatalogStore) SaveFeature(feature *boostdomain.Feature) error {
	return s.db.Save(feature).Error
}

func (s *CatalogStore) CountFeatures(gameID uint, status string) (int64, error) {
	var n int64
	err := s.db.Model(&boostdomain.Feature{}).Where("game_id = ? AND status = ?", gameID, status).Count(&n).Error
	return n, err
}

func (s *CatalogStore) MaxFeatureSort(gameID uint) (int, error) {
	var maxSort *int
	err := s.db.Model(&boostdomain.Feature{}).Where("game_id = ?", gameID).Select("MAX(sort)").Scan(&maxSort).Error
	if err != nil || maxSort == nil {
		return -1, err
	}
	return *maxSort, nil
}

func (s *CatalogStore) CreateFeatureVersion(version *boostdomain.FeatureVersion) error {
	return s.db.Create(version).Error
}

func (s *CatalogStore) ListFeatureVersions(featureID uint) ([]boostdomain.FeatureVersion, error) {
	var versions []boostdomain.FeatureVersion
	err := s.db.Where("feature_id = ?", featureID).Order("version DESC").Find(&versions).Error
	return versions, err
}

func (s *CatalogStore) GetFeatureVersion(featureID uint, version int) (*boostdomain.FeatureVersion, error) {
	return first[boostdomain.FeatureVersion](s.db.Where("feature_id = ? AND version = ?", featureID, version))
}
