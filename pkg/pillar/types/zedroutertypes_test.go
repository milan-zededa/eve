// Copyright (c) 2019-2021 Zededa, Inc.
// SPDX-License-Identifier: Apache-2.0

package types

import (
	"net"

	"github.com/satori/go.uuid"
	"github.com/stretchr/testify/assert"
	"testing"
	"time"
)

var underlayUUID = uuid.UUID{0x6b, 0xa7, 0xb8, 0x10, 0x9d, 0xad, 0x11, 0xd1,
	0x80, 0xb4, 0x00, 0xc0, 0x4f, 0xd4, 0x30, 0xc8}
var appNetworkConfig = AppNetworkConfig{
	UnderlayNetworkList: []UnderlayNetworkConfig{
		{Network: underlayUUID},
	},
}

func TestIsIPv6(t *testing.T) {
	testMatrix := map[string]struct {
		config        NetworkInstanceConfig
		expectedValue bool
	}{
		"AddressTypeIPV6": {
			config:        NetworkInstanceConfig{IpType: AddressTypeIPV6},
			expectedValue: true,
		},
		"AddressTypeCryptoIPV6": {
			config:        NetworkInstanceConfig{IpType: AddressTypeCryptoIPV6},
			expectedValue: true,
		},
		"AddressTypeIPV4": {
			config:        NetworkInstanceConfig{IpType: AddressTypeIPV4},
			expectedValue: false,
		},
		"AddressTypeCryptoIPV4": {
			config:        NetworkInstanceConfig{IpType: AddressTypeCryptoIPV4},
			expectedValue: false,
		},
		"AddressTypeNone": {
			config:        NetworkInstanceConfig{IpType: AddressTypeNone},
			expectedValue: false,
		},
		"AddressTypeLast": {
			config:        NetworkInstanceConfig{IpType: AddressTypeLast},
			expectedValue: false,
		},
	}

	for testname, test := range testMatrix {
		t.Logf("Running test case %s", testname)
		isIPv6 := test.config.IsIPv6()
		assert.IsType(t, test.expectedValue, isIPv6)
	}
}
func TestGetUnderlayConfig(t *testing.T) {
	testMatrix := map[string]struct {
		network uuid.UUID
		config  AppNetworkConfig
	}{
		"Underlay UUID": {
			network: underlayUUID,
			config:  appNetworkConfig,
		},
	}
	for testname, test := range testMatrix {
		t.Logf("Running test case %s", testname)
		config := test.config.getUnderlayConfig(test.network)
		assert.IsType(t, test.config.UnderlayNetworkList[0], *config)
	}
}
func TestIsNetworkUsed(t *testing.T) {
	var otherUUID = uuid.UUID{0x6b, 0xa7, 0xb8, 0x10, 0x9d, 0xad, 0x11, 0xd1,
		0x80, 0xb4, 0x00, 0xc0, 0xb8, 0xd4, 0x30, 0xc8}
	testMatrix := map[string]struct {
		network       uuid.UUID
		expectedValue bool
		config        AppNetworkConfig
	}{
		"Underlay UUID": {
			network:       underlayUUID,
			expectedValue: true,
			config:        appNetworkConfig,
		},
		"Other UUID": {
			network:       otherUUID,
			expectedValue: false,
			config:        appNetworkConfig,
		},
	}
	for testname, test := range testMatrix {
		t.Logf("Running test case %s", testname)
		networkUsed := test.config.IsNetworkUsed(test.network)
		assert.Equal(t, test.expectedValue, networkUsed)
	}
}

// Make sure IsDPCUsable passes
var usablePort = NetworkPortConfig{
	IfName:       "eth0",
	Phylabel:     "eth0",
	Logicallabel: "eth0",
	IsMgmt:       true,
	DhcpConfig:   DhcpConfig{Dhcp: DT_CLIENT},
}
var usablePorts = []NetworkPortConfig{usablePort}

var unusablePort1 = NetworkPortConfig{
	IfName:       "eth0",
	Phylabel:     "eth0",
	Logicallabel: "eth0",
	IsMgmt:       false,
	DhcpConfig:   DhcpConfig{Dhcp: DT_CLIENT},
}
var unusablePorts1 = []NetworkPortConfig{unusablePort1}

var unusablePort2 = NetworkPortConfig{
	IfName:       "eth0",
	Phylabel:     "eth0",
	Logicallabel: "eth0",
	IsMgmt:       true,
	DhcpConfig:   DhcpConfig{Dhcp: DT_NONE},
}
var unusablePorts2 = []NetworkPortConfig{unusablePort2}
var mixedPorts = []NetworkPortConfig{usablePort, unusablePort1, unusablePort2}

