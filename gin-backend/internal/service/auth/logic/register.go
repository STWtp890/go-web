package logic

import (
	"context"
	"errors"

	"gin-backend/internal/common/base/constant"
	"gin-backend/internal/common/connection"
	postgresqlconn "gin-backend/internal/common/connection/postgresql"
	authmodel "gin-backend/internal/orm/auth"
	"gin-backend/internal/service/auth/types/requests"

	"golang.org/x/crypto/bcrypt"
)

// RegisterLogic 用户注册
func RegisterLogic(ctx context.Context, req *requests.RegisterRequest) (*authmodel.User, error) {
	// 1. 检查邮箱用户是否已注册
	if checkRegisterUserExists(ctx, req.Email) {
		return nil, errors.New("用户已存在")
	}

	// 2. 生成密码哈希
	hashedPassword, err := generateRegisterPasswordHash(req.Password)
	if err != nil {
		return nil, err
	}

	// 3. 创建新用户
	user, err := createRegisterUser(ctx, req.Email, hashedPassword, req.Nickname)
	if err != nil {
		return nil, err
	}

	return user, nil
}


func checkRegisterUserExists(ctx context.Context, email string) bool {
	conn, err := postgresqlconn.PostgreSQLManager.Get(connection.ServiceAuth)
	if err != nil {
		return false
	}
	db, err := conn.GetConn()
	if err != nil {
		return false
	}

	result := db.WithContext(ctx).Where("email = ?", email).First(&authmodel.User{})
	if result.Error != nil {
		return false
	}
	return true
}

func generateRegisterPasswordHash(password string) (string, error) {
	hashedPassword, err := bcrypt.GenerateFromPassword([]byte(password), constant.BcryptCost)
	if err != nil {
		return "", err
	}
	return string(hashedPassword), nil
}

func createRegisterUser(ctx context.Context, email, hashedPassword, nickname string) (*authmodel.User, error) {
	conn, err := postgresqlconn.PostgreSQLManager.Get(connection.ServiceAuth)
	if err != nil {
		return nil, err
	}
	db, err := conn.GetConn()
	if err != nil {
		return nil, err
	}

	user := &authmodel.User{
		Email:    email,
		Password: hashedPassword,
		Nickname: nickname,
	}
	result := db.WithContext(ctx).Create(user)
	if result.Error != nil {
		return nil, result.Error
	}
	return user, nil
}
