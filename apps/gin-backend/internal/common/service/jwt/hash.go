package jwt

import (
	"crypto/sha256"
	"encoding/hex"
)

// hashToken 对 token 做 SHA256，避免在 Redis 中存储完整 JWT。
func hashToken(token string) string {
	hash := sha256.Sum256([]byte(token))
	return hex.EncodeToString(hash[:])
}
