package must

import (
	"fmt"
	"strings"
	"time"
)

// MixinSearchSecurityConfig 定义 gin-backend 调用 mixin-search 时使用的调用方身份与能力凭证。
//
// mixin-search 只接受携带有效 capability 的调用方，并按角色区分索引写入与检索。
// 因此只要启用文档索引投递、影子查询、检索评测或索引管理命令，本节都必须有效。
// 边界密钥本身放在独立文件中（capability_key_path），不写入本配置。
type MixinSearchSecurityConfig struct {
	// CapabilityKeyPath 是边界密钥文件路径，相对进程工作目录。
	CapabilityKeyPath string `yaml:"capability_key_path"`
	// Issuer 是签发方标识，写入 token 的 issuer 声明。
	Issuer string `yaml:"issuer"`
	// Audience 是被调服务标识，必须与 mixin-search 接受的值一致。
	Audience string `yaml:"audience"`
	// TokenTTL 是 capability 有效期，必须覆盖最长 RPC 超时。
	TokenTTL time.Duration `yaml:"token_ttl"`
	// IndexCallerID 是索引写入调用方标识（Worker 与对账）。
	IndexCallerID string `yaml:"index_caller_id"`
	// SearchCallerID 是服务内影子查询调用方标识。
	SearchCallerID string `yaml:"search_caller_id"`
}

func (c *MixinSearchSecurityConfig) ConfigCheck() error {
	if strings.TrimSpace(c.CapabilityKeyPath) == "" {
		return fmt.Errorf("mixin_search_security.capability_key_path cannot be empty")
	}
	if strings.TrimSpace(c.Issuer) == "" {
		return fmt.Errorf("mixin_search_security.issuer cannot be empty")
	}
	if strings.TrimSpace(c.Audience) == "" {
		return fmt.Errorf("mixin_search_security.audience cannot be empty")
	}
	if c.TokenTTL <= 0 {
		return fmt.Errorf("mixin_search_security.token_ttl must be greater than zero")
	}
	if strings.TrimSpace(c.IndexCallerID) == "" {
		return fmt.Errorf("mixin_search_security.index_caller_id cannot be empty")
	}
	if strings.TrimSpace(c.SearchCallerID) == "" {
		return fmt.Errorf("mixin_search_security.search_caller_id cannot be empty")
	}
	return nil
}
