package auth

import "gin-backend/internal/model/orm"

// User 用户模型
type User struct {
	ID       uint   `json:"id" gorm:"primaryKey;autoIncrement;"`
	Email    string `json:"email" gorm:"size:128;unique;not null"`
	Password string `json:"-" gorm:"size:128;not null"`
	Nickname string `json:"nickname" gorm:"size:64"`
	Avatar   string `json:"avatar" gorm:"size:255;default:''"`
	Banned   bool   `json:"banned" gorm:"default:false"`
	orm.TimeFiled
}

// TableName 指定表名
func (*User) TableName() string {
	return "users"
}
