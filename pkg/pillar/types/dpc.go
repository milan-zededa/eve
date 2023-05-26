// Copyright (c) 2023 Zededa, Inc.
// SPDX-License-Identifier: Apache-2.0

// DPC (Device Port Config) = configuration for physical network interfaces
// used for management or as app-shared (not app-direct).

package types

import (
	"fmt"
	"net"
	"os"
	"reflect"
	"time"

	"github.com/google/go-cmp/cmp"
	"github.com/lf-edge/eve/pkg/pillar/base"
	uuid "github.com/satori/go.uuid"
	"github.com/sirupsen/logrus"
)

type DevicePortConfigVersion uint32

// When new fields and/or new semantics are added to DevicePortConfig a new
// version value is added here.
const (
	DPCInitial DevicePortConfigVersion = iota
	DPCIsMgmt                          // Require IsMgmt to be set for management ports
)

// DPCState tracks the progression a DPC verification.
type DPCState uint8

const (
	// DPCStateNone : undefined state.
	DPCStateNone DPCState = iota
	// DPCStateFail : DPC verification failed.
	DPCStateFail
	// DPCStateFailWithIPAndDNS : failed to reach controller but has IP/DNS.
	DPCStateFailWithIPAndDNS
	// DPCStateSuccess : DPC verification succeeded.
	DPCStateSuccess
	// DPCStateIPDNSWait : waiting for interface IP address(es) and/or DNS server(s).
	DPCStateIPDNSWait
	// DPCStatePCIWait : waiting for some interface to come from pciback.
	DPCStatePCIWait
	// DPCStateIntfWait : waiting for some interface to appear in the network stack.
	DPCStateIntfWait
	// DPCStateRemoteWait : DPC verification failed because controller is down
	// or has old certificate.
	DPCStateRemoteWait
	// DPCStateAsyncWait : waiting for some config operations to finalize which are
	// running asynchronously in the background.
	DPCStateAsyncWait
)

// String returns the string name
func (status DPCState) String() string {
	switch status {
	case DPCStateNone:
		return ""
	case DPCStateFail:
		return "DPC_FAIL"
	case DPCStateFailWithIPAndDNS:
		return "DPC_FAIL_WITH_IPANDDNS"
	case DPCStateSuccess:
		return "DPC_SUCCESS"
	case DPCStateIPDNSWait:
		return "DPC_IPDNS_WAIT"
	case DPCStatePCIWait:
		return "DPC_PCI_WAIT"
	case DPCStateIntfWait:
		return "DPC_INTF_WAIT"
	case DPCStateRemoteWait:
		return "DPC_REMOTE_WAIT"
	case DPCStateAsyncWait:
		return "DPC_ASYNC_WAIT"
	default:
		return fmt.Sprintf("Unknown status %d", status)
	}
}

const (
	// PortCostMin is the lowest cost
	PortCostMin = uint8(0)
	// PortCostMax is the highest cost
	PortCostMax = uint8(255)
)

// L2LinkType - supported types of an L2 link
type L2LinkType uint8

const (
	// L2LinkTypeNone : not an L2 link (used for physical network adapters).
	L2LinkTypeNone L2LinkType = iota
	// L2LinkTypeVLAN : VLAN sub-interface
	L2LinkTypeVLAN
	// L2LinkTypeBond : Bond interface
	L2LinkTypeBond
)

// L2LinkConfig - contains either VLAN or Bond interface configuration,
// depending on the L2Type.
type L2LinkConfig struct {
	L2Type L2LinkType
	VLAN   VLANConfig
	Bond   BondConfig
}

// VLANConfig - VLAN sub-interface configuration.
type VLANConfig struct {
	// Logical name of the parent port.
	ParentPort string
	// VLAN ID.
	ID uint16
}

// BondMode specifies the policy indicating how bonding slaves are used
// during network transmissions.
type BondMode uint8

const (
	// BondModeUnspecified : default is Round-Robin
	BondModeUnspecified BondMode = iota
	// BondModeBalanceRR : Round-Robin
	BondModeBalanceRR
	// BondModeActiveBackup : Active/Backup
	BondModeActiveBackup
	// BondModeBalanceXOR : select slave for a packet using a hash function
	BondModeBalanceXOR
	// BondModeBroadcast : send every packet on all slaves
	BondModeBroadcast
	// BondMode802Dot3AD : IEEE 802.3ad Dynamic link aggregation
	BondMode802Dot3AD
	// BondModeBalanceTLB : Adaptive transmit load balancing
	BondModeBalanceTLB
	// BondModeBalanceALB : Adaptive load balancing
	BondModeBalanceALB
)