func TestIsDPCUsable(t *testing.T) {
	n := time.Now()
	testMatrix := map[string]struct {
		devicePortConfig DevicePortConfig
		expectedValue    bool
	}{
		"Management and DT_CLIENT": {
			devicePortConfig: DevicePortConfig{
				TestResults: TestResults{
					LastFailed:    time.Time{},
					LastSucceeded: n,
				},
				Ports: usablePorts,
			},
			expectedValue: true,
		},
		"Mixture of usable and unusable ports": {
			devicePortConfig: DevicePortConfig{
				TestResults: TestResults{
					LastFailed:    time.Time{},
					LastSucceeded: n,
				},
				Ports: mixedPorts,
			},
			expectedValue: true,
		},
		"Not management and DT_CLIENT": {
			devicePortConfig: DevicePortConfig{
				TestResults: TestResults{
					LastFailed:    time.Time{},
					LastSucceeded: n,
				},
				Ports: unusablePorts1,
			},
			expectedValue: false,
		},
		"Management and DT_NONE": {
			devicePortConfig: DevicePortConfig{
				TestResults: TestResults{
					LastFailed:    time.Time{},
					LastSucceeded: n,
				},
				Ports: unusablePorts2,
			},
			expectedValue: false,
		},
	}
	for testname, test := range testMatrix {
		t.Logf("Running test case %s", testname)
		value := test.devicePortConfig.IsDPCUsable()
		assert.Equal(t, test.expectedValue, value)
	}
}

func TestIsDPCTestable(t *testing.T) {
	n := time.Now()
	testMatrix := map[string]struct {
		devicePortConfig DevicePortConfig
		expectedValue    bool
	}{
		"Difference is exactly 60 seconds": {
			devicePortConfig: DevicePortConfig{
				TestResults: TestResults{
					LastFailed:    n.Add(time.Second * 60),
					LastSucceeded: n,
				},
				Ports: usablePorts,
			},
			expectedValue: false,
		},
		"Difference is 61 seconds": {
			devicePortConfig: DevicePortConfig{
				TestResults: TestResults{
					LastFailed:    n.Add(time.Second * 61),
					LastSucceeded: n,
				},
				Ports: usablePorts,
			},
			expectedValue: false,
		},
		"Difference is 59 seconds": {
			devicePortConfig: DevicePortConfig{
				TestResults: TestResults{
					LastFailed:    n.Add(time.Second * 59),
					LastSucceeded: n,
				},
				Ports: usablePorts,
			},
			expectedValue: false,
		},
		"LastFailed is 0": {
			devicePortConfig: DevicePortConfig{
				TestResults: TestResults{
					LastFailed:    time.Time{},
					LastSucceeded: n,
				},
				Ports: usablePorts,
			},
			expectedValue: true,
		},
		"Last Succeeded is after Last Failed": {
			devicePortConfig: DevicePortConfig{
				TestResults: TestResults{
					LastFailed:    n,
					LastSucceeded: n.Add(time.Second * 61),
				},
				Ports: usablePorts,
			},
			expectedValue: true,
		},
	}
	for testname, test := range testMatrix {
		t.Logf("Running test case %s", testname)
		value := test.devicePortConfig.IsDPCTestable(5 * time.Minute)
		assert.Equal(t, test.expectedValue, value)
	}
}

func TestIsDPCUntested(t *testing.T) {
	n := time.Now()
	testMatrix := map[string]struct {
		devicePortConfig DevicePortConfig
		expectedValue    bool
	}{
		"Last failed and Last Succeeded are 0": {
			devicePortConfig: DevicePortConfig{
				TestResults: TestResults{
					LastFailed:    time.Time{},
					LastSucceeded: time.Time{},
				},
				Ports: usablePorts,
			},
			expectedValue: true,
		},
		"Last Succeeded is not 0": {
			devicePortConfig: DevicePortConfig{
				TestResults: TestResults{
					LastFailed:    time.Time{},
					LastSucceeded: n,
				},
				Ports: usablePorts,
			},
			expectedValue: false,
		},
		"Last failed is not 0": {
			devicePortConfig: DevicePortConfig{
				TestResults: TestResults{
					LastFailed:    time.Time{},
					LastSucceeded: n,
				},
				Ports: usablePorts,
			},
			expectedValue: false,
		},
	}
	for testname, test := range testMatrix {
		t.Logf("Running test case %s", testname)
		value := test.devicePortConfig.IsDPCUntested()
		assert.Equal(t, test.expectedValue, value)
	}
}

