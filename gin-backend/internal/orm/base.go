package orm

import "gorm.io/gorm"

// TimeFiled 时间字段, 用于简化并统一模型中时间字段的定义
// DeletedAt 使用 gorm.DeletedAt 启用 GORM 软删除:
//   - Delete 自动转为 UPDATE deleted_at (软删), 保留历史可审计
//   - 查询自动附加 deleted_at IS NULL
//   - 需物理删除时显式使用 Unscoped()
type TimeFiled struct {
	CreatedAt int64          `json:"created_at" gorm:"autoCreateTime:true"`
	UpdatedAt int64          `json:"updated_at" gorm:"autoUpdateTime:true"`
	DeletedAt gorm.DeletedAt `json:"deleted_at"`
}