// LacpRate specifies the rate in which EVE will ask LACP link partners
// to transmit LACPDU packets in 802.3ad mode.
type LacpRate uint8

const (
	// LacpRateUnspecified : default is Slow.
	LacpRateUnspecified LacpRate = iota
	// LacpRateSlow : Request partner to transmit LACPDUs every 30 seconds.
	LacpRateSlow
	// LacpRateFast : Request partner to transmit LACPDUs every 1 second.
	LacpRateFast
)

// BondConfig - Bond (LAG) interface configuration.
type BondConfig struct {
	// Logical names of PhysicalIO network adapters aggregated by this bond.
	AggregatedPorts []string

	// Bonding policy.
	Mode BondMode

	// LACPDU packets transmission rate.
	// Applicable for BondMode802Dot3AD only.
	LacpRate LacpRate

	// Link monitoring is either disabled or one of the monitors
	// is enabled, never both at the same time.
	MIIMonitor BondMIIMonitor
	ARPMonitor BondArpMonitor
}

// BondMIIMonitor : MII link monitoring parameters (see devmodel.proto for description).
type BondMIIMonitor struct {
	Enabled   bool
	Interval  uint32
	UpDelay   uint32
	DownDelay uint32
}

// BondArpMonitor : ARP-based link monitoring parameters (see devmodel.proto for description).
type BondArpMonitor struct {
	Enabled   bool
	Interval  uint32
	IPTargets []net.IP
}

type NetworkProxyType uint8

// Values if these definitions should match the values
// given to the types in zapi.ProxyProto
const (
	NPT_HTTP NetworkProxyType = iota
	NPT_HTTPS
	NPT_SOCKS
	NPT_FTP
	NPT_NOPROXY
	NPT_LAST = 255
)

// WifiKeySchemeType - types of key management
type WifiKeySchemeType uint8

// Key Scheme type
const (
	KeySchemeNone WifiKeySchemeType = iota // enum for key scheme
	KeySchemeWpaPsk
	KeySchemeWpaEap
	KeySchemeOther
)

// WirelessType - types of wireless media
type WirelessType uint8

// enum wireless type
const (
	WirelessTypeNone WirelessType = iota // enum for wireless type
	WirelessTypeCellular
	WirelessTypeWifi
)

type ProxyEntry struct {
	Type   NetworkProxyType `json:"type"`
	Server string           `json:"server"`
	Port   uint32           `json:"port"`
}

type ProxyConfig struct {
	Proxies    []ProxyEntry
	Exceptions string
	Pacfile    string
	// If Enable is set we use WPAD. If the URL is not set we try
	// the various DNS suffixes until we can download a wpad.dat file
	NetworkProxyEnable bool   // Enable WPAD
	NetworkProxyURL    string // Complete URL i.e., with /wpad.dat
	WpadURL            string // The URL determined from DNS
	// List of certs which will be added to TLS trust
	ProxyCertPEM [][]byte `json:"pubsub-large-ProxyCertPEM"`
}

type DhcpType uint8

const (
	DT_NOOP       DhcpType = iota
	DT_STATIC              // Device static config
	DT_NONE                // App passthrough e.g., to a bridge
	DT_Deprecated          // XXX to match .proto value
	DT_CLIENT              // Device client on external port
)

type NetworkType uint8

const (
	NT_NOOP NetworkType = 0
	NT_IPV4             = 4
	NT_IPV6             = 6

	// EVE has been running with Dual stack DHCP behavior with both IPv4 & IPv6 specific networks.
	// There can be users who are currently benefitting from this behavior.
	// It makes sense to introduce two new types IPv4_ONLY & IPv6_ONLY and allow
	// the same family selection from UI for the use cases where only one of the IP families
	// is required on management/app-shared adapters.

	// NtIpv4Only : IPv4 addresses only
	NtIpv4Only = 5
	// NtIpv6Only : IPv6 addresses only
	NtIpv6Only = 7
	// NtDualStack : Run with dual stack
	NtDualStack = 8
)

