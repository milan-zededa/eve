// Copyright (c) 2017-2018 Zededa, Inc.
// SPDX-License-Identifier: Apache-2.0

package types

import "fmt"

// LedBlinkCount is enum type summarizing all LED blinking patterns.
type LedBlinkCount uint8

const (
	// LedBlinkUndefined - undefined/unknown LED blinking pattern.
	LedBlinkUndefined LedBlinkCount = iota
	// LedBlinkWaitingForIP - LED indication of device waiting to obtain management IP address.
	LedBlinkWaitingForIP
	// LedBlinkConnectingToController - LED indication of device trying to connect to the controller.
	LedBlinkConnectingToController
	// LedBlinkConnectedToController - LED indication of device being connected to the controller but not yet onboarded.
	LedBlinkConnectedToController
	// LedBlinkOnboarded - LED indication of device being connected to the controller and onboarded.
	LedBlinkOnboarded
	// LedBlinkAirplaneMode - LED indication of airplane mode being enabled (permanently disabled radio devices are not indicated)
	LedBlinkAirplaneMode
)
const (
	// LedBlinkOnboardingFailure - LED indication of device failing to onboard.
	LedBlinkOnboardingFailure LedBlinkCount = iota + 10
	_                                       // 11 is unused
	// LedBlinkRespWithoutTLS - LED indication or device receiving response from controller without TLS connection state.
	LedBlinkRespWithoutTLS
	// LedBlinkRespWithoutOSCP - LED indication or device receiving response from controller without OSCP.
	LedBlinkRespWithoutOSCP
	// LedBlinkInvalidControllerCert - LED indication or device failing to validate or fetch the controller certificate.
	LedBlinkInvalidControllerCert
)

// String returns human-readable description of the state indicated by the particular LED blinking count.
func (c LedBlinkCount) String() string {
	switch c {
	case LedBlinkUndefined:
		return "Undefined LED counter"
	case LedBlinkWaitingForIP:
		return "Waiting for DHCP IP address(es)"
	case LedBlinkConnectingToController:
		return "Trying to connect to EV Controller"
	case LedBlinkConnectedToController:
		return "Connected to EV Controller but not onboarded"
	case LedBlinkOnboarded:
		return "Connected to EV Controller and onboarded"
	case LedBlinkAirplaneMode:
		return "Airplane mode is enabled"
	case LedBlinkOnboardingFailure:
		return "Onboarding failure or conflict"
	case LedBlinkRespWithoutTLS:
		return "Response without TLS - ignored"
	case LedBlinkRespWithoutOSCP:
		return "Response without OSCP or bad OSCP - ignored"
	case LedBlinkInvalidControllerCert:
		return "Failed to fetch or verify EV Controller certificate"
	default:
		return fmt.Sprintf("Unsupported LED counter (%d)", c)
	}
}

type LedBlinkCounter struct {
	BlinkCounter LedBlinkCount
}

// Merge the 1/2 values based on having usable addresses or not, with
// the value we get based on access to zedcloud or errors.
func DeriveLedCounter(ledCounter LedBlinkCount, usableAddressCount int, airplaneMode bool) LedBlinkCount {
	if airplaneMode {
		return LedBlinkAirplaneMode
	} else if usableAddressCount == 0 {
		return LedBlinkWaitingForIP
	} else if ledCounter < LedBlinkConnectingToController {
		return LedBlinkConnectingToController
	} else {
		return ledCounter
	}
}
