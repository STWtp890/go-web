// Package sessioncookie owns browser cookie names and their security settings.
package sessioncookie

import (
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/base64"
	"net/http"
	"strings"
	"time"

	"gin-backend/internal/common/service/jwt"
	"gin-backend/internal/config"

	"github.com/gin-gonic/gin"
)

const (
	UserAccessCookie     = "pp_user_at"
	UserRefreshCookie    = "pp_user_rt"
	UserCSRFCookie       = "pp_user_csrf"
	ManagerAccessCookie  = "pp_manager_at"
	ManagerRefreshCookie = "pp_manager_rt"
	ManagerCSRFCookie    = "pp_manager_csrf"

	userProtectedPath    = "/api/v1/protected"
	userRefreshPath      = "/api/v1/public/auth/refresh"
	managerProtectedPath = "/api/v1/protected/manager"
	managerRefreshPath   = "/api/v1/public/manager/refresh"
)

// AccessToken 仅从浏览器 HttpOnly Cookie 读取 Access Token。
func AccessToken(c *gin.Context, cookieName string) string {
	cookie, err := c.Request.Cookie(cookieName)
	if err != nil {
		return ""
	}
	return cookie.Value
}

// RefreshToken 仅从浏览器 HttpOnly Cookie 读取 Refresh Token。
func RefreshToken(c *gin.Context, cookieName string) string {
	cookie, err := c.Request.Cookie(cookieName)
	if err != nil {
		return ""
	}
	return cookie.Value
}

// SetUserTokens 设置用户的访问令牌和刷新令牌到浏览器 cookie 中。
func SetUserTokens(c *gin.Context, accessToken, refreshToken string) error {
	return setTokens(c, UserAccessCookie, UserRefreshCookie, UserCSRFCookie, userProtectedPath, userRefreshPath, accessToken, refreshToken)
}

// SetManagerTokens 设置管理员的访问令牌和刷新令牌到浏览器 cookie 中。
func SetManagerTokens(c *gin.Context, accessToken, refreshToken string) error {
	return setTokens(c, ManagerAccessCookie, ManagerRefreshCookie, ManagerCSRFCookie, managerProtectedPath, managerRefreshPath, accessToken, refreshToken)
}

// ClearUserTokens 清除用户的访问令牌和刷新令牌。
func ClearUserTokens(c *gin.Context) {
	clear(c, UserAccessCookie, userProtectedPath, true)
	clear(c, UserRefreshCookie, userRefreshPath, true)
	clear(c, UserCSRFCookie, "/", false)
}

// ClearManagerTokens 清除管理员的访问令牌和刷新令牌。
func ClearManagerTokens(c *gin.Context) {
	clear(c, ManagerAccessCookie, managerProtectedPath, true)
	clear(c, ManagerRefreshCookie, managerRefreshPath, true)
	clear(c, ManagerCSRFCookie, "/", false)
}

// ValidateCSRF 检查 CSRF token 是否有效。它比较请求头中的 CSRF token 与浏览器 cookie 中的 CSRF token，并验证它们是否匹配。
func ValidateCSRF(c *gin.Context, cookieName, sessionID string) bool {
	header := c.GetHeader("X-CSRF-Token")
	cookie, err := c.Request.Cookie(cookieName)
	if err != nil || header == "" || subtle.ConstantTimeCompare([]byte(header), []byte(cookie.Value)) != 1 {
		return false
	}
	parts := strings.Split(header, ".")
	if len(parts) != 2 {
		return false
	}
	mac, err := base64.RawURLEncoding.DecodeString(parts[1])
	if err != nil {
		return false
	}
	expected := csrfMAC(parts[0], sessionID)
	return subtle.ConstantTimeCompare(mac, expected) == 1
}

// ValidateRefreshCSRF 验证在刷新令牌被使用和轮换之前的 cookie 认证的刷新请求。
func ValidateRefreshCSRF(c *gin.Context, csrfCookieName, refreshToken string) bool {
	claims, err := jwt.ParseTokenClaims(refreshToken, config.CustomConfig().JWT.GetPublicKey(), jwt.TokenUseRefresh)
	if err != nil {
		return false
	}
	sessionID, ok := jwt.SessionIDFromClaims(*claims)
	return ok && ValidateCSRF(c, csrfCookieName, sessionID)
}

func setTokens(c *gin.Context, accessName, refreshName, csrfName, accessPath, refreshPath, accessToken, refreshToken string) error {
	claims, err := jwt.ParseTokenClaims(accessToken, config.CustomConfig().JWT.GetPublicKey(), jwt.TokenUseAccess)
	if err != nil {
		return err
	}
	sessionID, ok := jwt.SessionIDFromClaims(*claims)
	if !ok {
		return http.ErrNoCookie
	}
	csrfToken, err := newCSRFToken(sessionID)
	if err != nil {
		return err
	}
	set(c, accessName, accessToken, accessPath, durationHours(config.CustomConfig().JWT.AccessExpireHours), true)
	set(c, refreshName, refreshToken, refreshPath, durationHours(config.CustomConfig().JWT.RefreshExpireHours), true)
	// CSRF token is intentionally readable by the SPA, so it must be scoped to
	// the document path as well as API requests. It is not a credential.
	set(c, csrfName, csrfToken, "/", durationHours(config.CustomConfig().JWT.RefreshExpireHours), false)
	return nil
}

func newCSRFToken(sessionID string) (string, error) {
	random := make([]byte, 32)
	if _, err := rand.Read(random); err != nil {
		return "", err
	}
	nonce := base64.RawURLEncoding.EncodeToString(random)
	return nonce + "." + base64.RawURLEncoding.EncodeToString(csrfMAC(nonce, sessionID)), nil
}

func csrfMAC(nonce, sessionID string) []byte {
	h := hmac.New(sha256.New, []byte(config.CustomConfig().Cookie.CSRFSecret))
	_, _ = h.Write([]byte(sessionID))
	_, _ = h.Write([]byte("."))
	_, _ = h.Write([]byte(nonce))
	return h.Sum(nil)
}

func set(c *gin.Context, name, value, path string, ttl time.Duration, httpOnly bool) {
	cfg := config.CustomConfig().Cookie
	http.SetCookie(c.Writer, &http.Cookie{
		Name:     name,
		Value:    value,
		Path:     path,
		Domain:   cfg.Domain,
		MaxAge:   int(ttl.Seconds()),
		Secure:   cfg.Secure,
		HttpOnly: httpOnly,
		SameSite: cfg.SameSiteMode(),
	})
}

func clear(c *gin.Context, name, path string, httpOnly bool) {
	cfg := config.CustomConfig().Cookie
	http.SetCookie(c.Writer, &http.Cookie{
		Name:     name,
		Value:    "",
		Path:     path,
		Domain:   cfg.Domain,
		MaxAge:   -1,
		Expires:  time.Unix(1, 0),
		Secure:   cfg.Secure,
		HttpOnly: httpOnly,
		SameSite: cfg.SameSiteMode(),
	})
}

func durationHours(hours uint) time.Duration { return time.Duration(hours) * time.Hour }
