package must

import "fmt"

// TLSConfig TLS 配置
type TLSConfig struct {
	CertFile string `yaml:"cert_file"` // 传入 os.ReadFile
	KeyFile  string `yaml:"key_file"`  // 传入 os.ReadFile
}

// ConfigCheck 检查 TLS 配置的有效性
func (c *TLSConfig) ConfigCheck() error {
	if c.CertFile == "" {
		return fmt.Errorf("TLS cert_file cannot be empty")
	}
	if c.KeyFile == "" {
		return fmt.Errorf("TLS key_file cannot be empty")
	}
	return nil
}
