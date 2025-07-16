package upgrade_test

import (
	"testing"

	"github.com/lf-edge/eve/evetest"
)

func TestEVEUpgradeSuite(test *testing.T) {
	evetest.Init(test)
	defer evetest.Close()

	// Define parameters for the entire test suite.
	evetest.DefineTestParameters(
		evetest.HypervisorParameter(),
		evetest.FilesystemParameter(),
	)

	evetest.RunTestSuite(
		evetest.TestCase{
			Test: TestEVEUpgrade,
		},
		evetest.TestCase{
			Test: TestFailedEVEUpgradeAndRevert,
		},
	)
}
