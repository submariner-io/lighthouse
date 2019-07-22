package lighthouse

import (
	"testing"

	"github.com/mholt/caddy"
)

// TestSetup tests the various things that should be parsed by setup.
// Make sure you also test for parse errors.
func TestSetup(t *testing.T) {
	c := caddy.NewTestController("dns", `lighthouse`)
	if err := setupLighthouse(c); err != nil {
		t.Fatalf("Expected no errors, but got: %v", err)
	}

	c = caddy.NewTestController("dns", `lighthouse more`)
	if err := setupLighthouse(c); err == nil {
		t.Fatalf("Expected errors, but got: %v", err)
	}
}