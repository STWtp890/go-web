package must

import (
	"fmt"
	"strings"
	"time"
)

// IndexDeliveryConfig 定义文档影子索引 Worker 的运行参数。
// enabled 只控制消费，文档事务始终保留 Outbox 事件。
type IndexDeliveryConfig struct {
	Enabled        bool          `yaml:"enabled"`
	GRPCAddress    string        `yaml:"grpc_address"`
	BatchSize      int           `yaml:"batch_size"`
	Concurrency    int           `yaml:"concurrency"`
	PollInterval   time.Duration `yaml:"poll_interval"`
	LeaseDuration  time.Duration `yaml:"lease_duration"`
	IndexTimeout   time.Duration `yaml:"index_timeout"`
	ControlTimeout time.Duration `yaml:"control_timeout"`
	BaseBackoff    time.Duration `yaml:"base_backoff"`
	MaxBackoff     time.Duration `yaml:"max_backoff"`
	MaxSendBytes   int           `yaml:"max_send_bytes"`
	HealthTimeout  time.Duration `yaml:"health_timeout"`
}

func (c *IndexDeliveryConfig) ConfigCheck() error {
	if c.Enabled && strings.TrimSpace(c.GRPCAddress) == "" {
		return fmt.Errorf("Document index delivery grpc_address cannot be empty when enabled")
	}
	if c.BatchSize <= 0 || c.Concurrency <= 0 {
		return fmt.Errorf("Document index delivery batch_size and concurrency must be greater than zero")
	}
	if c.PollInterval <= 0 || c.LeaseDuration <= 0 || c.IndexTimeout <= 0 || c.ControlTimeout <= 0 {
		return fmt.Errorf("Document index delivery intervals and timeouts must be greater than zero")
	}
	if c.LeaseDuration <= c.IndexTimeout || c.LeaseDuration <= c.ControlTimeout {
		return fmt.Errorf("Document index delivery lease_duration must exceed RPC timeouts")
	}
	if c.BaseBackoff <= 0 || c.MaxBackoff < c.BaseBackoff {
		return fmt.Errorf("Document index delivery backoff range is invalid")
	}
	if c.MaxSendBytes <= 0 || c.HealthTimeout <= 0 {
		return fmt.Errorf("Document index delivery max_send_bytes and health_timeout must be greater than zero")
	}
	return nil
}
