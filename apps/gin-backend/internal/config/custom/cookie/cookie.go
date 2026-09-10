// Package cookie contains the browser session-cookie configuration.
package cookie

import (
	"fmt"
	"net/http"
	"strings"
)

// CookieConfig controls the cookies used for browser JWT sessions.
// Domain is deliberately optional: leaving it empty creates host-only cookies,
// which is the safer default for both local development and production.
type CookieConfig struct {
	Secure     bool   `yaml:"secure"`
	Domain     string `yaml:"domain"`
	SameSite   string `yaml:"same_site"`
	CSRFSecret string `yaml:"csrf_secret"`
}

func (c *CookieConfig) ConfigCheck() error {
	switch strings.ToLower(c.SameSite) {
	case "lax", "strict", "none":
	default:
		return fmt.Errorf("cookie same_site must be one of lax, strict, none")
	}
	if strings.EqualFold(c.SameSite, "none") && !c.Secure {
		return fmt.Errorf("cookie same_site=none requires secure=true")
	}
	if c.CSRFSecret == "" {
		return fmt.Errorf("cookie csrf_secret cannot be empty")
	}
	return nil
}

// SameSiteMode converts the YAML-friendly representation to net/http's value.
func (c *CookieConfig) SameSiteMode() http.SameSite {
	switch strings.ToLower(c.SameSite) {
	case "strict":
		return http.SameSiteStrictMode
	case "none":
		return http.SameSiteNoneMode
	default:
		return http.SameSiteLaxMode
	}
}
