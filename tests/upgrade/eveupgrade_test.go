package upgrade_test

import (
	"testing"

	"github.com/lf-edge/eve/evetest"
)

func TestEVEUpgrade(test *testing.T) {
	evetest.Init(test)
	defer evetest.Close()

	// Define configurable parameters available for the test.
	evetest.DefineTestParameters(
		evetest.HypervisorParameter(),
		evetest.FilesystemParameter(),
	)

	// TODO
}

func TestFailedEVEUpgradeAndRevert(test *testing.T) {
	// TODO
}
