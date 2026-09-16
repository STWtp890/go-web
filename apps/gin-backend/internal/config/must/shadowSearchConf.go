package must

import (
	"fmt"
	"strings"
	"time"
)

// ShadowSearchConfig 定义 HTTP 正式搜索完成后的异步影子查询参数。
type ShadowSearchConfig struct {
	Enabled       bool          `yaml:"enabled"`
	GRPCAddress   string        `yaml:"grpc_address"`
	QueueSize     int           `yaml:"queue_size"`
	Concurrency   int           `yaml:"concurrency"`
	Timeout       time.Duration `yaml:"timeout"`
	RecordTimeout time.Duration `yaml:"record_timeout"`
	TopK          int           `yaml:"top_k"`
}

func (c *ShadowSearchConfig) ConfigCheck() error {
	if c.Enabled && strings.TrimSpace(c.GRPCAddress) == "" {
		return fmt.Errorf("Document search shadow grpc_address cannot be empty when enabled")
	}
	if c.QueueSize <= 0 || c.Concurrency <= 0 {
		return fmt.Errorf("Document search shadow queue_size and concurrency must be greater than zero")
	}
	if c.Timeout <= 0 || c.RecordTimeout <= 0 {
		return fmt.Errorf("Document search shadow timeouts must be greater than zero")
	}
	if c.TopK <= 0 || c.TopK > 100 {
		return fmt.Errorf("Document search shadow top_k must be between 1 and 100")
	}
	return nil
}
