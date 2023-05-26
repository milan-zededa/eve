// Copyright (c) 2023 Zededa, Inc.
// SPDX-License-Identifier: Apache-2.0

package types

import "fmt"

// IPAddrNotAvail is returned when there is no (suitable) IP address
// assigned to a given interface.
type IPAddrNotAvail struct {
	PortLL string
}

// Error message.
func (e *IPAddrNotAvail) Error() string {
	return fmt.Sprintf("port %s: no suitable IP address available",
		e.PortLL)
}

// DNSNotAvail is returned when there is no DNS server configured
// for a given interface.
type DNSNotAvail struct {
	PortLL string
}

// Error message.
func (e *DNSNotAvail) Error() string {
	return fmt.Sprintf("port %s: no DNS server available",
		e.PortLL)
}
