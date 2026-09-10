// pemgenerator 生成供 gin-backend 使用的 RSA 密钥对。
//
// 用法:
//
//	go run ./cmd/pemgenerator             # 默认输出到 configs/
//	go run ./cmd/pemgenerator -out ./keys # 指定输出目录
//	go run ./cmd/pemgenerator -bits 4096  # 指定密钥长度
package main

import (
	"crypto/rand"
	"crypto/rsa"
	"crypto/x509"
	"encoding/pem"
	"flag"
	"fmt"
	"os"
	"path/filepath"
)

func main() {
	var (
		outDir string
		bits   int
	)
	flag.StringVar(&outDir, "out", "configs", "PEM 文件输出目录")
	flag.IntVar(&bits, "bits", 2048, "RSA 密钥长度 (2048 或 4096)")
	flag.Parse()

	if err := os.MkdirAll(outDir, 0o755); err != nil {
		fatalf("创建目录失败: %v", err)
	}

	privateKey, err := rsa.GenerateKey(rand.Reader, bits)
	if err != nil {
		fatalf("生成私钥失败: %v", err)
	}

	privPath := filepath.Join(outDir, "rsa_private.pem")
	if err := savePrivateKey(privPath, privateKey); err != nil {
		fatalf("保存私钥失败: %v", err)
	}

	pubPath := filepath.Join(outDir, "rsa_public.pem")
	if err := savePublicKey(pubPath, &privateKey.PublicKey); err != nil {
		fatalf("保存公钥失败: %v", err)
	}

	fmt.Println("RSA 密钥对生成完成:")
	fmt.Printf("  私钥: %s\n", privPath)
	fmt.Printf("  公钥: %s\n", pubPath)
	fmt.Println("请确保 config.yaml 中路径正确, 且私钥文件已加入 .gitignore")
}

func savePrivateKey(path string, key *rsa.PrivateKey) error {
	// PKCS#8 格式（通用性最好）
	der, err := x509.MarshalPKCS8PrivateKey(key)
	if err != nil {
		return fmt.Errorf("序列化私钥失败: %w", err)
	}

	block := &pem.Block{
		Type:  "PRIVATE KEY",
		Bytes: der,
	}

	return os.WriteFile(path, pem.EncodeToMemory(block), 0o600)
}

func savePublicKey(path string, key *rsa.PublicKey) error {
	// PKIX 格式 (SubjectPublicKeyInfo)
	der, err := x509.MarshalPKIXPublicKey(key)
	if err != nil {
		return fmt.Errorf("序列化公钥失败: %w", err)
	}

	block := &pem.Block{
		Type:  "PUBLIC KEY",
		Bytes: der,
	}

	return os.WriteFile(path, pem.EncodeToMemory(block), 0o644)
}

func fatalf(format string, args ...any) {
	_, _ = fmt.Fprintf(os.Stderr, "错误: "+format+"\n", args...)
	os.Exit(1)
}
