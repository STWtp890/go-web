// Package common 提供项目级通用工具。
package common

import (
	"crypto/rand"
	"fmt"
	"time"
)

// NewUUIDv7 生成符合 RFC 9562 的 UUIDv7 字符串。
// UUIDv7 以毫秒级 Unix 时间戳为前缀，后接随机数，
// 兼具时间有序性与防遍历攻击能力。
//
// 格式: xxxxxxxx-xxxx-7xxx-xxxx-xxxxxxxxxxxx (36 字符)
//   - 前 48 bits: Unix 毫秒时间戳 (big-endian)
//   - 第 49-52 bits: 版本号 0111 (7)
//   - 第 53-64 bits: 随机数的前 12 bits
//   - 第 65-68 bits: 变体标识 10xx
//   - 剩余 62 bits: 随机数
func NewUUIDv7() (string, error) {
	var buf [16]byte

	// 时间戳 (48 bits, big-endian 毫秒)
	ts := uint64(time.Now().UnixMilli())
	buf[0] = byte(ts >> 40)
	buf[1] = byte(ts >> 32)
	buf[2] = byte(ts >> 24)
	buf[3] = byte(ts >> 16)
	buf[4] = byte(ts >> 8)
	buf[5] = byte(ts)

	// 随机数 (10 bytes = 80 bits)
	if _, err := rand.Read(buf[6:]); err != nil {
		return "", fmt.Errorf("uuidv7: rand read: %w", err)
	}

	// 版本号: 0111xxxx (7)
	buf[6] = (buf[6] & 0x0f) | 0x70

	// 变体: 10xxxxxx
	buf[8] = (buf[8] & 0x3f) | 0x80

	return fmtUUID(buf), nil
}

// MustUUIDv7 生成 UUIDv7，失败时 panic（适用于初始化阶段）。
func MustUUIDv7() string {
	id, err := NewUUIDv7()
	if err != nil {
		panic("uuidv7: " + err.Error())
	}
	return id
}

// IsValidUUIDv7 校验字符串是否为合法 UUIDv7 格式。
func IsValidUUIDv7(s string) bool {
	if len(s) != 36 {
		return false
	}
	// 检查分隔符位置
	if s[8] != '-' || s[13] != '-' || s[18] != '-' || s[23] != '-' {
		return false
	}
	// 版本号位置 (第14字符) 应为 '7'
	if s[14] != '7' {
		return false
	}
	// 变体位置 (第19字符) 应为 '8', '9', 'a', 'b'
	switch s[19] {
	case '8', '9', 'a', 'b', 'A', 'B':
	default:
		return false
	}
	return true
}

// fmtUUID 将 16 字节格式化为 "xxxxxxxx-xxxx-xxxx-xxxx-xxxxxxxxxxxx"。
func fmtUUID(b [16]byte) string {
	return fmt.Sprintf("%08x-%04x-%04x-%04x-%012x",
		b[0:4], b[4:6], b[6:8], b[8:10], b[10:16])
}