func TestWasDPCWorking(t *testing.T) {
	n := time.Now()
	testMatrix := map[string]struct {
		devicePortConfig DevicePortConfig
		expectedValue    bool
	}{
		"Last Succeeded is 0": {
			devicePortConfig: DevicePortConfig{
				TestResults: TestResults{
					LastFailed:    n,
					LastSucceeded: time.Time{},
				},
				Ports: usablePorts,
			},
			expectedValue: false,
		},
		"Last Succeeded is after Last Failed": {
			devicePortConfig: DevicePortConfig{
				TestResults: TestResults{
					LastFailed:    n,
					LastSucceeded: n.Add(time.Second * 60),
				},
				Ports: usablePorts,
			},
			expectedValue: true,
		},
		"Last Failed is after Last Succeeded": {
			devicePortConfig: DevicePortConfig{
				TestResults: TestResults{
					LastFailed:    n.Add(time.Second * 60),
					LastSucceeded: n,
				},
				Ports: usablePorts,
			},
			expectedValue: false,
		},
	}
	for testname, test := range testMatrix {
		t.Logf("Running test case %s", testname)
		value := test.devicePortConfig.WasDPCWorking()
		assert.Equal(t, test.expectedValue, value)
	}
}

func TestGetPortByIfName(t *testing.T) {
	testMatrix := map[string]struct {
		deviceNetworkStatus DeviceNetworkStatus
		port                string
		expectedValue       NetworkPortStatus
	}{
		"Test IfnName is port one": {
			deviceNetworkStatus: DeviceNetworkStatus{
				Version: DPCIsMgmt,
				Ports: []NetworkPortStatus{
					{IfName: "port one"},
				},
			},
			port: "port one",
			expectedValue: NetworkPortStatus{
				IfName: "port one",
			},
		},
	}
	for testname, test := range testMatrix {
		t.Logf("Running test case %s", testname)
		value := test.deviceNetworkStatus.GetPortByIfName(test.port)
		assert.Equal(t, test.expectedValue, *value)
	}
}

func TestGetPortCostList(t *testing.T) {
	testMatrix := map[string]struct {
		deviceNetworkStatus DeviceNetworkStatus
		expectedValue       []uint8
	}{
		"Test single": {
			deviceNetworkStatus: DeviceNetworkStatus{
				Version: DPCIsMgmt,
				Ports: []NetworkPortStatus{
					{IfName: "port one",
						IsMgmt: true,
						Cost:   0},
				},
			},
			expectedValue: []uint8{0},
		},
		"Test empty": {
			deviceNetworkStatus: DeviceNetworkStatus{},
			expectedValue:       []uint8{},
		},
		"Test no management": {
			deviceNetworkStatus: DeviceNetworkStatus{
				Version: DPCIsMgmt,
				Ports: []NetworkPortStatus{
					{IfName: "port one",
						IsMgmt: false,
						Cost:   1},
				},
			},
			expectedValue: []uint8{1},
		},
		"Test duplicates": {
			deviceNetworkStatus: DeviceNetworkStatus{
				Version: DPCIsMgmt,
				Ports: []NetworkPortStatus{
					{IfName: "port one",
						IsMgmt: true,
						Cost:   17},
					{IfName: "port two",
						IsMgmt: true,
						Cost:   1},
					{IfName: "port three",
						IsMgmt: true,
						Cost:   17},
				},
			},
			expectedValue: []uint8{1, 17},
		},
		"Test reverse": {
			deviceNetworkStatus: DeviceNetworkStatus{
				Version: DPCIsMgmt,
				Ports: []NetworkPortStatus{
					{IfName: "port one",
						IsMgmt: true,
						Cost:   2},
					{IfName: "port two",
						IsMgmt: true,
						Cost:   1},
					{IfName: "port three",
						IsMgmt: true,
						Cost:   0},
				},
			},
			expectedValue: []uint8{0, 1, 2},
		},
	}
	for testname, test := range testMatrix {
		t.Logf("Running test case %s", testname)
		value := test.deviceNetworkStatus.GetPortCostList()
		assert.Equal(t, test.expectedValue, value)
	}
}

func portsToIfNames(ports []*NetworkPortStatus) []string {
	ifNames := []string{}
	for _, p := range ports {
		ifNames = append(ifNames, p.IfName)
	}
	return ifNames
}

