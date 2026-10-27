// Package application 是代练模块的用例层。
package application

import (
	"bytes"
	"errors"
	"fmt"
	"regexp"
	"strings"
	"unicode/utf8"

	boostcontract "github.com/dujiao-next/internal/modules/boost/contract"
	boostdomain "github.com/dujiao-next/internal/modules/boost/domain"
	"github.com/dujiao-next/internal/modules/boost/domain/featuredef"
)

var (
	gameCodeRe   = regexp.MustCompile(`^[a-z][a-z0-9_]{1,31}$`)
	serverCodeRe = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9_-]{0,31}$`)
	colorRe      = regexp.MustCompile(`^#[0-9a-fA-F]{6}$`)
)

const defaultGameColor = "#2f6f6a"

// ValidationError 携带具体的出错原因，transport 层原样展示给管理员。
type ValidationError struct {
	Kind   error              // contract 里的哨兵错误
	Msg    string             // 给管理员看的中文提示
	Issues []featuredef.Issue // 功能定义校验错误（只在 ErrDefinition 时有）
}

func (e *ValidationError) Error() string { return e.Kind.Error() + ": " + e.Msg }
func (e *ValidationError) Unwrap() error { return e.Kind }

func invalid(kind error, format string, args ...interface{}) error {
	return &ValidationError{Kind: kind, Msg: fmt.Sprintf(format, args...)}
}

// CatalogService 后台游戏 / 区服 / 功能管理用例，规则与原型 engine.js adminSave* 一致。
type CatalogService struct {
	repo boostcontract.CatalogRepository
}

// NewCatalogService 创建目录管理用例。
func NewCatalogService(repo boostcontract.CatalogRepository) *CatalogService {
	if repo == nil {
		panic("boost catalog service: repo is nil")
	}
	return &CatalogService{repo: repo}
}

// ---------------------------------------------------------------------------
// 游戏
// ---------------------------------------------------------------------------

// GameInput 新建或修改游戏。修改时 Version 必须等于当前版本，否则返回 ErrVersionConflict。
type GameInput struct {
	ID        uint
	Version   *int
	Code      string
	Name      string
	ShortName string
	Color     string
	Sort      int
}

// ListGames 返回游戏列表（含区服和功能数量）。
func (s *CatalogService) ListGames(filter boostcontract.GameListFilter) ([]boostcontract.GameSummary, error) {
	return s.repo.ListGames(filter)
}

// GetGame 返回游戏；不存在时返回 ErrNotFound。
func (s *CatalogService) GetGame(id uint) (*boostdomain.Game, error) {
	game, err := s.repo.GetGame(id)
	if err != nil {
		return nil, err
	}
	if game == nil {
		return nil, boostcontract.ErrNotFound
	}
	return game, nil
}

// SaveGame 新建（ID 为 0）或修改游戏。code 创建后不可修改。
func (s *CatalogService) SaveGame(input GameInput) (*boostdomain.Game, error) {
	code := strings.TrimSpace(input.Code)
	name := strings.TrimSpace(input.Name)
	if !gameCodeRe.MatchString(code) {
		return nil, invalid(boostcontract.ErrInvalid, "游戏 code 需为 2–32 位小写字母、数字、下划线，并以字母开头")
	}
	if name == "" || utf8.RuneCountInString(name) > 30 {
		return nil, invalid(boostcontract.ErrInvalid, "游戏名称必填，最多 30 个字")
	}
	var saved *boostdomain.Game
	err := s.repo.WithinTransaction(func(repo boostcontract.CatalogRepository) error {
		var game *boostdomain.Game
		if input.ID != 0 {
			existing, err := repo.GetGame(input.ID)
			if err != nil {
				return err
			}
			if existing == nil {
				return boostcontract.ErrNotFound
			}
			if input.Version != nil && *input.Version != existing.Version {
				return boostcontract.ErrVersionConflict
			}
			if existing.Code != code {
				return invalid(boostcontract.ErrCodeImmutable, "游戏 code 创建后不能修改（执行端能力和历史任务都引用它）")
			}
			game = existing
			game.Version++
		} else {
			dup, err := repo.GetGameByCode(code)
			if err != nil {
				return err
			}
			if dup != nil {
				return invalid(boostcontract.ErrDuplicate, "游戏 code「%s」已存在", code)
			}
			game = &boostdomain.Game{Code: code, Status: boostdomain.GameStatusDraft, Version: 1}
		}
		game.Name = name
		game.ShortName = truncateRunes(firstNonEmpty(strings.TrimSpace(input.ShortName), truncateRunes(name, 2)), 4)
		switch {
		case colorRe.MatchString(input.Color):
			game.Color = input.Color
		case game.Color == "":
			game.Color = defaultGameColor
		}
		game.Sort = input.Sort
		if input.ID != 0 {
			if err := repo.SaveGame(game); err != nil {
				return err
			}
		} else if err := repo.CreateGame(game); err != nil {
			return err
		}
		saved = game
		return nil
	})
	return saved, err
}

