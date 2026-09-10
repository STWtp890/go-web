package must

import "fmt"

// PostgresConfig PostgreSQL 数据库配置 (auth/markdown/chat 共用, 对应 yaml 段 postgres)
type PostgresConfig struct {
	Host         string `yaml:"host"`
	Port         int    `yaml:"port"`
	User         string `yaml:"user"`
	Password     string `yaml:"password"`
	DBName       string `yaml:"dbname"`
	MaxIdleConns int    `yaml:"max_idle_conns"`
	MaxOpenConns int    `yaml:"max_open_conns"`
}

func (c *PostgresConfig) ConfigCheck() error {
	if c.Host == "" {
		return fmt.Errorf("Postgres host cannot be empty")
	}
	if c.Port == 0 {
		return fmt.Errorf("Postgres port cannot be zero")
	}
	if c.User == "" {
		return fmt.Errorf("Postgres user cannot be empty")
	}
	// 允许空密码 (本地开发/无密码环境)
	if c.DBName == "" {
		return fmt.Errorf("Postgres dbname cannot be empty")
	}
	if c.MaxIdleConns == 0 {
		return fmt.Errorf("Postgres max_idle_conns cannot be zero")
	}
	if c.MaxOpenConns == 0 {
		return fmt.Errorf("Postgres max_open_conns cannot be zero")
	}
	return nil
}
