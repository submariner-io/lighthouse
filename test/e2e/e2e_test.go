package e2e

import (
	"testing"

	_ "github.com/submariner-io/lighthouse/test/e2e/dataplane"
	"github.com/submariner-io/lighthouse/test/e2e/framework"
)

func init() {
	framework.ParseFlags()
}

func TestE2E(t *testing.T) {

	RunE2ETests(t)
}
