// 群组持久化: GroupStore 接口 + gorm 实现
package store

import (
	"context"

	ormchat "gin-backend/internal/model/orm/chat"

	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

// GroupStore 群组持久化接口 (DB 为成员关系真相源, 内存 GroupMap 仅缓存投递目标)
// :Method
// - `CreateGroup`: 创建群, owner 自动成为首个成员 (事务)
// - `Join`: 加入群 (复合唯一索引 + OnConflict 幂等)
// - `Leave`: 离开群 (硬删, 防软删行与唯一索引冲突)
// - `Members`: 群成员列表
// - `GroupsOf`: 用户加入的群 ID 列表 (上线预加载用)
// - `IsMember`: 是否群成员
// - `GroupExists`: 群是否存在 (加入前校验)
// - `ListGroups`: 全部群列表 (应用启动初始化内存模型用)
type GroupStore interface {
	CreateGroup(ctx context.Context, groupID, name, ownerID string) error
	Join(ctx context.Context, groupID, memberID string) error
	Leave(ctx context.Context, groupID, memberID string) error
	Members(ctx context.Context, groupID string) ([]string, error)
	GroupsOf(ctx context.Context, memberID string) ([]string, error)
	IsMember(ctx context.Context, groupID, memberID string) (bool, error)
	GroupExists(ctx context.Context, groupID string) (bool, error)
	ListGroups(ctx context.Context) ([]ormchat.Group, error)
}

// GormGroupStore GroupStore 的 gorm 实现 (PostgreSQL, DB 连接懒加载见 gormStore)
type GormGroupStore struct {
	gormStore
}

// NewGormGroupStore 创建 gorm 群组存储 (无参, DB 连接懒加载)
func NewGormGroupStore() *GormGroupStore {
	return &GormGroupStore{}
}

// CreateGroup 创建群并让 owner 成为首个成员 (事务保证一致性)
func (s *GormGroupStore) CreateGroup(ctx context.Context, groupID, name, ownerID string) error {
	db, err := s.dbConn()
	if err != nil {
		return err
	}
	return db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		group := ormchat.Group{GroupID: groupID, Name: name, OwnerID: ownerID}
		if err := tx.Create(&group).Error; err != nil {
			return err
		}
		member := ormchat.GroupMember{GroupID: groupID, MemberID: ownerID}
		return tx.Create(&member).Error
	})
}

// Join 加入群 (唯一索引冲突时静默忽略, 幂等)
func (s *GormGroupStore) Join(ctx context.Context, groupID, memberID string) error {
	db, err := s.dbConn()
	if err != nil {
		return err
	}
	return db.WithContext(ctx).
		Clauses(clause.OnConflict{DoNothing: true}).
		Create(&ormchat.GroupMember{GroupID: groupID, MemberID: memberID}).Error
}

// Leave 离开群 (Unscoped 硬删, 避免软删行占用唯一索引导致重新加入失败)
func (s *GormGroupStore) Leave(ctx context.Context, groupID, memberID string) error {
	db, err := s.dbConn()
	if err != nil {
		return err
	}
	return db.WithContext(ctx).
		Unscoped().
		Where("group_id = ? AND member_id = ?", groupID, memberID).
		Delete(&ormchat.GroupMember{}).Error
}

// Members 返回群成员列表
func (s *GormGroupStore) Members(ctx context.Context, groupID string) ([]string, error) {
	db, err := s.dbConn()
	if err != nil {
		return nil, err
	}
	var rows []ormchat.GroupMember
	if err := db.WithContext(ctx).
		Where("group_id = ?", groupID).
		Find(&rows).Error; err != nil {
		return nil, err
	}
	out := make([]string, 0, len(rows))
	for _, r := range rows {
		out = append(out, r.MemberID)
	}
	return out, nil
}

// GroupsOf 返回用户加入的群 ID 列表
func (s *GormGroupStore) GroupsOf(ctx context.Context, memberID string) ([]string, error) {
	db, err := s.dbConn()
	if err != nil {
		return nil, err
	}
	var rows []ormchat.GroupMember
	if err := db.WithContext(ctx).
		Where("member_id = ?", memberID).
		Find(&rows).Error; err != nil {
		return nil, err
	}
	out := make([]string, 0, len(rows))
	for _, r := range rows {
		out = append(out, r.GroupID)
	}
	return out, nil
}

// IsMember 判断是否群成员
func (s *GormGroupStore) IsMember(ctx context.Context, groupID, memberID string) (bool, error) {
	db, err := s.dbConn()
	if err != nil {
		return false, err
	}
	var count int64
	if err := db.WithContext(ctx).
		Model(&ormchat.GroupMember{}).
		Where("group_id = ? AND member_id = ?", groupID, memberID).
		Count(&count).Error; err != nil {
		return false, err
	}
	return count > 0, nil
}

// GroupExists 判断群是否存在 (软删行自动过滤)
func (s *GormGroupStore) GroupExists(ctx context.Context, groupID string) (bool, error) {
	db, err := s.dbConn()
	if err != nil {
		return false, err
	}
	var count int64
	if err := db.WithContext(ctx).
		Model(&ormchat.Group{}).
		Where("group_id = ?", groupID).
		Count(&count).Error; err != nil {
		return false, err
	}
	return count > 0, nil
}

// ListGroups 返回全部群 (应用启动初始化内存模型用, 软删自动过滤)
func (s *GormGroupStore) ListGroups(ctx context.Context) ([]ormchat.Group, error) {
	db, err := s.dbConn()
	if err != nil {
		return nil, err
	}
	var rows []ormchat.Group
	if err := db.WithContext(ctx).Find(&rows).Error; err != nil {
		return nil, err
	}
	return rows, nil
}