// SetGameStatus 修改游戏状态。上线前至少要有一个启用的区服和一个已上架的功能。
func (s *CatalogService) SetGameStatus(id uint, status string) (*boostdomain.Game, error) {
	switch status {
	case boostdomain.GameStatusDraft, boostdomain.GameStatusActive, boostdomain.GameStatusHidden:
	default:
		return nil, invalid(boostcontract.ErrInvalid, "游戏状态只能是 draft / active / hidden")
	}
	var saved *boostdomain.Game
	err := s.repo.WithinTransaction(func(repo boostcontract.CatalogRepository) error {
		game, err := repo.GetGame(id)
		if err != nil {
			return err
		}
		if game == nil {
			return boostcontract.ErrNotFound
		}
		if status == boostdomain.GameStatusActive {
			servers, err := repo.CountServers(game.ID, boostdomain.ServerStatusActive)
			if err != nil {
				return err
			}
			if servers == 0 {
				return invalid(boostcontract.ErrGameNotReady, "上线前至少要有一个启用的区服")
			}
			features, err := repo.CountFeatures(game.ID, boostdomain.FeatureStatusActive)
			if err != nil {
				return err
			}
			if features == 0 {
				return invalid(boostcontract.ErrGameNotReady, "上线前至少要有一个已上架的代练功能")
			}
		}
		if game.Status == status {
			saved = game
			return nil
		}
		game.Status = status
		game.Version++
		if err := repo.SaveGame(game); err != nil {
			return err
		}
		saved = game
		return nil
	})
	return saved, err
}

// ---------------------------------------------------------------------------
// 区服
// ---------------------------------------------------------------------------

// ServerInput 新建或修改区服。
type ServerInput struct {
	ID       uint
	GameID   uint
	Code     string
	Name     string
	Platform string
	Sort     int
}

// ListServers 返回游戏的区服。
func (s *CatalogService) ListServers(gameID uint) ([]boostdomain.GameServer, error) {
	if _, err := s.GetGame(gameID); err != nil {
		return nil, err
	}
	return s.repo.ListServers(gameID)
}

// SaveServer 新建或修改区服。区服 code 可以修改，但同一游戏内不能重复。
func (s *CatalogService) SaveServer(input ServerInput) (*boostdomain.GameServer, error) {
	code := strings.TrimSpace(input.Code)
	name := strings.TrimSpace(input.Name)
	if !serverCodeRe.MatchString(code) {
		return nil, invalid(boostcontract.ErrInvalid, "区服 code 需为 1–32 位字母、数字、- _")
	}
	if name == "" || utf8.RuneCountInString(name) > 30 {
		return nil, invalid(boostcontract.ErrInvalid, "区服名称必填，最多 30 个字")
	}
	platform := input.Platform
	switch platform {
	case boostdomain.PlatformAll, boostdomain.PlatformAndroid, boostdomain.PlatformIOS, boostdomain.PlatformPC:
	default:
		platform = boostdomain.PlatformAll
	}
	var saved *boostdomain.GameServer
	err := s.repo.WithinTransaction(func(repo boostcontract.CatalogRepository) error {
		game, err := repo.GetGame(input.GameID)
		if err != nil {
			return err
		}
		if game == nil {
			return boostcontract.ErrNotFound
		}
		dup, err := repo.GetServerByCode(game.ID, code)
		if err != nil {
			return err
		}
		var server *boostdomain.GameServer
		if input.ID != 0 {
			server, err = repo.GetServer(input.ID)
			if err != nil {
				return err
			}
			if server == nil || server.GameID != game.ID {
				return boostcontract.ErrNotFound
			}
			if dup != nil && dup.ID != server.ID {
				return invalid(boostcontract.ErrDuplicate, "区服 code「%s」已存在", code)
			}
		} else {
			if dup != nil {
				return invalid(boostcontract.ErrDuplicate, "区服 code「%s」已存在", code)
			}
			server = &boostdomain.GameServer{GameID: game.ID, Status: boostdomain.ServerStatusActive}
		}
		server.Code, server.Name, server.Platform, server.Sort = code, name, platform, input.Sort
		if input.ID != 0 {
			err = repo.SaveServer(server)
		} else {
			err = repo.CreateServer(server)
		}
		if err != nil {
			return err
		}
		saved = server
		return nil
	})
	return saved, err
}

