package must

import "fmt"

// LogConfig 日志配置
type LogConfig struct {
	Level    string `yaml:"level"`
	Format   string `yaml:"format"`
	Output   string `yaml:"output"`
	FilePath string `yaml:"file_path"`
}

func (c *LogConfig) ConfigCheck() error {
	if c.Level == "" {
		return fmt.Errorf("Log level cannot be empty")
	}
	if c.Format == "" {
		return fmt.Errorf("Log format cannot be empty")
	}
	// Output/FilePath 允许为空 (缺省输出到 stdout)
	return nil
}
