package e2e

import (
	"testing"

	_ "github.com/submariner-io/lighthouse/test/e2e/dataplane"
	e2e "github.com/submariner-io/submariner/test/e2e"
	"github.com/submariner-io/submariner/test/e2e/framework"
)

func init() {
	framework.ParseFlags()
}

func TestE2E(t *testing.T) {
	e2e.RunE2ETests(t)
}
