package types

import (
	"github.com/stretchr/testify/assert"
	"testing"
)

func TestDeriveLedCounter(t *testing.T) {

	testMatrix := map[string]struct {
		ledBlinkCount      LedBlinkCount
		usableAddressCount int
		airplaneMode       bool
		expectedValue      LedBlinkCount
	}{
		"usableAddressCount is 0": {
			ledBlinkCount:      LedBlinkUndefined,
			usableAddressCount: 0,
			expectedValue:      LedBlinkWaitingForIP,
		},
		"ledBlinkCount less than 2 (without IP)": {
			ledBlinkCount:      LedBlinkUndefined,
			usableAddressCount: 1,
			expectedValue:      LedBlinkConnectingToController,
		},
		"ledBlinkCount is 2 (has IP)": {
			ledBlinkCount:      LedBlinkConnectingToController,
			usableAddressCount: 1,
			expectedValue:      LedBlinkConnectingToController,
		},
		"ledBlinkCount is greater than 2 (connected)": {
			ledBlinkCount:      LedBlinkConnectedToController,
			usableAddressCount: 1,
			expectedValue:      LedBlinkConnectedToController,
		},
		"airplane mode is enabled (no usable addresses)": {
			ledBlinkCount:      LedBlinkUndefined,
			usableAddressCount: 0,
			airplaneMode:       true,
			expectedValue:      LedBlinkAirplaneMode,
		},
		"airplane mode is enabled (have usable addresses)": {
			ledBlinkCount:      LedBlinkConnectedToController,
			usableAddressCount: 12,
			airplaneMode:       true,
			expectedValue:      LedBlinkAirplaneMode,
		},
	}

	for testname, test := range testMatrix {
		t.Logf("Running test case %s", testname)
		output := DeriveLedCounter(test.ledBlinkCount, test.usableAddressCount, test.airplaneMode)
		assert.Equal(t, test.expectedValue, output)
	}
}
