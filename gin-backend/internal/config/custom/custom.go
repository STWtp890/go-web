package custom

import (
	"gin-backend/internal/config/custom/cors"
	"gin-backend/internal/config/custom/jwt"
	"gin-backend/internal/config/custom/upload"
)

// CustomConfig 自定义配置
type CustomConfig struct {
	JWT    jwt.JWTConfig       `yaml:"jwt"`
	CORS   cors.CORSConfig     `yaml:"cors"`
	Upload upload.UploadConfig `yaml:"upload"`
}

// NewCustomConfig 创建自定义配置实例
// :Return
// - *CustomConfig 自定义配置实例
func NewCustomConfig() *CustomConfig {
	cc := &CustomConfig{
		JWT:    jwt.JWTConfig{},
		CORS:   cors.CORSConfig{},
		Upload: upload.UploadConfig{},
	}
	cc.ConfigCheck()
	return cc
}

// ConfigCheck 检查自定义配置是否有效
// :Return
// - error 配置检查错误信息, 如果配置有效则返回 nil
func (c *CustomConfig) ConfigCheck() error {
	err := c.JWT.ConfigCheck()
	if err != nil {
		return err
	}
	err = c.CORS.ConfigCheck()
	if err != nil {
		return err
	}
	err = c.Upload.ConfigCheck()
	if err != nil {
		return err
	}
	return nil
}
