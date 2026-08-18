// Package redis 提供 Redis 连接的创建与初始化 (纯工厂, 无全局状态),
// 连接生命周期由业务通过 RedisManager 注册 / 注销维护。
package redis

import (
	"context"
	"fmt"
	"strconv"

	"gin-backend/internal/config/must"

	goredis "github.com/redis/go-redis/v9"
)

// NewClient 创建并初始化 Redis 客户端连接 (创建 + Ping 验证)
// :Param
// - `cfg` Redis 配置
// :Return
// - `*goredis.Client` 已就绪的 Redis 客户端
// - `error` 如果创建或 Ping 失败, 返回错误信息
func NewClient(cfg *must.RedisConfig) (*goredis.Client, error) {
	addr := cfg.Host + ":" + strconv.Itoa(cfg.Port)
	client := goredis.NewClient(&goredis.Options{
		Addr:     addr,
		Password: cfg.Password,
		DB:       cfg.DB,
	})

	if err := client.Ping(context.Background()).Err(); err != nil {
		_ = client.Close()
		return nil, fmt.Errorf("redis: 连接失败 %s: %w", addr, err)
	}
	return client, nil
}
