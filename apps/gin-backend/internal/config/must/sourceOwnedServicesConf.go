package must

import (
	"fmt"
	"strings"
	"time"
)

// SourceOwnedServicesConfig 定义 go-web 调用 ADR-017 拆出的三个服务的接线。
//
// ADR-017 下 go-web 不再直接读写正式文档与空间业务表：它保留 Web 账号与 Web 侧
// 业务入口，通过文档服务接口完成文档操作，并在检索前向文档服务取得已判定的资源
// 范围 capability。该段配置是这条链路的唯一入口，缺失即视为未接线（服务启动时拒绝
// 文档相关路由），而不是退化为直接写表。
type SourceOwnedServicesConfig struct {
	// Enabled 决定是否接线来源专属服务。true 时 endpoint、audience、caller 与
	// capability key 都必须有效；false 时文档路由不可用，服务不会回退到写表。
	Enabled bool `yaml:"enabled"`

	// DocumentServiceAddress 是文档服务 gRPC 地址。
	DocumentServiceAddress string `yaml:"document_service_address"`
	// DocumentSearchAddress 是正式文档检索服务 gRPC 地址。
	DocumentSearchAddress string `yaml:"document_search_address"`

	// DocumentServiceAudience 是文档服务期望的 audience。
	DocumentServiceAudience string `yaml:"document_service_audience"`
	// DocumentSearchAudience 是文档检索服务期望的 audience。
	DocumentSearchAudience string `yaml:"document_search_audience"`
	// DocumentServiceAudienceExpected 由文档服务在 capability 中写入的 audience；
	// 与 DocumentSearchAudience 一致时才使用该 capability。
	CapabilityAudience string `yaml:"capability_audience"`

	// Caller 是呈现给文档服务的服务身份。它必须是已登记且可以声明 web:* 主体的调用方。
	// gin-backend 只以 go-web 身份出现；spacectl 是文档服务侧独立入口（见
	// apps/document-service/cmd/document-service-spacectl），不再由本应用代持。
	Caller string `yaml:"caller"`
	// Actor 是写入审计的运维/操作者标识；留空时使用 Caller。
	Actor string `yaml:"actor"`

	// CapabilityKeyPath 是边界密钥文件路径，与服务身份断言共用同一份密钥。
	CapabilityKeyPath string `yaml:"capability_key_path"`

	// RequestTimeout 是单次跨服务调用的超时。
	RequestTimeout time.Duration `yaml:"request_timeout"`
}

// ConfigCheck 在启用时要求全部字段有效；未启用时不校验，但也不会放行文档路由。
func (c *SourceOwnedServicesConfig) ConfigCheck() error {
	if !c.Enabled {
		return nil
	}
	if strings.TrimSpace(c.DocumentServiceAddress) == "" {
		return fmt.Errorf("source_owned_services.document_service_address cannot be empty when enabled")
	}
	if strings.TrimSpace(c.DocumentSearchAddress) == "" {
		return fmt.Errorf("source_owned_services.document_search_address cannot be empty when enabled")
	}
	if strings.TrimSpace(c.DocumentServiceAudience) == "" {
		return fmt.Errorf("source_owned_services.document_service_audience cannot be empty when enabled")
	}
	if strings.TrimSpace(c.DocumentSearchAudience) == "" {
		return fmt.Errorf("source_owned_services.document_search_audience cannot be empty when enabled")
	}
	audience := strings.TrimSpace(c.CapabilityAudience)
	if audience == "" {
		return fmt.Errorf("source_owned_services.capability_audience cannot be empty when enabled")
	}
	if audience != strings.TrimSpace(c.DocumentSearchAudience) {
		// A capability minted for one audience must never be presented to another
		// service: the mismatch would be caught at runtime as an authentication
		// failure, which is harder to diagnose than a startup error.
		return fmt.Errorf(
			"source_owned_services.capability_audience %q must equal document_search_audience %q",
			audience, c.DocumentSearchAudience,
		)
	}
	switch strings.TrimSpace(c.Caller) {
	case "go-web":
	default:
		return fmt.Errorf(
			"source_owned_services.caller must be go-web for gin-backend, got %q",
			c.Caller,
		)
	}
	if strings.TrimSpace(c.CapabilityKeyPath) == "" {
		return fmt.Errorf("source_owned_services.capability_key_path cannot be empty when enabled")
	}
	if c.RequestTimeout <= 0 {
		return fmt.Errorf("source_owned_services.request_timeout must be greater than zero")
	}
	if strings.TrimSpace(c.Actor) == "" {
		c.Actor = strings.TrimSpace(c.Caller)
	}
	return nil
}
