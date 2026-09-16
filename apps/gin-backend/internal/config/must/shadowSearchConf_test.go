package must

import (
	"testing"
	"time"
)

func TestShadowSearchConfigCheck(t *testing.T) {
	valid := ShadowSearchConfig{
		Enabled: true, GRPCAddress: "mixin-search:9090", QueueSize: 256, Concurrency: 4,
		Timeout: 750 * time.Millisecond, RecordTimeout: 2 * time.Second, TopK: 50,
	}
	if err := valid.ConfigCheck(); err != nil {
		t.Fatalf("valid config: %v", err)
	}
	invalid := valid
	invalid.TopK = 101
	if err := invalid.ConfigCheck(); err == nil {
		t.Fatal("top_k greater than 100 was accepted")
	}
	invalid = valid
	invalid.GRPCAddress = ""
	if err := invalid.ConfigCheck(); err == nil {
		t.Fatal("enabled config without address was accepted")
	}
}