type DhcpConfig struct {
	Dhcp       DhcpType // If DT_STATIC use below; if DT_NONE do nothing
	AddrSubnet string   // In CIDR e.g., 192.168.1.44/24
	Gateway    net.IP
	DomainName string
	NtpServer  net.IP
	DnsServers []net.IP    // If not set we use Gateway as DNS server
	Type       NetworkType // IPv4 or IPv6 or Dual stack
}

// WifiConfig - Wifi structure
type WifiConfig struct {
	SSID      string            // wifi SSID
	KeyScheme WifiKeySchemeType // such as WPA-PSK, WPA-EAP

	// XXX: to be deprecated, use CipherBlockStatus instead
	Identity string // identity or username for WPA-EAP

	// XXX: to be deprecated, use CipherBlockStatus instead
	Password string // string of pass phrase or password hash
	Priority int32

	// CipherBlockStatus, for encrypted credentials
	CipherBlockStatus
}

// CellNetPortConfig - configuration for cellular network port (part of DPC).
type CellNetPortConfig struct {
	// Parameters to apply for connecting to cellular networks.
	// Configured separately for every SIM card inserted into the modem.
	AccessPoints []CellularAccessPoint
	// Probe used to detect broken connection.
	Probe WwanProbe
	// Enable to get location info from the GNSS receiver of the cellular modem.
	LocationTracking bool
}

// CellularAccessPoint contains config parameters for connecting to a cellular network.
type CellularAccessPoint struct {
	// SIM card slot to which this configuration applies.
	// 0 - unspecified (apply to currently activated or the only available)
	// 1 - config for SIM card in the first slot
	// 2 - config for SIM card in the second slot
	// etc.
	SIMSlot uint8
	// If true, then this configuration is currently activated.
	Activated bool
	// Access Point Network
	APN string
	// Authentication protocol used by the network.
	AuthProtocol WwanAuthProtocol
	// CipherBlockStatus with encrypted credentials.
	CipherBlockStatus
	// The set of cellular network operators that modem should preferably try to register
	// and connect into.
	// Network operator should be referenced by PLMN (Public Land Mobile Network) code.
	PreferredPLMNs []string
	// The list of preferred Radio Access Technologies (RATs) to use for connecting
	// to the network.
	PreferredRATs []WwanRAT
	// If true, then modem will avoid connecting to networks with roaming.
	ForbidRoaming bool
}

// WirelessConfig - wireless structure
type WirelessConfig struct {
	WType    WirelessType      // Wireless Type
	Cellular CellNetPortConfig // Cellular connectivity config params
	Wifi     []WifiConfig      // Wifi Config params
}

// NetworkPortConfig has the configuration and some status like TestResults
// for one physical network interface.
// XXX odd to have ParseErrors and/or TestResults here but we don't have
// a corresponding Status struct.
// Note that if fields are added the MostlyEqual function needs to be updated.
type NetworkPortConfig struct {
	IfName       string
	USBAddr      string
	PCIAddr      string
	Phylabel     string // Physical name set by controller/model
	Logicallabel string // SystemAdapter's name which is logical label in phyio
	Alias        string // From SystemAdapter's alias
	// NetworkUUID - UUID of the Network Object configured for the port.
	NetworkUUID uuid.UUID
	IsMgmt      bool  // Used to talk to controller
	IsL3Port    bool  // True if port is applicable to operate on the network layer
	Cost        uint8 // Zero is free
	DhcpConfig
	ProxyConfig
	L2LinkConfig
	WirelessCfg WirelessConfig
	// TestResults - Errors from parsing plus success/failure from testing
	TestResults
}

// DevicePortConfig is a misnomer in that it includes the total test results
// plus the test results for a given port. The complete status with
// IP addresses lives in DeviceNetworkStatus
type DevicePortConfig struct {
	Version      DevicePortConfigVersion
	Key          string
	TimePriority time.Time // All zero's is fallback lowest priority
	State        DPCState
	ShaFile      string // File in which to write ShaValue once DevicePortConfigList published
	ShaValue     []byte
	TestResults
	LastIPAndDNS time.Time // Time when we got some IP addresses and DNS

	Ports []NetworkPortConfig
}

// PubKey is used for pubsub. Key string plus TimePriority
func (config DevicePortConfig) PubKey() string {
	return config.Key + "@" + config.TimePriority.UTC().Format(time.RFC3339Nano)
}

