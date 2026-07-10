// Code scaffolded by goctl. Safe to edit.
// goctl 1.10.1

package config

import (
	"fmt"

	"github.com/zeromicro/go-zero/core/logx"
	"github.com/zeromicro/go-zero/core/stores/cache"
	"github.com/zeromicro/go-zero/core/stores/redis"
	"github.com/zeromicro/go-zero/core/stores/sqlx"
	"github.com/zeromicro/go-zero/rest"
)

type Config struct {
	rest.RestConf
	Auth, Refresh struct {
		AccessSecret string
		AccessExpire int64
	}
	RedisConf redis.RedisConf
	SqlxConf  sqlx.SqlConf
	CacheConf cache.CacheConf
	LogConf   logx.LogConf
}

func (c *Config) Validate() error {
	if c.Auth.AccessSecret == "" {
		return fmt.Errorf("Auth.AccessSecret is empty")
	}
	if c.Auth.AccessExpire <= 0 {
		return fmt.Errorf("Auth.AccessExpire must be greater than 0")
	}
	if c.Refresh.AccessSecret == "" {
		return fmt.Errorf("Refresh.AccessSecret is empty")
	}
	if c.Refresh.AccessExpire <= 0 {
		return fmt.Errorf("Refresh.AccessExpire must be greater than 0")
	}
	return nil
}