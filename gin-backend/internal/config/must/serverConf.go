package must

import (
	"fmt"
	"time"
)

// ServerConfig 服务器配置
type ServerConfig struct {
	Port         int           `yaml:"port"`
	Mode         string        `yaml:"mode"` // GinServerMode: debug, release, test
	ReadTimeout  time.Duration `yaml:"read_timeout"`
	WriteTimeout time.Duration `yaml:"write_timeout"`
}

// ConfigCheck 检查 Server 配置的有效性
func (c *ServerConfig) ConfigCheck() error {
	if c.Port == 0 {
		return fmt.Errorf("Server port cannot be zero")
	}
	if c.Mode == "" {
		return fmt.Errorf("Server mode cannot be empty")
	}
	if c.ReadTimeout == 0 {
		return fmt.Errorf("Server read_timeout cannot be zero")
	}
	if c.WriteTimeout == 0 {
		return fmt.Errorf("Server write_timeout cannot be zero")
	}
	return nil
}