// SetServerStatus 启用或停用区服。停用游戏唯一的启用区服时，已上线的游戏会被拒绝。
func (s *CatalogService) SetServerStatus(id uint, status string) (*boostdomain.GameServer, error) {
	if status != boostdomain.ServerStatusActive && status != boostdomain.ServerStatusDisabled {
		return nil, invalid(boostcontract.ErrInvalid, "区服状态只能是 active / disabled")
	}
	var saved *boostdomain.GameServer
	err := s.repo.WithinTransaction(func(repo boostcontract.CatalogRepository) error {
		server, err := repo.GetServer(id)
		if err != nil {
			return err
		}
		if server == nil {
			return boostcontract.ErrNotFound
		}
		if status == boostdomain.ServerStatusDisabled && server.Status == boostdomain.ServerStatusActive {
			if err := ensureGameStaysReady(repo, server.GameID, func() (int64, error) {
				return repo.CountServers(server.GameID, boostdomain.ServerStatusActive)
			}, "这是已上线游戏唯一启用的区服，请先隐藏游戏或启用其他区服"); err != nil {
				return err
			}
		}
		server.Status = status
		if err := repo.SaveServer(server); err != nil {
			return err
		}
		saved = server
		return nil
	})
	return saved, err
}

// ensureGameStaysReady 在下架最后一个区服 / 功能前检查：已上线的游戏不能因此变得不满足上线条件。
func ensureGameStaysReady(repo boostcontract.CatalogRepository, gameID uint, countActive func() (int64, error), msg string) error {
	game, err := repo.GetGame(gameID)
	if err != nil {
		return err
	}
	if game == nil || game.Status != boostdomain.GameStatusActive {
		return nil
	}
	active, err := countActive()
	if err != nil {
		return err
	}
	if active <= 1 {
		return invalid(boostcontract.ErrGameNotReady, "%s", msg)
	}
	return nil
}

// ---------------------------------------------------------------------------
// 功能
// ---------------------------------------------------------------------------

// FeatureInput 新建（ID 为 0）或保存功能的新版本。Definition 是 JSON 原文。
type FeatureInput struct {
	ID         uint
	GameID     uint
	Version    *int
	Definition []byte
	AdminID    *uint
}

// FeatureSaveResult 保存结果。Changed=false 表示内容没变，没有生成新版本。
type FeatureSaveResult struct {
	Feature  *boostdomain.Feature `json:"feature"`
	Changed  bool                 `json:"changed"`
	Warnings []featuredef.Issue   `json:"warnings"`
}

// ListFeatures 返回游戏的功能，按 sort 排序。
func (s *CatalogService) ListFeatures(gameID uint) ([]boostdomain.Feature, error) {
	if _, err := s.GetGame(gameID); err != nil {
		return nil, err
	}
	return s.repo.ListFeatures(gameID)
}

// GetFeature 返回功能；不存在时返回 ErrNotFound。
func (s *CatalogService) GetFeature(id uint) (*boostdomain.Feature, error) {
	feature, err := s.repo.GetFeature(id)
	if err != nil {
		return nil, err
	}
	if feature == nil {
		return nil, boostcontract.ErrNotFound
	}
	return feature, nil
}

// ValidateDefinition 只校验不保存，供编辑器实时提示。
func (s *CatalogService) ValidateDefinition(definition []byte) featuredef.Result {
	return featuredef.ValidateJSON(definition)
}