// LogCreate :
func (config DevicePortConfig) LogCreate(logBase *base.LogObject) {
	logObject := base.NewLogObject(logBase, base.DevicePortConfigLogType, "",
		nilUUID, config.LogKey())
	if logObject == nil {
		return
	}
	logObject.CloneAndAddField("ports-int64", len(config.Ports)).
		AddField("last-failed", config.LastFailed).
		AddField("last-succeeded", config.LastSucceeded).
		AddField("last-error", config.LastError).
		AddField("state", config.State.String()).
		Noticef("DevicePortConfig create")
	for _, p := range config.Ports {
		// XXX different logobject for a particular port?
		logObject.CloneAndAddField("ifname", p.IfName).
			AddField("logical-label", p.Logicallabel).
			AddField("last-error", p.LastError).
			AddField("last-succeeded", p.LastSucceeded).
			AddField("last-failed", p.LastFailed).
			Noticef("DevicePortConfig port create")
	}
}

// LogModify :
func (config DevicePortConfig) LogModify(logBase *base.LogObject, old interface{}) {
	logObject := base.EnsureLogObject(logBase, base.DevicePortConfigLogType, "",
		nilUUID, config.LogKey())

	oldConfig, ok := old.(DevicePortConfig)
	if !ok {
		logObject.Clone().Fatalf("LogModify: Old object interface passed is not of DevicePortConfig type")
	}
	if len(oldConfig.Ports) != len(config.Ports) ||
		oldConfig.LastFailed != config.LastFailed ||
		oldConfig.LastSucceeded != config.LastSucceeded ||
		oldConfig.LastError != config.LastError ||
		oldConfig.State != config.State {

		logData := logObject.CloneAndAddField("ports-int64", len(config.Ports)).
			AddField("last-failed", config.LastFailed).
			AddField("last-succeeded", config.LastSucceeded).
			AddField("last-error", config.LastError).
			AddField("state", config.State.String()).
			AddField("old-ports-int64", len(oldConfig.Ports)).
			AddField("old-last-failed", oldConfig.LastFailed).
			AddField("old-last-succeeded", oldConfig.LastSucceeded).
			AddField("old-last-error", oldConfig.LastError).
			AddField("old-state", oldConfig.State.String())
		if len(oldConfig.Ports) == len(config.Ports) &&
			config.LastFailed == oldConfig.LastFailed &&
			config.LastError == oldConfig.LastError &&
			oldConfig.State == config.State &&
			config.LastSucceeded.After(oldConfig.LastFailed) &&
			oldConfig.LastSucceeded.After(oldConfig.LastFailed) {
			// if we have success again, reduce log level
			logData.Function("DevicePortConfig port modify")
		} else {
			logData.Notice("DevicePortConfig port modify")
		}
	}
	// XXX which fields to compare/log?
	for i, p := range config.Ports {
		if len(oldConfig.Ports) <= i {
			continue
		}
		op := oldConfig.Ports[i]
		// XXX different logobject for a particular port?
		if p.HasError() != op.HasError() ||
			p.LastFailed != op.LastFailed ||
			p.LastSucceeded != op.LastSucceeded ||
			p.LastError != op.LastError {
			logData := logObject.CloneAndAddField("ifname", p.IfName).
				AddField("logical-label", p.Logicallabel).
				AddField("last-error", p.LastError).
				AddField("last-succeeded", p.LastSucceeded).
				AddField("last-failed", p.LastFailed).
				AddField("old-last-error", op.LastError).
				AddField("old-last-succeeded", op.LastSucceeded).
				AddField("old-last-failed", op.LastFailed)
			if p.HasError() == op.HasError() &&
				p.LastFailed == op.LastFailed &&
				p.LastError == op.LastError &&
				p.LastSucceeded.After(op.LastFailed) &&
				op.LastSucceeded.After(op.LastFailed) {
				// if we have success again, reduce log level
				logData.Function("DevicePortConfig port modify")
			} else {
				logData.Notice("DevicePortConfig port modify")
			}
		}
	}
}