// TestGetMgmtPortsSortedByCost covers both GetMgmtPortsSortedByCost and GetAllPortsSortedByCost.
func TestGetMgmtPortsSortedByCost(t *testing.T) {
	testMatrix := map[string]struct {
		deviceNetworkStatus DeviceNetworkStatus
		rotate              int
		expectedMgmtValue   []string
		expectedAllValue    []string
	}{
		"Test single": {
			deviceNetworkStatus: DeviceNetworkStatus{
				Version: DPCIsMgmt,
				Ports: []NetworkPortStatus{
					{IfName: "port1",
						IsMgmt: true,
						Cost:   0},
				},
			},
			expectedMgmtValue: []string{"port1"},
			expectedAllValue:  []string{"port1"},
		},
		"Test single rotate": {
			deviceNetworkStatus: DeviceNetworkStatus{
				Version: DPCIsMgmt,
				Ports: []NetworkPortStatus{
					{IfName: "port1",
						IsMgmt: true,
						Cost:   0},
				},
			},
			rotate:            14,
			expectedMgmtValue: []string{"port1"},
			expectedAllValue:  []string{"port1"},
		},
		"Test empty": {
			deviceNetworkStatus: DeviceNetworkStatus{},
			expectedMgmtValue:   []string{},
			expectedAllValue:    []string{},
		},
		"Test no management": {
			deviceNetworkStatus: DeviceNetworkStatus{
				Version: DPCIsMgmt,
				Ports: []NetworkPortStatus{
					{IfName: "port1",
						IsMgmt: false,
						Cost:   0},
				},
			},
			rotate:            14,
			expectedMgmtValue: []string{},
			expectedAllValue:  []string{"port1"},
		},
		"Test duplicates": {
			deviceNetworkStatus: DeviceNetworkStatus{
				Version: DPCIsMgmt,
				Ports: []NetworkPortStatus{
					{IfName: "port1",
						IsMgmt: true,
						Cost:   17},
					{IfName: "port2",
						IsMgmt: true,
						Cost:   1},
					{IfName: "port3",
						IsMgmt: true,
						Cost:   17},
					{IfName: "port4",
						IsMgmt: true,
						Cost:   1},
				},
			},
			expectedMgmtValue: []string{"port2", "port4", "port1", "port3"},
			expectedAllValue:  []string{"port2", "port4", "port1", "port3"},
		},
		"Test duplicates rotate": {
			deviceNetworkStatus: DeviceNetworkStatus{
				Version: DPCIsMgmt,
				Ports: []NetworkPortStatus{
					{IfName: "port1",
						IsMgmt: true,
						Cost:   17},
					{IfName: "port2",
						IsMgmt: true,
						Cost:   1},
					{IfName: "port3",
						IsMgmt: true,
						Cost:   17},
					{IfName: "port4",
						IsMgmt: true,
						Cost:   1},
				},
			},
			rotate:            1,
			expectedMgmtValue: []string{"port4", "port2", "port3", "port1"},
			expectedAllValue:  []string{"port4", "port2", "port3", "port1"},
		},
		"Test duplicates some management": {
			deviceNetworkStatus: DeviceNetworkStatus{
				Version: DPCIsMgmt,
				Ports: []NetworkPortStatus{
					{IfName: "port1",
						IsMgmt: false,
						Cost:   17},
					{IfName: "port2",
						IsMgmt: false,
						Cost:   1},
					{IfName: "port3",
						IsMgmt: true,
						Cost:   17},
					{IfName: "port4",
						IsMgmt: true,
						Cost:   1},
				},
			},
			expectedMgmtValue: []string{"port4", "port3"},
			expectedAllValue:  []string{"port2", "port4", "port1", "port3"},
		},
		"Test reverse": {
			deviceNetworkStatus: DeviceNetworkStatus{
				Version: DPCIsMgmt,
				Ports: []NetworkPortStatus{
					{IfName: "port1",
						IsMgmt: true,
						Cost:   2},
					{IfName: "port2",
						IsMgmt: true,
						Cost:   1},
					{IfName: "port3",
						IsMgmt: true,
						Cost:   0},
				},
			},
			expectedMgmtValue: []string{"port3", "port2", "port1"},
			expectedAllValue:  []string{"port3", "port2", "port1"},
		},
		"Test reverse some management": {
			deviceNetworkStatus: DeviceNetworkStatus{
				Version: DPCIsMgmt,
				Ports: []NetworkPortStatus{
					{IfName: "port1",
						IsMgmt: true,
						Cost:   2},
					{IfName: "port2",
						IsMgmt: false,
						Cost:   1},
					{IfName: "port3",
						IsMgmt: true,
						Cost:   0},
				},
			},
			expectedMgmtValue: []string{"port3", "port1"},
			expectedAllValue:  []string{"port3", "port2", "port1"},
		},
	}
	for testname, test := range testMatrix {
		t.Logf("Running test case %s", testname)
		ports := test.deviceNetworkStatus.GetMgmtPortsSortedByCost(test.rotate)
		assert.Equal(t, test.expectedMgmtValue, portsToIfNames(ports))
		ports = test.deviceNetworkStatus.GetAllPortsSortedByCost(test.rotate)
		assert.Equal(t, test.expectedAllValue, portsToIfNames(ports))
	}
}

