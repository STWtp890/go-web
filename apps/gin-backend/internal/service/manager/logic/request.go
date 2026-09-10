// 审批流: 申请列表 / 通过 / 拒绝
package logic

import (
	"context"
	"errors"
	"time"

	managermodel "gin-backend/internal/model/orm/manager"
	"gin-backend/internal/model/store"
	tc "gin-backend/internal/service/manager/types/constant"
	"gin-backend/internal/service/manager/utils"

	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

// ListRequestsLogic 审批列表 (默认 pending, 可按状态过滤), 先申请先审批
// :Return
// - `[]*managermodel.RegistrationRequest` 申请单列表 (不含 PasswordHash 敏感字段由 json:"-" 屏蔽)
// - `int64` 总数 (分页用)
// - `error` 查询失败
func ListRequestsLogic(ctx context.Context, status string, page, pageSize int) ([]*managermodel.RegistrationRequest, int64, error) {
	db, err := utils.ManagerDB(ctx)
	if err != nil {
		return nil, 0, err
	}
	if status == "" {
		status = managermodel.RequestPending
	}
	if page <= 0 {
		page = 1
	}
	if pageSize <= 0 || pageSize > 100 {
		pageSize = 10
	}

	var total int64
	if err := db.Model(&managermodel.RegistrationRequest{}).
		Where("status = ?", status).
		Count(&total).Error; err != nil {
		return nil, 0, err
	}

	var list []*managermodel.RegistrationRequest
	if err := db.Where("status = ?", status).
		Order("id ASC").
		Offset((page - 1) * pageSize).
		Limit(pageSize).
		Find(&list).Error; err != nil {
		return nil, 0, err
	}
	return list, total, nil
}

// ApproveLogic 通过申请: 事务内创建管理员账号 + 更新申请单 (原子)
// 流程: 锁行校验 pending → 用户名未被占用 → 创建 managers → 申请单置 approved
// :Param
// - `requestID` 申请单 ID
// - `reviewerID` 审批人 (managers.id)
// - `comment` 审批意见 (可选)
// :Return
// - `*managermodel.Manager` 新创建的管理员账号
// - `error` ErrRequestNotFound / ErrRequestReviewed / ErrUsernameTaken / 写库失败
func ApproveLogic(ctx context.Context, requestID, reviewerID uint, comment string) (*managermodel.Manager, error) {
	db, err := utils.ManagerDB(ctx)
	if err != nil {
		return nil, err
	}

	var m *managermodel.Manager
	err = db.Transaction(func(tx *gorm.DB) error {
		// 1. 锁行查询申请单
		var r managermodel.RegistrationRequest
		if err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).
			Where("id = ?", requestID).
			First(&r).Error; err != nil {
			if errors.Is(err, gorm.ErrRecordNotFound) {
				return tc.ErrRequestNotFound
			}
			return err
		}
		if r.Status != managermodel.RequestPending {
			return tc.ErrRequestReviewed
		}

		// 2. 校验用户名未被占用 (并发安全: 依赖 managers.username 唯一索引兜底)
		var count int64
		if err := tx.Model(&managermodel.Manager{}).
			Where("username = ?", r.Username).
			Count(&count).Error; err != nil {
			return err
		}
		if count > 0 {
			return tc.ErrUsernameTaken
		}

		// 3. 创建管理员账号 (密码哈希从申请单迁移)
		m = &managermodel.Manager{
			Username: r.Username,
			Password: r.PasswordHash,
			Email:    r.Email,
			Status:   managermodel.ManagerActive,
		}
		if err := tx.Create(m).Error; err != nil {
			return err
		}

		// 4. 更新申请单状态
		now := time.Now()
		return tx.Model(&managermodel.RegistrationRequest{}).
			Where("id = ?", r.ID).
			Select("status", "reviewer_id", "review_comment", "reviewed_at").
			Updates(&managermodel.RegistrationRequest{
				Status:        managermodel.RequestApproved,
				ReviewerID:    reviewerID,
				ReviewComment: comment,
				ReviewedAt:    &now,
			}).Error
	})
	if err == nil {
		// 5. 失效管理员缓存 (新账号下次登录/刷新回源新数据; Cache-Aside 一致性)
		_ = store.Manager.Evict(ctx, m.ID, m.Username)
	}
	return m, err
}

// RejectLogic 拒绝申请 (仅 pending 可拒绝)
func RejectLogic(ctx context.Context, requestID, reviewerID uint, comment string) error {
	db, err := utils.ManagerDB(ctx)
	if err != nil {
		return err
	}

	return db.Transaction(func(tx *gorm.DB) error {
		var r managermodel.RegistrationRequest
		if err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).Where("id = ?", requestID).First(&r).Error; err != nil {
			if errors.Is(err, gorm.ErrRecordNotFound) {
				return tc.ErrRequestNotFound
			}
			return err
		}
		if r.Status != managermodel.RequestPending {
			return tc.ErrRequestReviewed
		}
		now := time.Now()
		result := tx.Model(&managermodel.RegistrationRequest{}).
			Where("id = ? AND status = ?", r.ID, managermodel.RequestPending).
			Select("status", "reviewer_id", "review_comment", "reviewed_at").
			Updates(&managermodel.RegistrationRequest{Status: managermodel.RequestRejected, ReviewerID: reviewerID, ReviewComment: comment, ReviewedAt: &now})
		if result.Error != nil {
			return result.Error
		}
		if result.RowsAffected != 1 {
			return tc.ErrRequestReviewed
		}
		return nil
	})
}