// LogDelete :
func (config DevicePortConfig) LogDelete(logBase *base.LogObject) {
	logObject := base.EnsureLogObject(logBase, base.DevicePortConfigLogType, "",
		nilUUID, config.LogKey())
	logObject.CloneAndAddField("ports-int64", len(config.Ports)).
		AddField("last-failed", config.LastFailed).
		AddField("last-succeeded", config.LastSucceeded).
		AddField("last-error", config.LastError).
		AddField("state", config.State.String()).
		Noticef("DevicePortConfig delete")
	for _, p := range config.Ports {
		// XXX different logobject for a particular port?
		logObject.CloneAndAddField("ifname", p.IfName).
			AddField("logical-label", p.Logicallabel).
			AddField("last-error", p.LastError).
			AddField("last-succeeded", p.LastSucceeded).
			AddField("last-failed", p.LastFailed).
			Noticef("DevicePortConfig port delete")
	}

	base.DeleteLogObject(logBase, config.LogKey())
}

// LogKey :
func (config DevicePortConfig) LogKey() string {
	return string(base.DevicePortConfigLogType) + "-" + config.PubKey()
}

// GetPortByIfName - get pointer to port with the given interface name.
func (config *DevicePortConfig) GetPortByIfName(ifname string) *NetworkPortConfig {
	for indx := range config.Ports {
		portPtr := &config.Ports[indx]
		if ifname == portPtr.IfName {
			return portPtr
		}
	}
	return nil
}

// GetPortByLogicalLabel - get pointer to port with the given logical label.
// Shared labels are not supported by this function (consider using GetPortsByLogicallabel
// from DeviceNetworkStatus instead).
func (config *DevicePortConfig) GetPortByLogicalLabel(label string) *NetworkPortConfig {
	for indx := range config.Ports {
		portPtr := &config.Ports[indx]
		if label == portPtr.Logicallabel {
			return portPtr
		}
	}
	return nil
}

// RecordPortSuccess - Record success for a given port.
func (config *DevicePortConfig) RecordPortSuccess(logicalLabel string) {
	portPtr := config.GetPortByLogicalLabel(logicalLabel)
	if portPtr != nil {
		portPtr.RecordSuccess()
	}
}

// RecordPortFailure - Record failure for a given port.
func (config *DevicePortConfig) RecordPortFailure(logicalLabel string, errStr string) {
	portPtr := config.GetPortByLogicalLabel(logicalLabel)
	if portPtr != nil {
		portPtr.RecordFailure(errStr)
	}
}

// DoSanitize -
func (config *DevicePortConfig) DoSanitize(log *base.LogObject,
	sanitizeTimePriority bool, sanitizeKey bool, key string,
	sanitizeName, sanitizeL3Port bool) {

	if sanitizeTimePriority {
		zeroTime := time.Time{}
		if config.TimePriority == zeroTime {
			// A json override file should really contain a
			// timepriority field so we can determine whether
			// it or the information received from the controller
			// is more current.
			// If we can stat the file we use 1980, otherwise
			// we use 1970; using the modify time of the file
			// is too unpredictable.
			_, err1 := os.Stat(fmt.Sprintf("%s/DevicePortConfig/%s.json",
				TmpDirname, key))
			_, err2 := os.Stat(fmt.Sprintf("%s/DevicePortConfig/%s.json",
				IdentityDirname, key))
			if err1 == nil || err2 == nil {
				config.TimePriority = time.Date(1980,
					time.January, 1, 0, 0, 0, 0, time.UTC)
			} else {
				config.TimePriority = time.Date(1970,
					time.January, 1, 0, 0, 0, 0, time.UTC)
			}
			log.Warnf("DoSanitize: Forcing TimePriority for %s to %v",
				key, config.TimePriority)
		}
	}
	if sanitizeKey {
		if config.Key == "" {
			config.Key = key
			log.Noticef("DoSanitize: Forcing Key for %s TS %v\n",
				key, config.TimePriority)
		}
	}
	if sanitizeName {
		// In case Phylabel isn't set we make it match IfName. Ditto for Logicallabel
		// Only needed to handle upgrades from very old versions.
		for i := range config.Ports {
			port := &config.Ports[i]
			if port.Phylabel == "" && port.IfName != "" {
				port.Phylabel = port.IfName
				log.Functionf("XXX DoSanitize: Forcing Phylabel for %s ifname %s\n",
					key, port.IfName)
			}
			if port.Logicallabel == "" && port.IfName != "" {
				port.Logicallabel = port.IfName
				log.Functionf("XXX DoSanitize: Forcing Logicallabel for %s ifname %s\n",
					key, port.IfName)
			}
		}
	}
	if sanitizeL3Port {
		// IsL3Port flag was introduced to NetworkPortConfig in 7.3.0
		// It is used to differentiate between L3 ports (with IP/DNS config)
		// and intermediate L2-only ports (bond slaves, VLAN parents, etc.).
		// Before 7.3.0, EVE didn't support L2-only adapters and all uplink ports
		// were L3 endpoints.
		// However, even with VLANs and bonds there has to be at least one L3
		// port (L2 adapters are only intermediates with L3 endpoint(s) at the top).
		// This means that to support upgrade from older EVE versions,
		// we can simply check if there is at least one L3 port, and if not, it means
		// that we are dealing with an older persisted/override DPC, where all
		// ports should be marked as L3.
		var hasL3Port bool
		for _, port := range config.Ports {
			hasL3Port = hasL3Port || port.IsL3Port
		}
		if !hasL3Port {
			for i := range config.Ports {
				config.Ports[i].IsL3Port = true
			}
		}
	}
}