func TestGetMgmtPortsByCost(t *testing.T) {
	testMatrix := map[string]struct {
		deviceNetworkStatus DeviceNetworkStatus
		cost                uint8
		expectedValue       []string
	}{
		"Test single": {
			deviceNetworkStatus: DeviceNetworkStatus{
				Version: DPCIsMgmt,
				Ports: []NetworkPortStatus{
					{IfName: "port1",
						IsMgmt: true,
						Cost:   0},
				},
			},
			cost:          0,
			expectedValue: []string{"port1"},
		},
		"Test single wrong cost": {
			deviceNetworkStatus: DeviceNetworkStatus{
				Version: DPCIsMgmt,
				Ports: []NetworkPortStatus{
					{IfName: "port1",
						IsMgmt: true,
						Cost:   0},
				},
			},
			cost:          14,
			expectedValue: []string{},
		},
		"Test empty": {
			deviceNetworkStatus: DeviceNetworkStatus{},
			cost:                0,
			expectedValue:       []string{},
		},
		"Test no management": {
			deviceNetworkStatus: DeviceNetworkStatus{
				Version: DPCIsMgmt,
				Ports: []NetworkPortStatus{
					{IfName: "port1",
						IsMgmt: false,
						Cost:   0},
				},
			},
			cost:          0,
			expectedValue: []string{},
		},
		"Test duplicates cost 1": {
			deviceNetworkStatus: DeviceNetworkStatus{
				Version: DPCIsMgmt,
				Ports: []NetworkPortStatus{
					{IfName: "port1",
						IsMgmt: true,
						Cost:   17},
					{IfName: "port2",
						IsMgmt: true,
						Cost:   1},
					{IfName: "port3",
						IsMgmt: true,
						Cost:   17},
					{IfName: "port4",
						IsMgmt: true,
						Cost:   1},
				},
			},
			cost:          1,
			expectedValue: []string{"port2", "port4"},
		},
		"Test duplicates cost 17": {
			deviceNetworkStatus: DeviceNetworkStatus{
				Version: DPCIsMgmt,
				Ports: []NetworkPortStatus{
					{IfName: "port1",
						IsMgmt: true,
						Cost:   17},
					{IfName: "port2",
						IsMgmt: true,
						Cost:   1},
					{IfName: "port3",
						IsMgmt: true,
						Cost:   17},
					{IfName: "port4",
						IsMgmt: true,
						Cost:   1},
				},
			},
			cost:          17,
			expectedValue: []string{"port1", "port3"},
		},
		"Test duplicates bad cost": {
			deviceNetworkStatus: DeviceNetworkStatus{
				Version: DPCIsMgmt,
				Ports: []NetworkPortStatus{
					{IfName: "port1",
						IsMgmt: true,
						Cost:   17},
					{IfName: "port2",
						IsMgmt: true,
						Cost:   1},
					{IfName: "port3",
						IsMgmt: true,
						Cost:   17},
					{IfName: "port4",
						IsMgmt: true,
						Cost:   1},
				},
			},
			cost:          18,
			expectedValue: []string{},
		},
		"Test duplicates some management": {
			deviceNetworkStatus: DeviceNetworkStatus{
				Version: DPCIsMgmt,
				Ports: []NetworkPortStatus{
					{IfName: "port1",
						IsMgmt: false,
						Cost:   17},
					{IfName: "port2",
						IsMgmt: false,
						Cost:   1},
					{IfName: "port3",
						IsMgmt: true,
						Cost:   17},
					{IfName: "port4",
						IsMgmt: true,
						Cost:   1},
				},
			},
			cost:          17,
			expectedValue: []string{"port3"},
		},
	}
	for testname, test := range testMatrix {
		t.Logf("Running test case %s", testname)
		ports := test.deviceNetworkStatus.GetMgmtPortsByCost(test.cost)
		assert.Equal(t, test.expectedValue, portsToIfNames(ports))
	}
}

