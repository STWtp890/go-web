package must

import (
	"testing"
	"time"
)

func TestIndexDeliveryConfigCheck(t *testing.T) {
	valid := IndexDeliveryConfig{
		Enabled: true, GRPCAddress: "mixin-search:9090", BatchSize: 32, Concurrency: 8,
		PollInterval: 500 * time.Millisecond, LeaseDuration: 2 * time.Minute,
		IndexTimeout: 30 * time.Second, ControlTimeout: 5 * time.Second,
		BaseBackoff: time.Second, MaxBackoff: 5 * time.Minute,
		MaxSendBytes: 16 << 20, HealthTimeout: 2 * time.Second,
	}
	if err := valid.ConfigCheck(); err != nil {
		t.Fatalf("valid config failed: %v", err)
	}

	invalid := valid
	invalid.LeaseDuration = invalid.IndexTimeout
	if err := invalid.ConfigCheck(); err == nil {
		t.Fatal("expected lease/RPC timeout validation error")
	}
}
