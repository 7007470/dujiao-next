package domain

import "time"

// Game 代练游戏，由后台添加。Code 创建后不可修改；
// 上线（active）前至少要有一个启用的区服和一个已上架的功能。
type Game struct {
	ID        uint      `gorm:"primarykey" json:"id"`
	Code      string    `gorm:"type:varchar(32);not null;uniqueIndex" json:"code"`
	Name      string    `gorm:"type:varchar(64);not null" json:"name"`
	ShortName string    `gorm:"type:varchar(16)" json:"short_name"`
	Color     string    `gorm:"type:varchar(16)" json:"color"`
	Sort      int       `gorm:"not null;index" json:"sort"`
	Status    string    `gorm:"type:varchar(16);not null;default:'draft';index" json:"status"`
	Version   int       `gorm:"not null;default:1" json:"version"`
	CreatedAt time.Time `json:"created_at"`
	UpdatedAt time.Time `json:"updated_at"`
}

func (Game) TableName() string { return "boost_games" }

// GameServer 游戏区服。同一游戏内 Code 唯一。
type GameServer struct {
	ID        uint      `gorm:"primarykey" json:"id"`
	GameID    uint      `gorm:"not null;uniqueIndex:idx_boost_game_servers_game_code,priority:1" json:"game_id"`
	Code      string    `gorm:"type:varchar(32);not null;uniqueIndex:idx_boost_game_servers_game_code,priority:2" json:"code"`
	Name      string    `gorm:"type:varchar(64);not null" json:"name"`
	Platform  string    `gorm:"type:varchar(16);not null;default:'all'" json:"platform"`
	Sort      int       `gorm:"not null" json:"sort"`
	Status    string    `gorm:"type:varchar(16);not null;default:'active';index" json:"status"`
	CreatedAt time.Time `json:"created_at"`
	UpdatedAt time.Time `json:"updated_at"`
}

func (GameServer) TableName() string { return "boost_game_servers" }
