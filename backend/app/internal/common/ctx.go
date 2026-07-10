package common

import (
	"context"
	"encoding/json"
	"fmt"
)

// GetUserIdFromCtx 从 context 中获取 userId（go-zero JWT 中间件注入）
// go-zero rest.WithJwt 将 jwt.MapClaims 自定义字段注入 context，
// 数字类型为 json.Number（部分版本为 float64），需兼容处理。
func GetUserIdFromCtx(ctx context.Context) (string, error) {
	v := ctx.Value("userId")
	switch v.(type) {
		case string:
			if v.(string) == "" {
				return "", fmt.Errorf("userId not found in context")
			}
			return v.(string), nil
		case nil:
			return "", fmt.Errorf("userId not found in context")
		default:
			return "", fmt.Errorf("userId is not a string, got %T", v)
	}
}

// GetRoleIdFromCtx 从 context 中获取 roleId（go-zero JWT 中间件注入）
// go-zero rest.WithJwt 将 jwt.MapClaims 自定义字段注入 context，
// 数字类型为 json.Number（部分版本为 float64），需兼容处理。
func GetRoleIdFromCtx(ctx context.Context) (int64, error) {
	v := ctx.Value("roleId")
	if v == nil {
		return 0, fmt.Errorf("roleId not found in context")
	}

	switch val := v.(type) {
	case int64:
		return val, nil
	case json.Number:
		n, err := val.Int64()
		if err != nil {
			return 0, fmt.Errorf("roleId is not a valid int64: %v", err)
		}
		return n, nil
	case float64:
		return int64(val), nil
	default:
		return 0, fmt.Errorf("roleId is not a numeric type, got %T", v)
	}
}