// PreviewInput 后台预览：用编辑器里尚未保存的定义编译报价。
type PreviewInput struct {
	Definition   []byte
	Params       []byte
	DiscountRate int
}

// Preview 校验定义并按参数编译报价。定义不合法时返回 ErrDefinition。
func (s *CatalogService) Preview(input PreviewInput) (*featuredef.Plan, error) {
	def, result := parseAndValidate(input.Definition)
	if !result.OK {
		return nil, definitionError(result)
	}
	params := featuredef.Value{}
	if len(bytes.TrimSpace(input.Params)) > 0 {
		parsed, err := featuredef.ParseValue(input.Params)
		if err != nil {
			return nil, invalid(boostcontract.ErrInvalid, "参数不是合法的 JSON")
		}
		params = parsed
	}
	plan := featuredef.Compile(def, params, input.DiscountRate)
	return &plan, nil
}

// SaveFeature 校验定义并保存为新版本。内容不变不升版本；带旧版本号保存返回 ErrVersionConflict；
// 功能 code 创建后不可修改，也不能移到别的游戏。不合法的定义不会留下任何半成品。
func (s *CatalogService) SaveFeature(input FeatureInput) (*FeatureSaveResult, error) {
	def, result := parseAndValidate(input.Definition)
	if !result.OK {
		return nil, definitionError(result)
	}
	normalized, err := featuredef.Normalize(def).MarshalJSON()
	if err != nil {
		return nil, err
	}
	code, _ := def.Get("code").Str()

	out := &FeatureSaveResult{Warnings: result.Warnings}
	err = s.repo.WithinTransaction(func(repo boostcontract.CatalogRepository) error {
		game, err := repo.GetGame(input.GameID)
		if err != nil {
			return err
		}
		if game == nil {
			return boostcontract.ErrNotFound
		}
		var feature *boostdomain.Feature
		if input.ID != 0 {
			feature, err = repo.GetFeature(input.ID)
			if err != nil {
				return err
			}
			if feature == nil {
				return boostcontract.ErrNotFound
			}
			if input.Version != nil && *input.Version != feature.Version {
				return boostcontract.ErrVersionConflict
			}
			if feature.GameID != game.ID {
				return invalid(boostcontract.ErrFeatureWrongGame, "不能把功能移到另一个游戏")
			}
			if feature.Code != code {
				return invalid(boostcontract.ErrCodeImmutable, "功能 code 创建后不能修改（当前为「%s」）", feature.Code)
			}
			if feature.DefinitionJSON == string(normalized) {
				out.Feature = feature // 内容没变，不升版本
				return nil
			}
			feature.Version++
			feature.DefinitionJSON = string(normalized)
			if err := repo.SaveFeature(feature); err != nil {
				return err
			}
		} else {
			dup, err := repo.GetFeatureByCode(game.ID, code)
			if err != nil {
				return err
			}
			if dup != nil {
				return invalid(boostcontract.ErrDuplicate, "该游戏已有功能 code「%s」", code)
			}
			maxSort, err := repo.MaxFeatureSort(game.ID)
			if err != nil {
				return err
			}
			feature = &boostdomain.Feature{
				GameID: game.ID, Code: code, Status: boostdomain.FeatureStatusDraft,
				Sort: maxSort + 1, Version: 1, DefinitionJSON: string(normalized),
			}
			if err := repo.CreateFeature(feature); err != nil {
				return err
			}
		}
		if err := repo.CreateFeatureVersion(&boostdomain.FeatureVersion{
			FeatureID: feature.ID, Version: feature.Version, DefinitionJSON: feature.DefinitionJSON, AdminID: input.AdminID,
		}); err != nil {
			return err
		}
		out.Feature = feature
		out.Changed = true
		return nil
	})
	if err != nil {
		return nil, err
	}
	return out, nil
}

