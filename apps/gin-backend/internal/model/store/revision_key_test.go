package store

import "testing"

func TestEntityRevisionKeys(t *testing.T) {
	if got, want := userCacheKey(42, 7), "cache:user:v2:42:revision:7"; got != want {
		t.Fatalf("user key = %q, want %q", got, want)
	}
	if got, want := managerCacheKey(9, 11), "cache:manager:v2:9:revision:11"; got != want {
		t.Fatalf("manager key = %q, want %q", got, want)
	}
}
