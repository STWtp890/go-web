package config

import (
	"fmt"
	"strings"
)

// 开发期占位凭据黑名单。
//
// 这些值来自仓库内面向本地 Compose 的示例配置。release 模式下绝不允许它们生效——
// 否则「生产部署」会静默沿用公开的示例密钥。仓库内 configs/config.docker.yaml 因此
// 声明为 debug 模式（它服务于本地 Compose，且运行在明文 HTTP 上）。
var (
	placeholderCSRFSecrets = map[string]struct{}{
		"development-only-change-before-production": {},
		"changeme":   {},
		"change-me":  {},
		"secret":     {},
		"csrfsecret": {},
	}
	placeholderPasswords = map[string]struct{}{
		"postgres": {},
		"password": {},
		"changeme": {},
		"root":     {},
	}
)

// minCSRFSecretLength 是 release 模式下 CSRF 密钥的最小长度。
// 该密钥参与 HMAC-SHA256 计算，过短会削弱不可预测性。
const minCSRFSecretLength = 32

// checkReleaseSecrets 在 release 模式下拒绝开发期占位凭据。
//
// 设计取舍：本函数只按 mode 判断，不提供「跳过校验」开关。
// 本地开发若需要 release 形态，必须改为 debug——这样「声称为 release 的配置
// 不得携带开发密钥」是一条无法用注释或开关绕过的硬不变量。
func checkReleaseSecrets(c *Config) error {
	if !strings.EqualFold(strings.TrimSpace(c.ServerConfig.Mode), "release") {
		return nil
	}

	csrfSecret := strings.TrimSpace(c.CustomConfig.Cookie.CSRFSecret)
	if _, placeholder := placeholderCSRFSecrets[strings.ToLower(csrfSecret)]; placeholder {
		return fmt.Errorf(
			"release 模式禁止占位凭据: custom.cookie.csrf_secret 仍是仓库示例值，请提供独立密钥",
		)
	}
	if len(csrfSecret) < minCSRFSecretLength {
		return fmt.Errorf(
			"release 模式要求 custom.cookie.csrf_secret 至少 %d 个字符, 当前 %d",
			minCSRFSecretLength, len(csrfSecret),
		)
	}
	if !c.CustomConfig.Cookie.Secure {
		return fmt.Errorf("release 模式要求 custom.cookie.secure=true (当前为 false)")
	}

	password := strings.ToLower(strings.TrimSpace(c.PostgresConfig.Password))
	if _, placeholder := placeholderPasswords[password]; placeholder {
		return fmt.Errorf(
			"release 模式禁止占位凭据: postgres.password 仍是仓库示例口令，请提供独立口令",
		)
	}

	return nil
}
