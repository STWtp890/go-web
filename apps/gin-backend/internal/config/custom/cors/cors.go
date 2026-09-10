package cors

import (
	"fmt"
	"time"
)

// CORSConfig 跨域配置
type CORSConfig struct {
	AllowOrigins     []string      `yaml:"allow_origins"`
	AllowMethods     []string      `yaml:"allow_methods"`
	AllowHeaders     []string      `yaml:"allow_headers"`
	ExposeHeaders    []string      `yaml:"expose_headers"`
	AllowCredentials bool          `yaml:"allow_credentials"`
	MaxAge           time.Duration `yaml:"max_age"`
}

func (c *CORSConfig) ConfigCheck() error {
	if c.AllowOrigins == nil {
		return fmt.Errorf("CORS allow_origins cannot be empty")
	}
	if c.AllowMethods == nil {
		return fmt.Errorf("CORS allow_methods cannot be empty")
	}
	if c.AllowHeaders == nil {
		return fmt.Errorf("CORS allow_headers cannot be empty")
	}
	if c.ExposeHeaders == nil {
		return fmt.Errorf("CORS expose_headers cannot be empty")
	}
	if c.MaxAge == 0 {
		return fmt.Errorf("CORS max_age cannot be zero")
	}
	return nil
}