// Common DeviceNetworkStatus with addresses and costs; link-local etc
// for the Count and Get functions
// Note that
var (
	commonDeviceNetworkStatus = DeviceNetworkStatus{
		Version: DPCIsMgmt,
		Ports: []NetworkPortStatus{
			{
				// Global and link local
				IfName: "port1",
				IsMgmt: true,
				Cost:   17,
				AddrInfoList: []AddrInfo{
					{Addr: addrIPv4Global1},
					{Addr: addrIPv4Local1},
					{Addr: addrIPv6Global1},
					{Addr: addrIPv6Local1},
					{Addr: addrIPv4Global5},
				},
			},
			{
				// Only link local
				IfName: "port2",
				IsMgmt: true,
				Cost:   1,
				AddrInfoList: []AddrInfo{
					{Addr: addrIPv4Local2},
					{Addr: addrIPv6Local2},
				},
			},
			{
				// Has no AddrInfo
				IfName: "port3",
				IsMgmt: true,
				Cost:   17,
			},
			{
				// Global and link local; more globals per if
				IfName: "port4",
				IsMgmt: true,
				Cost:   1,
				AddrInfoList: []AddrInfo{
					{Addr: addrIPv4Global4},
					{Addr: addrIPv4Local4},
					{Addr: addrIPv6Global4},
					{Addr: addrIPv6Local4},
					{Addr: addrIPv4Global3},
					{Addr: addrIPv6Global3},
					{Addr: addrIPv4Global6},
				},
			},
			{
				// Has no IP addresses but has AddrInfo
				IfName: "port5",
				IsMgmt: true,
				Cost:   17,
				AddrInfoList: []AddrInfo{
					{LastGeoTimestamp: time.Now()},
					{LastGeoTimestamp: time.Now()},
				},
			},
		},
	}

	addrIPv4Global1 = net.ParseIP("192.168.1.10")
	addrIPv4Global2 = net.ParseIP("192.168.2.10")
	addrIPv4Global3 = net.ParseIP("192.168.3.10")
	addrIPv4Global4 = net.ParseIP("192.168.4.10")
	addrIPv4Global5 = net.ParseIP("192.168.5.10")
	addrIPv4Global6 = net.ParseIP("192.168.6.10")
	addrIPv4Local1  = net.ParseIP("169.254.99.1")
	addrIPv4Local2  = net.ParseIP("169.254.99.2")
	addrIPv4Local3  = net.ParseIP("169.254.99.3")
	addrIPv4Local4  = net.ParseIP("169.254.99.4")
	addrIPv6Global1 = net.ParseIP("fec0::1")
	addrIPv6Global2 = net.ParseIP("fec0::2")
	addrIPv6Global3 = net.ParseIP("fec0::3")
	addrIPv6Global4 = net.ParseIP("fec0::4")
	addrIPv6Local1  = net.ParseIP("fe80::1")
	addrIPv6Local2  = net.ParseIP("fe80::2")
	addrIPv6Local3  = net.ParseIP("fe80::3")
	addrIPv6Local4  = net.ParseIP("fe80::4")
)

func TestCountAddrsExceptLinkLocal(t *testing.T) {
	testMatrix := map[string]struct {
		expectedValue int
	}{
		"Test CountAddrsExceptLinkLocal": {
			expectedValue: 8,
		},
	}
	for testname, test := range testMatrix {
		t.Logf("Running test case %s", testname)
		value := commonDeviceNetworkStatus.CountAddrsExceptLinkLocal()
		assert.Equal(t, test.expectedValue, value)
	}
}

func TestPortCountAddrsExceptLinkLocal(t *testing.T) {
	testMatrix := map[string]struct {
		ifname        string
		expectFail    bool
		expectedValue int
	}{
		"Test port1 CountAddrsExceptLinkLocal": {
			ifname:        "port1",
			expectedValue: 3,
		},
		"Test port2 CountAddrsExceptLinkLocal": {
			ifname:        "port2",
			expectedValue: 0,
			expectFail:    true,
		},
		"Test port3 CountAddrsExceptLinkLocal": {
			ifname:        "port3",
			expectedValue: 0,
			expectFail:    true,
		},
		"Test port4 CountAddrsExceptLinkLocal": {
			ifname:        "port4",
			expectedValue: 5,
		},
	}
	for testname, test := range testMatrix {
		t.Logf("Running test case %s", testname)
		port := commonDeviceNetworkStatus.GetPortByIfName(test.ifname)
		value, err := port.CountAddrsExceptLinkLocal()
		assert.Equal(t, test.expectedValue, value)
		if test.expectFail {
			assert.NotNil(t, err)
		} else {
			assert.Nil(t, err)
		}
	}
}

