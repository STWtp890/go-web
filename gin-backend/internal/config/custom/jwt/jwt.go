package jwt

import (
	"crypto/rsa"
	"crypto/x509"
	"encoding/pem"
	"fmt"
	"os"
)

// JWTConfig JWT 配置
// :Attributes
// - `PrivateKeyPath` 非对称加密私钥 PEM 文件路径
// - `PublicKeyPath` 非对称加密公钥 PEM 文件路径
// - `PrivateKey` 解析后的 RSA 私钥（运行时注入，非 YAML 字段）
// - `PublicKey` 解析后的 RSA 公钥（运行时注入，非 YAML 字段）
// - `ExpireHours` JWT 过期时间, 单位为小时
type JWTConfig struct {
	PrivateKeyPath string `yaml:"private_key_path"` // 私钥 PEM 文件路径
	PublicKeyPath  string `yaml:"public_key_path"`  // 公钥 PEM 文件路径
	AccessExpireHours    uint   `yaml:"access_expire_hours"`     // Access Token 过期时间, 单位为小时
	RefreshExpireHours   uint   `yaml:"refresh_expire_hours"` // Refresh Token 过期时间, 单位为小时

	// 运行时解析后的密钥（不参与 YAML 序列化）
	privateKey *rsa.PrivateKey
	publicKey  *rsa.PublicKey 
}

// ConfigCheck 检查 JWT 配置是否有效
func (c *JWTConfig) ConfigCheck() error {
	if c.PrivateKeyPath == "" || c.PublicKeyPath == "" {
		return fmt.Errorf("jwt: private_key_path 和 public_key_path 不能为空")
	}
	if c.AccessExpireHours <= 0 {
		return fmt.Errorf("jwt: expire_hours 必须大于 0")
	}
	if c.RefreshExpireHours <= 0 {
		return fmt.Errorf("jwt: refresh_expire_hours 必须大于 0")
	}
	return nil
}

// LoadKeys 从 PEM 文件加载 RSA 密钥对
func (c *JWTConfig) LoadKeys() error {
	// 加载私钥
	privKey, err := loadPrivateKey(c.PrivateKeyPath)
	if err != nil {
		return fmt.Errorf("jwt: 加载私钥失败: %w", err)
	}
	c.privateKey = privKey

	// 加载公钥
	pubKey, err := loadPublicKey(c.PublicKeyPath)
	if err != nil {
		return fmt.Errorf("jwt: 加载公钥失败: %w", err)
	}
	c.publicKey = pubKey

	return nil
}

// GetPrivateKey 获取 RSA 私钥
func (c *JWTConfig) GetPrivateKey() *rsa.PrivateKey {
	return c.privateKey
}

// GetPublicKey 获取 RSA 公钥
func (c *JWTConfig) GetPublicKey() *rsa.PublicKey {
	return c.publicKey
}

func loadPrivateKey(path string) (*rsa.PrivateKey, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}

	block, _ := pem.Decode(data)
	if block == nil {
		return nil, fmt.Errorf("无法解析 PEM 数据")
	}

	// 尝试 PKCS#1 格式
	if key, err := x509.ParsePKCS1PrivateKey(block.Bytes); err == nil {
		return key, nil
	}

	// 尝试 PKCS#8 格式
	key, err := x509.ParsePKCS8PrivateKey(block.Bytes)
	if err != nil {
		return nil, fmt.Errorf("不支持的私钥格式: %w", err)
	}

	rsaKey, ok := key.(*rsa.PrivateKey)
	if !ok {
		return nil, fmt.Errorf("不是 RSA 私钥")
	}
	return rsaKey, nil
}

func loadPublicKey(path string) (*rsa.PublicKey, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}

	block, _ := pem.Decode(data)
	if block == nil {
		return nil, fmt.Errorf("无法解析 PEM 数据")
	}

	// 尝试 PKIX 格式（SubjectPublicKeyInfo）
	pub, err := x509.ParsePKIXPublicKey(block.Bytes)
	if err != nil {
		return nil, fmt.Errorf("不支持的公钥格式: %w", err)
	}

	rsaKey, ok := pub.(*rsa.PublicKey)
	if !ok {
		return nil, fmt.Errorf("不是 RSA 公钥")
	}
	return rsaKey, nil
}
