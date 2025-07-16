package lps_test

import (
	"testing"

	"github.com/lf-edge/eve/evetest"
)

func TestLPSSuite(test *testing.T) {
	evetest.Init(test)
	defer evetest.Close()

	evetest.DefineTestParameters(
		evetest.HypervisorParameter(),
	)

	evetest.RunTestSuite(
		evetest.TestCase{
			Test: TestNetworkLocalChanges,
		},
	)
}
