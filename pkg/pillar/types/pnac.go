// Copyright (c) 2026 Zededa, Inc.
// SPDX-License-Identifier: Apache-2.0

package types

import (
	"time"

	eveconfig "github.com/lf-edge/eve-api/go/config"
	eveinfo "github.com/lf-edge/eve-api/go/info"
)

// PNACConfig : configuration for Port-based Network Access Control (PNAC).
type PNACConfig struct {
	// Indicates whether 802.1X authentication is enabled on the given port.
	Enabled bool

	// EAP identity (optional).
	// Even when certificate-based authentication is used (e.g., EAP-TLS),
	// an explicit EAP identity may be configured and does not need to match
	// the certificate’s DN or SAN attributes.
	// If no EAP identity is configured and a certificate-based EAP method
	// is used, EVE will derive the identity from the enrolled certificate,
	// preferring the subject common name (CN), or the SAN URI if CN is absent.
	EAPIdentity string `json:",omitempty"`

	// EAP method to use for authentication.
	// Currently, only EAP-TLS is supported; additional methods may be added in the future.
	EAPMethod eveconfig.EAPMethod `json:",omitempty"`

	// Certificate enrollment profile to use for authentication.
	// Relevant only when the selected EAP method requires a certificate (e.g., EAP-TLS).
	//
	// This field references the ProfileName of a certificate enrollment profile defined
	// in EdgeDevConfig (currently SCEP profiles only, see EdgeDevConfig.ScepProfiles).
	// While SCEP is the only supported enrollment protocol today, this field is
	// intended to reference any supported enrollment profile in the future.
	CertEnrollmentProfileName string `json:",omitempty"`
}

// PNACStatus : device-reported status of Port-Based Network Access Control (PNAC)
// using IEEE 802.1X on a specific network port.
type PNACStatus struct {
	// Indicates whether 802.1X authentication is enabled on the given port.
	Enabled bool

	// Current supplicant state as reported by the 802.1X client.
	State eveinfo.SupplicantState

	// Timestamp of the most recent successful 802.1X authentication.
	// Unset if authentication has not yet completed successfully.
	LastAuthTimestamp time.Time

	// Error reported by the supplicant during authentication.
	// May include authentication failures, certificate validation errors,
	// or timeouts.
	Error ErrorDescription
}

// PNACMetrics : IEEE 802.1X Port-Based Network Access Control (PNAC) metrics reported
// by the device for the given port.
type PNACMetrics struct {
	// Logical label identifying the network port associated with these metrics.
	LogicalLabel string
	// Total number of EAPOL frames received from the authenticator.
	EAPOLFramesRx uint64
	// Total number of EAPOL frames transmitted to the authenticator.
	EAPOLFramesTx uint64
	// Number of EAPOL-Start frames transmitted to initiate authentication.
	EAPOLStartFramesTx uint64
	// Number of EAPOL-Logoff frames transmitted to terminate authentication.
	EAPOLLogoffFramesTx uint64
	// Number of EAP-Response frames transmitted in response to authentication requests.
	EAPOLRespFramesTx uint64
	// Number of EAP-Request Identity frames received from the authenticator.
	EAPOLReqIdFramesRx uint64
	// Total number of other EAP-Request frames received from the authenticator.
	EAPOLReqFramesRx uint64
	// Number of invalid or malformed EAPOL frames received.
	EAPOLInvalidFramesRx uint64
	// Number of received EAPOL frames with incorrect length or truncated payload.
	EAPLengthErrorFramesRx uint64
}
