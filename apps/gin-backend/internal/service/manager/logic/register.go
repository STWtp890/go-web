// 提交管理员注册申请
package logic

import (
	"context"
	"strings"

	bs "gin-backend/internal/common/base/constant"
	managermodel "gin-backend/internal/model/orm/manager"
	tc "gin-backend/internal/service/manager/types/constant"
	"gin-backend/internal/service/manager/types/requests"
	"gin-backend/internal/service/manager/utils"

	"golang.org/x/crypto/bcrypt"
)

// RegisterLogic 提交管理员注册申请 (审批前仅记录 pending)
// :Param
// - `ctx` 上下文
// - `req` 注册申请请求
// :Return
// - `*managermodel.RegistrationRequest` 创建的申请单
// - `error` 用户名占用 / 写入失败
func RegisterLogic(ctx context.Context, req *requests.RegisterRequest) (*managermodel.RegistrationRequest, error) {
	db, err := utils.ManagerDB(ctx)
	if err != nil {
		return nil, err
	}

	// 1. 检查用户名占用: 管理员表 或 未拒绝的申请单
	var count int64
	if err := db.Model(&managermodel.Manager{}).
		Where("username = ?", req.Username).
		Count(&count).Error; err != nil {
		return nil, err
	}
	if count > 0 {
		return nil, tc.ErrUsernameTaken
	}
	if err := db.Model(&managermodel.RegistrationRequest{}).
		Where("username = ? AND status IN ?", req.Username,
			[]string{managermodel.RequestPending, managermodel.RequestApproved}).
		Count(&count).Error; err != nil {
		return nil, err
	}
	if count > 0 {
		return nil, tc.ErrUsernameTaken
	}

	// 2. bcrypt 哈希
	hash, err := bcrypt.GenerateFromPassword([]byte(req.Password), bs.BcryptCost)
	if err != nil {
		return nil, err
	}

	// 3. 创建申请单 (pending)
	r := &managermodel.RegistrationRequest{
		Username:     req.Username,
		PasswordHash: string(hash),
		Email:        req.Email,
		Reason:       req.Reason,
		Status:       managermodel.RequestPending,
	}
	if err := db.Create(r).Error; err != nil {
		if strings.Contains(err.Error(), "uni_manager_request_active_username") {
			return nil, tc.ErrUsernameTaken
		}
		return nil, err
	}
	return r, nil
}