// CountMgmtPorts returns the number of management ports
// Exclude any broken ones with Dhcp = DT_NONE
func (config *DevicePortConfig) CountMgmtPorts() int {

	count := 0
	for _, port := range config.Ports {
		if port.IsMgmt && port.Dhcp != DT_NONE {
			count++
		}
	}
	return count
}

// MostlyEqual compares two DevicePortConfig but skips things that are
// more of status such as the timestamps and the TestResults
// XXX Compare Version or not?
// We compare the Ports in array order.
func (config *DevicePortConfig) MostlyEqual(config2 *DevicePortConfig) bool {

	if config.Key != config2.Key {
		return false
	}
	if len(config.Ports) != len(config2.Ports) {
		return false
	}
	for i, p1 := range config.Ports {
		p2 := config2.Ports[i]
		if p1.IfName != p2.IfName ||
			p1.USBAddr != p2.USBAddr ||
			p1.PCIAddr != p2.PCIAddr ||
			p1.Phylabel != p2.Phylabel ||
			p1.Logicallabel != p2.Logicallabel ||
			p1.Alias != p2.Alias ||
			p1.IsMgmt != p2.IsMgmt ||
			p1.Cost != p2.Cost {
			return false
		}
		if !reflect.DeepEqual(p1.DhcpConfig, p2.DhcpConfig) ||
			!reflect.DeepEqual(p1.ProxyConfig, p2.ProxyConfig) ||
			!reflect.DeepEqual(p1.WirelessCfg, p2.WirelessCfg) {
			return false
		}
	}
	return true
}

// IsDPCTestable - Return false if recent failure (less than "minTimeSinceFailure")
// Also returns false if it isn't usable
func (config DevicePortConfig) IsDPCTestable(minTimeSinceFailure time.Duration) bool {
	if !config.IsDPCUsable() {
		return false
	}
	if config.LastFailed.IsZero() {
		return true
	}
	if config.LastSucceeded.After(config.LastFailed) {
		return true
	}
	return time.Since(config.LastFailed) >= minTimeSinceFailure
}

// IsDPCUntested - returns true if this is something we might want to test now.
// Checks if it is Usable since there is no point in testing unusable things.
func (config DevicePortConfig) IsDPCUntested() bool {
	if config.LastFailed.IsZero() && config.LastSucceeded.IsZero() &&
		config.IsDPCUsable() {
		return true
	}
	return false
}

// IsDPCUsable - checks whether something is invalid; no management IP
// addresses means it isn't usable hence we return false if none.
func (config DevicePortConfig) IsDPCUsable() bool {
	mgmtCount := config.CountMgmtPorts()
	return mgmtCount > 0
}

// WasDPCWorking - Check if the last results for the DPC was Success
func (config DevicePortConfig) WasDPCWorking() bool {

	if config.LastSucceeded.IsZero() {
		return false
	}
	if config.LastSucceeded.After(config.LastFailed) {
		return true
	}
	return false
}

// UpdatePortStatusFromIntfStatusMap - Set TestResults for ports in DevicePortConfig to
// those from intfStatusMap. If a port is not found in intfStatusMap, it means
// the port was not tested, so we retain the original TestResults for the port.
func (config *DevicePortConfig) UpdatePortStatusFromIntfStatusMap(
	intfStatusMap IntfStatusMap) {
	for indx := range config.Ports {
		portPtr := &config.Ports[indx]
		tr, ok := intfStatusMap.StatusMap[portPtr.Logicallabel]
		if ok {
			portPtr.TestResults.Update(tr)
		}
		// Else - Port not tested hence no change
	}
}

