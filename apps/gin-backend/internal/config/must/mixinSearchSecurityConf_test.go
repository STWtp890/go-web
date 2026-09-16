package must

import (
	"testing"
	"time"
)

func TestMixinSearchSecurityConfigCheck(t *testing.T) {
	valid := MixinSearchSecurityConfig{
		CapabilityKeyPath: "secrets/mixin_search_capability.key",
		Issuer:            "go-web",
		Audience:          "mixin-search",
		TokenTTL:          2 * time.Minute,
		IndexCallerID:     "go-web-index-worker",
		SearchCallerID:    "go-web-shadow-search",
	}
	if err := valid.ConfigCheck(); err != nil {
		t.Fatalf("valid config failed: %v", err)
	}

	cases := []struct {
		name   string
		mutate func(*MixinSearchSecurityConfig)
	}{
		{name: "key path", mutate: func(c *MixinSearchSecurityConfig) { c.CapabilityKeyPath = " " }},
		{name: "issuer", mutate: func(c *MixinSearchSecurityConfig) { c.Issuer = "" }},
		{name: "audience", mutate: func(c *MixinSearchSecurityConfig) { c.Audience = "" }},
		{name: "token ttl", mutate: func(c *MixinSearchSecurityConfig) { c.TokenTTL = 0 }},
		{name: "index caller", mutate: func(c *MixinSearchSecurityConfig) { c.IndexCallerID = "" }},
		{name: "search caller", mutate: func(c *MixinSearchSecurityConfig) { c.SearchCallerID = "" }},
	}
	for _, testCase := range cases {
		testCase := testCase
		t.Run(testCase.name, func(t *testing.T) {
			t.Parallel()
			invalid := valid
			testCase.mutate(&invalid)
			if err := invalid.ConfigCheck(); err == nil {
				t.Fatalf("expected a validation error for an invalid %s", testCase.name)
			}
		})
	}
}
