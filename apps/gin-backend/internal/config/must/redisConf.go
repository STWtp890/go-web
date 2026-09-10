package must

import "fmt"

// RedisConfig Redis 配置
type RedisConfig struct {
	Host     string `yaml:"host"`
	Port     int    `yaml:"port"`
	Password string `yaml:"password"`
	DB       int    `yaml:"db"`
}

func (c *RedisConfig) ConfigCheck() error {
	if c.Host == "" {
		return fmt.Errorf("Redis host cannot be empty")
	}
	if c.Port == 0 {
		return fmt.Errorf("Redis port cannot be zero")
	}
	// 允许空密码与 db=0 (本地开发/无密码环境)
	return nil
}
