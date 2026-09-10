package config

import (
	"gin-backend/internal/config/custom"
	"gin-backend/internal/config/must"
	"os"

	"gopkg.in/yaml.v3"
)

// Config 配置结构体
// :Field
// - ServerConfig: 服务配置
// - LogConfig: 日志配置
// - PostgresConfig: PostgreSQL 数据库配置 (auth/markdown/chat 共用)
// - RedisConfig: Redis 配置
// - TLSConfig: TLS 配置
// - CustomConfig: 自定义配置
type Config struct {
	ServerConfig   must.ServerConfig   `yaml:"server"`
	LogConfig      must.LogConfig      `yaml:"log"`
	PostgresConfig must.PostgresConfig `yaml:"postgres"`
	RedisConfig    must.RedisConfig    `yaml:"redis"`
	TLSConfig      must.TLSConfig      `yaml:"tls"`

	CustomConfig custom.CustomConfig `yaml:"custom"`
}

var globalConfig *Config

// NewConfig 只会创建一个新的配置实例, 你需要自行保证并确认 Config 的有效性
func NewConfig() *Config {
	c := &Config{}
	return c
}

// GetConfig 获取全局配置实例
func GetConfig() *Config {
	return globalConfig
}

// CustomConfig 获取自定义配置实例
func CustomConfig() *custom.CustomConfig {
	return &globalConfig.CustomConfig
}

// Load 从 YAML 文件加载配置
func Load(path string) (*Config, error) {
	if path == "" {
		return nil, os.ErrNotExist
	}

	// 1. 读取 YAML 文件
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}

	// 2. Unmarshal YAML 数据到 Config 结构体
	c := NewConfig()
	if err := yaml.Unmarshal(data, c); err != nil {
		return nil, err
	}

	// 3. 检查配置有效性
	err = ConfigCheck(c)
	if err != nil {
		return nil, err
	}

	globalConfig = c
	return c, nil
}

// ConfigCheck 检查配置是否有效
func ConfigCheck(c *Config) error {
	if err := c.ServerConfig.ConfigCheck(); err != nil {
		return err
	}
	if err := c.LogConfig.ConfigCheck(); err != nil {
		return err
	}
	if err := c.PostgresConfig.ConfigCheck(); err != nil {
		return err
	}
	if err := c.RedisConfig.ConfigCheck(); err != nil {
		return err
	}
	if err := c.CustomConfig.ConfigCheck(); err != nil {
		return err
	}
	return nil
}