// IsAnyPortInPciBack
//
//	Checks if any of the Ports are part of IO bundles which are in PCIback.
//	If true, it also returns the port logical label.
//	Also returns whether it is currently used by an application by
//	returning a UUID. If the UUID is zero it is in PCIback but available.
//	Use filterUnassigned to filter out unassigned ports.
func (config *DevicePortConfig) IsAnyPortInPciBack(
	log *base.LogObject, aa *AssignableAdapters, filterUnassigned bool) (bool, string, uuid.UUID) {
	if aa == nil {
		log.Functionf("IsAnyPortInPciBack: nil aa")
		return false, "", uuid.UUID{}
	}
	log.Functionf("IsAnyPortInPciBack: aa init %t, %d bundles, %d ports",
		aa.Initialized, len(aa.IoBundleList), len(config.Ports))
	for _, port := range config.Ports {
		ioBundle := aa.LookupIoBundleLogicallabel(port.Logicallabel)
		if ioBundle == nil {
			// It is not guaranteed that all Ports are part of Assignable Adapters
			// If not found, the adapter is not capable of being assigned at
			// PCI level. So it cannot be in PCI back.
			log.Functionf("IsAnyPortInPciBack: port %s not found",
				port.Logicallabel)
			continue
		}
		if ioBundle.IsPCIBack && (!filterUnassigned || ioBundle.UsedByUUID != nilUUID) {
			return true, port.Logicallabel, ioBundle.UsedByUUID
		}
	}
	return false, "", uuid.UUID{}
}

// GetPortsWithoutIfName returns logical labels of ports defined without interface name.
func (config *DevicePortConfig) GetPortsWithoutIfName() (ports []string) {
	for _, port := range config.Ports {
		if port.IfName == "" {
			ports = append(ports, port.Logicallabel)
		}
	}
	return ports
}

// DevicePortConfigList is an array in timestamp aka priority order;
// first one is the most desired config to use
// It includes test results hence is misnamed - should have a separate status
// This is only published under the key "global"
type DevicePortConfigList struct {
	CurrentIndex   int
	PortConfigList []DevicePortConfig
}

// MostlyEqual - Equal if everything else other than timestamps is equal.
func (config DevicePortConfigList) MostlyEqual(config2 DevicePortConfigList) bool {

	if len(config.PortConfigList) != len(config2.PortConfigList) {
		return false
	}
	if config.CurrentIndex != config2.CurrentIndex {
		return false
	}
	for i, c1 := range config.PortConfigList {
		c2 := config2.PortConfigList[i]

		if !c1.MostlyEqual(&c2) || c1.State != c2.State {
			return false
		}
	}
	return true
}

// PubKey is used for pubsub
func (config DevicePortConfigList) PubKey() string {
	return "global"
}

// LogCreate :
func (config DevicePortConfigList) LogCreate(logBase *base.LogObject) {
	logObject := base.NewLogObject(logBase, base.DevicePortConfigListLogType, "",
		nilUUID, config.LogKey())
	if logObject == nil {
		return
	}
	logObject.CloneAndAddField("current-index-int64", config.CurrentIndex).
		AddField("num-portconfig-int64", len(config.PortConfigList)).
		Noticef("DevicePortConfigList create")
}

// LogModify :
func (config DevicePortConfigList) LogModify(logBase *base.LogObject, old interface{}) {
	logObject := base.EnsureLogObject(logBase, base.DevicePortConfigListLogType, "",
		nilUUID, config.LogKey())

	oldConfig, ok := old.(DevicePortConfigList)
	if !ok {
		logObject.Clone().Errorf("LogModify: Old object interface passed is not of DevicePortConfigList type")
		return
	}
	if oldConfig.CurrentIndex != config.CurrentIndex ||
		len(oldConfig.PortConfigList) != len(config.PortConfigList) {

		logObject.CloneAndAddField("current-index-int64", config.CurrentIndex).
			AddField("num-portconfig-int64", len(config.PortConfigList)).
			AddField("old-current-index-int64", oldConfig.CurrentIndex).
			AddField("old-num-portconfig-int64", len(oldConfig.PortConfigList)).
			Noticef("DevicePortConfigList modify")
	} else {
		// Log at Trace level - most likely just a timestamp change
		logObject.CloneAndAddField("diff", cmp.Diff(oldConfig, config)).
			Tracef("DevicePortConfigList modify other change")
	}

}

