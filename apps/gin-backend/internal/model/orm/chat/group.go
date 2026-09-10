// Package chat 定义聊天持久化模型 (群组 / 消息)
package chat

import "gin-backend/internal/model/orm"

// Group 群聊群组
// :Field
// - GroupID: 对外群标识 (UUIDv7 风格, 防遍历)
// - OwnerID: 群主标识 (JWT sub)
type Group struct {
	ID      uint   `json:"id" gorm:"primaryKey;autoIncrement"`
	GroupID string `json:"group_id" gorm:"size:64;uniqueIndex;not null"`
	Name    string `json:"name" gorm:"size:128;not null"`
	OwnerID string `json:"owner_id" gorm:"size:64;index;not null"`
	orm.TimeFiled
}

// TableName 指定表名
func (*Group) TableName() string {
	return "chat_groups"
}

// GroupMember 群成员关系 (复合唯一索引防重复加入)
// :Field
// - GroupID: 群标识
// - MemberID: 成员标识 (JWT sub)
type GroupMember struct {
	ID       uint   `json:"id" gorm:"primaryKey;autoIncrement"`
	GroupID  string `json:"group_id" gorm:"size:64;index:idx_group_member,unique,priority:1;not null"`
	MemberID string `json:"member_id" gorm:"size:64;index:idx_group_member,unique,priority:2;not null"`
	orm.TimeFiled
}

// TableName 指定表名
func (*GroupMember) TableName() string {
	return "chat_group_members"
}