func TestCountAddrsExceptLinkLocalWithCost(t *testing.T) {
	testMatrix := map[string]struct {
		cost          uint8
		expectedValue int
	}{
		"Test 0 CountAddrsExceptLinkLocalWithCost": {
			cost:          0,
			expectedValue: 5,
		},
		"Test 16 CountAddrsExceptLinkLocalWithCost": {
			cost:          16,
			expectedValue: 5,
		},
		"Test 17 CountAddrsExceptLinkLocalWithCost": {
			cost:          17,
			expectedValue: 8,
		},
		"Test 255 CountAddrsExceptLinkLocalWithCost": {
			cost:          255,
			expectedValue: 8,
		},
	}
	for testname, test := range testMatrix {
		t.Logf("Running test case %s", testname)
		value := commonDeviceNetworkStatus.CountAddrsExceptLinkLocalWithCost(test.cost)
		assert.Equal(t, test.expectedValue, value)
	}
}

func TestCountIPv4AddrsExceptLinkLocal(t *testing.T) {
	testMatrix := map[string]struct {
		expectedValue int
	}{
		"Test CountIPv4AddrsExceptLinkLocal": {
			expectedValue: 5,
		},
	}
	for testname, test := range testMatrix {
		t.Logf("Running test case %s", testname)
		value := commonDeviceNetworkStatus.CountIPv4AddrsExceptLinkLocal()
		assert.Equal(t, test.expectedValue, value)
	}
}

func TestPortCountIPv4AddrsExceptLinkLocal(t *testing.T) {
	testMatrix := map[string]struct {
		ifname        string
		expectFail    bool
		expectedValue int
	}{
		"Test port1 CountIPv4AddrsExceptLinkLocal": {
			ifname:        "port1",
			expectedValue: 2,
		},
		"Test port2 CountIPv4AddrsExceptLinkLocal": {
			ifname:        "port2",
			expectedValue: 0,
			expectFail:    true,
		},
		"Test port3 CountIPv4AddrsExceptLinkLocal": {
			ifname:        "port3",
			expectedValue: 0,
			expectFail:    true,
		},
		"Test port4 CountIPv4AddrsExceptLinkLocal": {
			ifname:        "port4",
			expectedValue: 3,
		},
	}
	for testname, test := range testMatrix {
		t.Logf("Running test case %s", testname)
		port := commonDeviceNetworkStatus.GetPortByIfName(test.ifname)
		value, err := port.CountIPv4AddrsExceptLinkLocal()
		assert.Equal(t, test.expectedValue, value)
		if test.expectFail {
			assert.NotNil(t, err)
		} else {
			assert.Nil(t, err)
		}
	}
}

func TestPickAddrExceptLinkLocal(t *testing.T) {
	testMatrix := map[string]struct {
		pickNum       int
		expectedValue net.IP
		expectFail    bool
	}{
		"Test 0 PickAddrExceptLinkLocal": {
			pickNum:       0,
			expectedValue: addrIPv4Global4,
		},
		"Test 1 PickAddrExceptLinkLocal": {
			pickNum:       1,
			expectedValue: addrIPv6Global4,
		},
		"Test 2 PickAddrExceptLinkLocal": {
			pickNum:       2,
			expectedValue: addrIPv4Global3,
		},
		"Test 3 PickAddrExceptLinkLocal": {
			pickNum:       3,
			expectedValue: addrIPv6Global3,
		},
		"Test 7 PickAddrExceptLinkLocal": {
			pickNum:       7,
			expectedValue: addrIPv4Global5,
		},
		// Wrap around
		"Test 8 PickAddrExceptLinkLocal": {
			pickNum:       8,
			expectedValue: addrIPv4Global4,
		},
		"Test 9 PickAddrExceptLinkLocal": {
			pickNum:       9,
			expectedValue: addrIPv6Global4,
		},
	}
	for testname, test := range testMatrix {
		t.Logf("Running test case %s", testname)
		value, err := commonDeviceNetworkStatus.PickAddrExceptLinkLocal(test.pickNum)
		assert.Equal(t, test.expectedValue, value)
		if test.expectFail {
			assert.NotNil(t, err)
		} else {
			assert.Nil(t, err)
		}
	}
}

