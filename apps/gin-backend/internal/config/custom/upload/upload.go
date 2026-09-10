package upload

import "fmt"

// UploadConfig 上传配置w
type UploadConfig struct {
	MaxSize   int64    `yaml:"max_size"`
	AllowExts []string `yaml:"allow_exts"`
	Path      string   `yaml:"path"`
}

// ConfigCheck 检查上传配置是否有效
func (c *UploadConfig) ConfigCheck() error {
	if c.MaxSize <= 0 {
		return fmt.Errorf("Upload max_size must be greater than 0")
	}
	if c.AllowExts == nil {
		return fmt.Errorf("Upload allow_exts cannot be empty")
	}
	if c.Path == "" {
		return fmt.Errorf("Upload path cannot be empty")
	}
	return nil
}
