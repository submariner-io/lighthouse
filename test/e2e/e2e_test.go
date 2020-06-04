package e2e

import (
	"testing"

	_ "github.com/submariner-io/lighthouse/test/e2e/discovery"
	"github.com/submariner-io/shipyard/test/e2e"
)

func TestE2E(t *testing.T) {
	e2e.RunE2ETests(t)
}