func TestPortPickAddrExceptLinkLocal(t *testing.T) {
	testMatrix := map[string]struct {
		ifname        string
		pickNum       int
		expectedValue net.IP
		expectFail    bool
	}{
		"Test port1 pick 0 PickAddrExceptLinkLocal": {
			ifname:        "port1",
			pickNum:       0,
			expectedValue: addrIPv4Global1,
		},
		"Test port1 pick 1 PickAddrExceptLinkLocal": {
			ifname:        "port1",
			pickNum:       1,
			expectedValue: addrIPv6Global1,
		},
		"Test port1 pick 2 PickAddrExceptLinkLocal": {
			ifname:        "port1",
			pickNum:       2,
			expectedValue: addrIPv4Global5,
		},
		// Wraparound
		"Test port1 pick 3 PickAddrExceptLinkLocal": {
			ifname:        "port1",
			pickNum:       3,
			expectedValue: addrIPv4Global1,
		},
		"Test port2 pick 0 PickAddrExceptLinkLocal": {
			ifname:        "port2",
			pickNum:       0,
			expectedValue: net.IP{},
			expectFail:    true,
		},
		"Test port3 pick 0 PickAddrExceptLinkLocal": {
			ifname:        "port3",
			pickNum:       0,
			expectedValue: net.IP{},
			expectFail:    true,
		},
		"Test port4 pick 0 PickAddrExceptLinkLocal": {
			ifname:        "port4",
			pickNum:       0,
			expectedValue: addrIPv4Global4,
		},
		"Test port4 pick 1 PickAddrExceptLinkLocal": {
			ifname:        "port4",
			pickNum:       1,
			expectedValue: addrIPv6Global4,
		},
		"Test port4 pick 2 PickAddrExceptLinkLocal": {
			ifname:        "port4",
			pickNum:       2,
			expectedValue: addrIPv4Global3,
		},
	}
	for testname, test := range testMatrix {
		t.Logf("Running test case %s", testname)
		port := commonDeviceNetworkStatus.GetPortByIfName(test.ifname)
		value, err := port.PickAddrExceptLinkLocal(test.pickNum)
		assert.Equal(t, test.expectedValue, value)
		if test.expectFail {
			assert.NotNil(t, err)
		} else {
			assert.Nil(t, err)
		}
	}
}

func TestPickAddrExceptLinkLocalWithCost(t *testing.T) {
	testMatrix := map[string]struct {
		pickNum       int
		cost          uint8
		expectedValue net.IP
		expectFail    bool
	}{
		"Test 0 cost 0 PickAddrExceptLinkLocalWithCost": {
			pickNum:       0,
			cost:          0,
			expectedValue: addrIPv4Global4,
		},
		"Test 1 cost 0 PickAddrExceptLinkLocalWithCost": {
			pickNum:       1,
			cost:          0,
			expectedValue: addrIPv6Global4,
		},
		"Test 2 cost 0 PickAddrExceptLinkLocalWithCost": {
			pickNum:       2,
			cost:          0,
			expectedValue: addrIPv4Global3,
		},
		"Test 3 cost 0 PickAddrExceptLinkLocalWithCost": {
			pickNum:       3,
			cost:          0,
			expectedValue: addrIPv6Global3,
		},
		// Wrap around
		"Test 7 cost 0 PickAddrExceptLinkLocalWithCost": {
			pickNum:       7,
			cost:          0,
			expectedValue: addrIPv4Global3,
		},
		"Test 8 cost 0 PickAddrExceptLinkLocalWithCost": {
			pickNum:       8,
			cost:          0,
			expectedValue: addrIPv6Global3,
		},
		"Test 9 cost 0 PickAddrExceptLinkLocalWithCost": {
			pickNum:       9,
			cost:          0,
			expectedValue: addrIPv4Global6,
		},
		"Test 0 cost 20 PickAddrExceptLinkLocalWithCost": {
			pickNum:       0,
			cost:          20,
			expectedValue: addrIPv4Global4,
		},
		"Test 1 cost 20 PickAddrExceptLinkLocalWithCost": {
			pickNum:       1,
			cost:          20,
			expectedValue: addrIPv6Global4,
		},
		"Test 2 cost 20 PickAddrExceptLinkLocalWithCost": {
			pickNum:       2,
			cost:          20,
			expectedValue: addrIPv4Global3,
		},
		"Test 3 cost 20 PickAddrExceptLinkLocalWithCost": {
			pickNum:       3,
			cost:          20,
			expectedValue: addrIPv6Global3,
		},
		"Test 7 cost 20 PickAddrExceptLinkLocalWithCost": {
			pickNum:       7,
			cost:          20,
			expectedValue: addrIPv4Global5,
		},
		// Wrap around
		"Test 8 cost 20 PickAddrExceptLinkLocalWithCost": {
			pickNum:       8,
			cost:          20,
			expectedValue: addrIPv4Global4,
		},
		"Test 9 cost 20 PickAddrExceptLinkLocalWithCost": {
			pickNum:       9,
			cost:          20,
			expectedValue: addrIPv6Global4,
		},
	}
	for testname, test := range testMatrix {
		t.Logf("Running test case %s", testname)
		value, err := commonDeviceNetworkStatus.PickAddrExceptLinkLocalWithCost(
			test.pickNum, test.cost)
		assert.Equal(t, test.expectedValue, value)
		if test.expectFail {
			assert.NotNil(t, err)
		} else {
			assert.Nil(t, err)
		}
	}
}
