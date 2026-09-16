package config

import (
	"strings"
	"testing"

	"gin-backend/internal/config/custom"
	"gin-backend/internal/config/custom/cookie"
	"gin-backend/internal/config/must"
)

func strongReleaseConfig() *Config {
	c := NewConfig()
	c.ServerConfig = must.ServerConfig{Port: 8080, Mode: "release"}
	c.PostgresConfig = must.PostgresConfig{Password: "a-strong-unique-password"}
	c.CustomConfig = custom.CustomConfig{
		Cookie: cookie.CookieConfig{
			Secure:     true,
			CSRFSecret: strings.Repeat("k", minCSRFSecretLength),
		},
	}
	return c
}

func TestCheckReleaseSecrets(t *testing.T) {
	tests := []struct {
		name    string
		mutate  func(*Config)
		wantErr bool
	}{
		{
			name: "debug 模式允许仓库示例凭据",
			mutate: func(c *Config) {
				c.ServerConfig.Mode = "debug"
				c.CustomConfig.Cookie.CSRFSecret = "development-only-change-before-production"
				c.CustomConfig.Cookie.Secure = false
				c.PostgresConfig.Password = "postgres"
			},
			wantErr: false,
		},
		{
			name:    "release 接受合规配置",
			mutate:  func(*Config) {},
			wantErr: false,
		},
		{
			name: "release 拒绝占位 csrf_secret",
			mutate: func(c *Config) {
				c.CustomConfig.Cookie.CSRFSecret = "development-only-change-before-production"
			},
			wantErr: true,
		},
		{
			name: "release 拒绝过短 csrf_secret",
			mutate: func(c *Config) {
				c.CustomConfig.Cookie.CSRFSecret = "too-short"
			},
			wantErr: true,
		},
		{
			name: "release 要求 cookie.secure=true",
			mutate: func(c *Config) {
				c.CustomConfig.Cookie.Secure = false
			},
			wantErr: true,
		},
		{
			name: "release 拒绝占位 postgres 口令",
			mutate: func(c *Config) {
				c.PostgresConfig.Password = "postgres"
			},
			wantErr: true,
		},
	}

	for _, testCase := range tests {
		t.Run(testCase.name, func(t *testing.T) {
			c := strongReleaseConfig()
			testCase.mutate(c)

			err := checkReleaseSecrets(c)
			if testCase.wantErr && err == nil {
				t.Fatal("expected an error, got nil")
			}
			if !testCase.wantErr && err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
		})
	}
}