// SetFeatureStatus 上架 / 下架 / 回到草稿。下架已上线游戏的最后一个上架功能会被拒绝。
func (s *CatalogService) SetFeatureStatus(id uint, status string) (*boostdomain.Feature, error) {
	switch status {
	case boostdomain.FeatureStatusDraft, boostdomain.FeatureStatusActive, boostdomain.FeatureStatusDisabled:
	default:
		return nil, invalid(boostcontract.ErrInvalid, "功能状态只能是 draft / active / disabled")
	}
	var saved *boostdomain.Feature
	err := s.repo.WithinTransaction(func(repo boostcontract.CatalogRepository) error {
		feature, err := repo.GetFeature(id)
		if err != nil {
			return err
		}
		if feature == nil {
			return boostcontract.ErrNotFound
		}
		if status != boostdomain.FeatureStatusActive && feature.Status == boostdomain.FeatureStatusActive {
			if err := ensureGameStaysReady(repo, feature.GameID, func() (int64, error) {
				return repo.CountFeatures(feature.GameID, boostdomain.FeatureStatusActive)
			}, "这是已上线游戏唯一上架的功能，请先隐藏游戏或上架其他功能"); err != nil {
				return err
			}
		}
		feature.Status = status
		if err := repo.SaveFeature(feature); err != nil {
			return err
		}
		saved = feature
		return nil
	})
	return saved, err
}

// MoveFeature 把功能在游戏内上移（delta=-1）或下移（delta=1），并把排序整理为 0..n-1。
func (s *CatalogService) MoveFeature(id uint, delta int) ([]boostdomain.Feature, error) {
	if delta != -1 && delta != 1 {
		return nil, invalid(boostcontract.ErrInvalid, "只能上移或下移一位")
	}
	var list []boostdomain.Feature
	err := s.repo.WithinTransaction(func(repo boostcontract.CatalogRepository) error {
		feature, err := repo.GetFeature(id)
		if err != nil {
			return err
		}
		if feature == nil {
			return boostcontract.ErrNotFound
		}
		list, err = repo.ListFeatures(feature.GameID)
		if err != nil {
			return err
		}
		i := -1
		for k := range list {
			if list[k].ID == feature.ID {
				i = k
			}
		}
		j := i + delta
		if i < 0 || j < 0 || j >= len(list) {
			return nil
		}
		list[i], list[j] = list[j], list[i]
		for k := range list {
			if list[k].Sort == k {
				continue
			}
			list[k].Sort = k
			if err := repo.SaveFeature(&list[k]); err != nil {
				return err
			}
		}
		return nil
	})
	return list, err
}

// ListFeatureVersions 返回功能的历史版本（新的在前）。
func (s *CatalogService) ListFeatureVersions(featureID uint) ([]boostdomain.FeatureVersion, error) {
	if _, err := s.GetFeature(featureID); err != nil {
		return nil, err
	}
	return s.repo.ListFeatureVersions(featureID)
}

// GetFeatureVersion 返回某个历史版本，供编辑器载入。
func (s *CatalogService) GetFeatureVersion(featureID uint, version int) (*boostdomain.FeatureVersion, error) {
	v, err := s.repo.GetFeatureVersion(featureID, version)
	if err != nil {
		return nil, err
	}
	if v == nil {
		return nil, boostcontract.ErrNotFound
	}
	return v, nil
}

// ---------------------------------------------------------------------------

func parseAndValidate(definition []byte) (featuredef.Value, featuredef.Result) {
	def, err := featuredef.ParseValue(definition)
	if err != nil {
		return featuredef.Value{}, featuredef.ValidateJSON(definition)
	}
	return def, featuredef.Validate(def)
}

func definitionError(result featuredef.Result) error {
	first := result.Errors[0]
	msg := first.Msg
	if first.Path != "" {
		msg = first.Path + "：" + first.Msg
	}
	return &ValidationError{
		Kind:   boostcontract.ErrDefinition,
		Msg:    fmt.Sprintf("功能定义有 %d 处错误，第一处：%s", len(result.Errors), msg),
		Issues: result.Errors,
	}
}

// AsValidationError 取出 ValidationError（如果有）。
func AsValidationError(err error) (*ValidationError, bool) {
	var ve *ValidationError
	ok := errors.As(err, &ve)
	return ve, ok
}

func firstNonEmpty(values ...string) string {
	for _, v := range values {
		if v != "" {
			return v
		}
	}
	return ""
}

func truncateRunes(s string, n int) string {
	if utf8.RuneCountInString(s) <= n {
		return s
	}
	return string([]rune(s)[:n])
}