// LogDelete :
func (config DevicePortConfigList) LogDelete(logBase *base.LogObject) {
	logObject := base.EnsureLogObject(logBase, base.DevicePortConfigListLogType, "",
		nilUUID, config.LogKey())
	logObject.CloneAndAddField("current-index-int64", config.CurrentIndex).
		AddField("num-portconfig-int64", len(config.PortConfigList)).
		Noticef("DevicePortConfigList delete")

	base.DeleteLogObject(logBase, config.LogKey())
}

// LogKey :
func (config DevicePortConfigList) LogKey() string {
	return string(base.DevicePortConfigListLogType) + "-" + config.PubKey()
}

// IntfStatusMap - Used to return per-interface test results (success and failures).
// Network interfaces are referenced by logical labels.
type IntfStatusMap struct {
	// StatusMap -> Key: port logical label, Value: TestResults
	StatusMap map[string]TestResults
}

// RecordSuccess records a success for the ifName
func (intfMap *IntfStatusMap) RecordSuccess(logicalLabel string) {
	tr, ok := intfMap.StatusMap[logicalLabel]
	if !ok {
		tr = TestResults{}
	}
	tr.RecordSuccess()
	intfMap.StatusMap[logicalLabel] = tr
}

// RecordFailure records a failure for the given network interface.
func (intfMap *IntfStatusMap) RecordFailure(logicalLabel string, errStr string) {
	tr, ok := intfMap.StatusMap[logicalLabel]
	if !ok {
		tr = TestResults{}
	}
	tr.RecordFailure(errStr)
	intfMap.StatusMap[logicalLabel] = tr
}

// SetOrUpdateFromMap - Set all the entries from the given per-interface map
// Entries which are not in the source are not modified
func (intfMap *IntfStatusMap) SetOrUpdateFromMap(source IntfStatusMap) {
	for intf, src := range source.StatusMap {
		tr, ok := intfMap.StatusMap[intf]
		if !ok {
			tr = TestResults{}
		}
		tr.Update(src)
		intfMap.StatusMap[intf] = tr
	}
}

// NewIntfStatusMap - Create a new instance of IntfStatusMap
func NewIntfStatusMap() *IntfStatusMap {
	intfStatusMap := IntfStatusMap{}
	intfStatusMap.StatusMap = make(map[string]TestResults)
	return &intfStatusMap
}

// TestResults is used to record when some test Failed or Succeeded.
// All zeros timestamps means it was never tested.
type TestResults struct {
	LastFailed    time.Time
	LastSucceeded time.Time
	LastError     string // Set when LastFailed is updated
}

// RecordSuccess records a success
// Keeps the LastFailed in place as history
func (trPtr *TestResults) RecordSuccess() {
	trPtr.LastSucceeded = time.Now()
	trPtr.LastError = ""
}

// RecordFailure records a failure
// Keeps the LastSucceeded in place as history
func (trPtr *TestResults) RecordFailure(errStr string) {
	if errStr == "" {
		logrus.Fatal("Missing error string")
	}
	trPtr.LastFailed = time.Now()
	trPtr.LastError = errStr
}

// HasError returns true if there is an error
// Returns false if it was never tested i.e., both timestamps zero
func (trPtr *TestResults) HasError() bool {
	return trPtr.LastFailed.After(trPtr.LastSucceeded)
}

// Update uses the src to add info to the results
// If src has newer information for the 'other' part we update that as well.
func (trPtr *TestResults) Update(src TestResults) {
	if src.HasError() {
		trPtr.LastFailed = src.LastFailed
		trPtr.LastError = src.LastError
		if src.LastSucceeded.After(trPtr.LastSucceeded) {
			trPtr.LastSucceeded = src.LastSucceeded
		}
	} else {
		trPtr.LastSucceeded = src.LastSucceeded
		trPtr.LastError = ""
		if src.LastFailed.After(trPtr.LastFailed) {
			trPtr.LastFailed = src.LastFailed
		}
	}
}

// Clear test results.
func (trPtr *TestResults) Clear() {
	trPtr.LastFailed = time.Time{}
	trPtr.LastSucceeded = time.Time{}
	trPtr.LastError = ""
}
