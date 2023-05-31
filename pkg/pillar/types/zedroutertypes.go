// Copyright (c) 2017-2021 Zededa, Inc.
// SPDX-License-Identifier: Apache-2.0

package types

import (
	"bytes"
	"encoding/binary"
	"net"
	"time"

	"github.com/google/go-cmp/cmp"
	"github.com/lf-edge/eve/pkg/pillar/base"
	uuid "github.com/satori/go.uuid"
)

// At the MinSubnetSize there is room for one app instance (.0 being reserved,
// .3 broadcast, .1 is the bridgeIPAddr, and .2 is usable).
const (
	MinSubnetSize   = 4  // minimum Subnet Size
	LargeSubnetSize = 16 // for determining default Dhcp Range
)

// AppNetworkConfig describes network configuration for a single app.
type AppNetworkConfig struct {
	UUIDandVersion      UUIDandVersion
	DisplayName         string
	Activate            bool
	GetStatsIPAddr      net.IP
	UnderlayNetworkList []UnderlayNetworkConfig
	CloudInitUserData   *string `json:"pubsub-large-CloudInitUserData"`
	CipherBlockStatus   CipherBlockStatus
	MetaDataType        MetaDataType
}

func (config AppNetworkConfig) Key() string {
	return config.UUIDandVersion.UUID.String()
}

// LogCreate :
func (config AppNetworkConfig) LogCreate(logBase *base.LogObject) {
	logObject := base.NewLogObject(logBase, base.AppNetworkConfigLogType, config.DisplayName,
		config.UUIDandVersion.UUID, config.LogKey())
	if logObject == nil {
		return
	}
	logObject.CloneAndAddField("activate", config.Activate).
		Noticef("App network config create")
}

// LogModify :
func (config AppNetworkConfig) LogModify(logBase *base.LogObject, old interface{}) {
	logObject := base.EnsureLogObject(logBase, base.AppNetworkConfigLogType, config.DisplayName,
		config.UUIDandVersion.UUID, config.LogKey())

	oldConfig, ok := old.(AppNetworkConfig)
	if !ok {
		logObject.Clone().Fatalf("LogModify: Old object interface passed is not of AppNetworkConfig type")
	}
	if oldConfig.Activate != config.Activate {

		logObject.CloneAndAddField("activate", config.Activate).
			AddField("old-activate", oldConfig.Activate).
			Noticef("App network config modify")
	} else {
		// Log at Function level
		logObject.CloneAndAddField("diff", cmp.Diff(oldConfig, config)).
			Functionf("App network config modify other change")
	}
}

// LogDelete :
func (config AppNetworkConfig) LogDelete(logBase *base.LogObject) {
	logObject := base.EnsureLogObject(logBase, base.AppNetworkConfigLogType, config.DisplayName,
		config.UUIDandVersion.UUID, config.LogKey())
	logObject.CloneAndAddField("activate", config.Activate).
		Noticef("App network config delete")

	base.DeleteLogObject(logBase, config.LogKey())
}

// LogKey :
func (config AppNetworkConfig) LogKey() string {
	return string(base.AppNetworkConfigLogType) + "-" + config.Key()
}

func (config *AppNetworkConfig) getUnderlayConfig(
	network uuid.UUID) *UnderlayNetworkConfig {
	for i := range config.UnderlayNetworkList {
		ulConfig := &config.UnderlayNetworkList[i]
		if ulConfig.Network == network {
			return ulConfig
		}
	}
	return nil
}

func (config *AppNetworkConfig) IsNetworkUsed(network uuid.UUID) bool {
	ulConfig := config.getUnderlayConfig(network)
	if ulConfig != nil {
		return true
	}
	// Network UUID matching neither UL nor OL network
	return false
}

func (status AppNetworkStatus) Pending() bool {
	return status.PendingAdd || status.PendingModify || status.PendingDelete
}

// AwaitingNetwork - Is the app waiting for network?
func (status AppNetworkStatus) AwaitingNetwork() bool {
	return status.AwaitNetworkInstance
}

// GetULStatusForNI returns UnderlayNetworkStatus for every application VIF
// connected to the given network instance (there can be multiple interfaces connected
// to the same network instance).
func (status AppNetworkStatus) GetULStatusForNI(netUUID uuid.UUID) []*UnderlayNetworkStatus {
	var uls []*UnderlayNetworkStatus
	for i := range status.UnderlayNetworkList {
		ul := &status.UnderlayNetworkList[i]
		if ul.Network == netUUID {
			uls = append(uls, ul)
		}
	}
	return uls
}

// AppNetworkStatus describes status of network connectivity for a single app.
type AppNetworkStatus struct {
	UUIDandVersion UUIDandVersion
	AppNum         int
	Activated      bool
	PendingAdd     bool
	PendingModify  bool
	PendingDelete  bool
	DisplayName    string
	// Copy from the AppNetworkConfig; used to delete when config is gone.
	GetStatsIPAddr       net.IP
	UnderlayNetworkList  []UnderlayNetworkStatus
	AwaitNetworkInstance bool // If any Missing flag is set in the networks
	// Any errors from provisioning the network
	// ErrorAndTime provides SetErrorNow() and ClearError()
	ErrorAndTime
}

func (status AppNetworkStatus) Key() string {
	return status.UUIDandVersion.UUID.String()
}

// LogCreate :
func (status AppNetworkStatus) LogCreate(logBase *base.LogObject) {
	logObject := base.NewLogObject(logBase, base.AppNetworkStatusLogType, status.DisplayName,
		status.UUIDandVersion.UUID, status.LogKey())
	if logObject == nil {
		return
	}
	logObject.CloneAndAddField("activated", status.Activated).
		Noticef("App network status create")
}

// LogModify :
func (status AppNetworkStatus) LogModify(logBase *base.LogObject, old interface{}) {
	logObject := base.EnsureLogObject(logBase, base.AppNetworkStatusLogType, status.DisplayName,
		status.UUIDandVersion.UUID, status.LogKey())

	oldStatus, ok := old.(AppNetworkStatus)
	if !ok {
		logObject.Clone().Fatalf("LogModify: Old object interface passed is not of AppNetworkStatus type")
	}
	if oldStatus.Activated != status.Activated {

		logObject.CloneAndAddField("activated", status.Activated).
			AddField("old-activated", oldStatus.Activated).
			Noticef("App network status modify")
	} else {
		// Log at Function level
		logObject.CloneAndAddField("diff", cmp.Diff(oldStatus, status)).
			Functionf("App network status modify other change")
	}

	if status.HasError() {
		errAndTime := status.ErrorAndTime
		logObject.CloneAndAddField("activated", status.Activated).
			AddField("error", errAndTime.Error).
			AddField("error-time", errAndTime.ErrorTime).
			Noticef("App network status modify")
	}
}

// LogDelete :
func (status AppNetworkStatus) LogDelete(logBase *base.LogObject) {
	logObject := base.EnsureLogObject(logBase, base.AppNetworkStatusLogType, status.DisplayName,
		status.UUIDandVersion.UUID, status.LogKey())
	logObject.CloneAndAddField("activated", status.Activated).
		Noticef("App network status delete")

	base.DeleteLogObject(logBase, status.LogKey())
}

// LogKey :
func (status AppNetworkStatus) LogKey() string {
	return string(base.AppNetworkStatusLogType) + "-" + status.Key()
}

// AppContainerMetrics - metrics for container deployed inside an app.
type AppContainerMetrics struct {
	UUIDandVersion UUIDandVersion // App UUID
	// Stats Collection time for uploading stats to cloud
	CollectTime time.Time
	StatsList   []AppContainerStats
}

// AppContainerStats - state data for container deployed inside and app.
type AppContainerStats struct {
	ContainerName string // unique under an App
	Status        string // uptime, pause, stop status
	Pids          uint32 // number of PIDs within the container
	// CPU stats
	Uptime         int64  // unix.nano, time since container starts
	CPUTotal       uint64 // container CPU since starts in nanosec
	SystemCPUTotal uint64 // total system, user, idle in nanosec
	// Memory stats
	UsedMem      uint32 // in MBytes
	AllocatedMem uint32 // in MBytes
	// Network stats
	TxBytes uint64 // in Bytes
	RxBytes uint64 // in Bytes
	// Disk stats
	ReadBytes  uint64 // in MBytes
	WriteBytes uint64 // in MBytes
}

// Key - key for AppContainerMetrics
func (acMetric AppContainerMetrics) Key() string {
	return acMetric.UUIDandVersion.UUID.String()
}

// LogCreate :
func (acMetric AppContainerMetrics) LogCreate(logBase *base.LogObject) {
	logObject := base.NewLogObject(logBase, base.AppContainerMetricsLogType, "",
		acMetric.UUIDandVersion.UUID, acMetric.LogKey())
	if logObject == nil {
		return
	}
	logObject.Metricf("App container metric create")
}

// LogModify :
func (acMetric AppContainerMetrics) LogModify(logBase *base.LogObject, old interface{}) {
	logObject := base.EnsureLogObject(logBase, base.AppContainerMetricsLogType, "",
		acMetric.UUIDandVersion.UUID, acMetric.LogKey())

	oldAcMetric, ok := old.(AppContainerMetrics)
	if !ok {
		logObject.Clone().Fatalf("LogModify: Old object interface passed is not of AppContainerMetrics type")
	}
	// XXX remove? XXX huge?
	logObject.CloneAndAddField("diff", cmp.Diff(oldAcMetric, acMetric)).
		Metricf("App container metric modify")
}

// LogDelete :
func (acMetric AppContainerMetrics) LogDelete(logBase *base.LogObject) {
	logObject := base.EnsureLogObject(logBase, base.AppContainerMetricsLogType, "",
		acMetric.UUIDandVersion.UUID, acMetric.LogKey())
	logObject.Metricf("App container metric delete")

	base.DeleteLogObject(logBase, acMetric.LogKey())
}

// LogKey :
func (acMetric AppContainerMetrics) LogKey() string {
	return string(base.AppContainerMetricsLogType) + "-" + acMetric.Key()
}

type MapServerType uint8

const (
	MST_INVALID MapServerType = iota
	MST_MAPSERVER
	MST_SUPPORT_SERVER
	MST_LAST = 255
)

type DnsNameToIP struct {
	HostName string
	IPs      []net.IP
}

type UnderlayNetworkConfig struct {
	Name       string           // From proto message
	AppMacAddr net.HardwareAddr // If set use it for vif
	AppIPAddr  net.IP           // If set use DHCP to assign to app
	IntfOrder  int32            // XXX need to get from API

	// XXX Shouldn't we use ErrorAndTime here
	// Error
	//	If there is a parsing error and this uLNetwork config cannot be
	//	processed, set the error here. This allows the error to be propagated
	//  back to zedcloud
	//	If this is non-empty ( != ""), the UL network Config should not be
	// 	processed further. It Should just	be flagged to be in error state
	//  back to the cloud.
	Error        string
	Network      uuid.UUID // Points to a NetworkInstance.
	ACLs         []ACE
	AccessVlanID uint32
	IfIdx        uint32 // If we have multiple interfaces on that network, we will increase the index
}

type UnderlayNetworkStatus struct {
	UnderlayNetworkConfig
	VifInfo
	BridgeMac         net.HardwareAddr
	BridgeIPAddr      net.IP   // The address for DNS/DHCP service in zedrouter
	AllocatedIPv4Addr net.IP   // Assigned to domU
	AllocatedIPv6List []net.IP // IPv6 addresses assigned to domU
	IPv4Assigned      bool     // Set to true once DHCP has assigned it to domU
	IPAddrMisMatch    bool
	HostName          string
}

// Extracted from the protobuf NetworkConfig. Used by parseSystemAdapter
// XXX replace by inline once we have device model
type NetworkXObjectConfig struct {
	UUID            uuid.UUID
	Type            NetworkType
	Dhcp            DhcpType // If DT_STATIC or DT_CLIENT use below
	Subnet          net.IPNet
	Gateway         net.IP
	DomainName      string
	NtpServer       net.IP
	DnsServers      []net.IP // If not set we use Gateway as DNS server
	DhcpRange       IpRange
	DnsNameToIPList []DnsNameToIP // Used for DNS and ACL ipset
	Proxy           *ProxyConfig
	WirelessCfg     WirelessConfig
	// Any errors from the parser
	// ErrorAndTime provides SetErrorNow() and ClearError()
	ErrorAndTime
}

type IpRange struct {
	Start net.IP
	End   net.IP
}

// Contains used to evaluate whether an IP address
// is within the range
func (ipRange IpRange) Contains(ipAddr net.IP) bool {
	if bytes.Compare(ipAddr, ipRange.Start) >= 0 &&
		bytes.Compare(ipAddr, ipRange.End) <= 0 {
		return true
	}
	return false
}

// Size returns addresses count inside IpRange
func (ipRange IpRange) Size() uint32 {
	//TBD:XXX, IPv6 handling
	ip1v4 := ipRange.Start.To4()
	ip2v4 := ipRange.End.To4()
	if ip1v4 == nil || ip2v4 == nil {
		return 0
	}
	ip1Int := binary.BigEndian.Uint32(ip1v4)
	ip2Int := binary.BigEndian.Uint32(ip2v4)
	if ip1Int > ip2Int {
		return ip1Int - ip2Int
	}
	return ip2Int - ip1Int
}

func (config NetworkXObjectConfig) Key() string {
	return config.UUID.String()
}

// LogCreate :
func (config NetworkXObjectConfig) LogCreate(logBase *base.LogObject) {
	logObject := base.NewLogObject(logBase, base.NetworkXObjectConfigLogType, "",
		config.UUID, config.LogKey())
	if logObject == nil {
		return
	}
	logObject.Noticef("NetworkXObject config create")
}

// LogModify :
func (config NetworkXObjectConfig) LogModify(logBase *base.LogObject, old interface{}) {
	logObject := base.EnsureLogObject(logBase, base.NetworkXObjectConfigLogType, "",
		config.UUID, config.LogKey())

	oldConfig, ok := old.(NetworkXObjectConfig)
	if !ok {
		logObject.Clone().Fatalf("LogModify: Old object interface passed is not of NetworkXObjectConfig type")
	}
	// XXX remove?
	logObject.CloneAndAddField("diff", cmp.Diff(oldConfig, config)).
		Noticef("NetworkXObject config modify")
}

// LogDelete :
func (config NetworkXObjectConfig) LogDelete(logBase *base.LogObject) {
	logObject := base.EnsureLogObject(logBase, base.NetworkXObjectConfigLogType, "",
		config.UUID, config.LogKey())
	logObject.Noticef("NetworkXObject config delete")

	base.DeleteLogObject(logBase, config.LogKey())
}

// LogKey :
func (config NetworkXObjectConfig) LogKey() string {
	return string(base.NetworkXObjectConfigLogType) + "-" + config.Key()
}

// AssignedAddrs :
type AssignedAddrs struct {
	IPv4Addr  net.IP
	IPv6Addrs []net.IP
}

type NetworkInstanceInfo struct {
	BridgeNum     int
	BridgeName    string
	BridgeIPAddr  net.IP
	BridgeMac     net.HardwareAddr
	BridgeIfindex int

	// Collection of address assignments; from MAC address to IP address
	IPAssignments map[string]AssignedAddrs

	// Set of vifs on this bridge
	Vifs []VifNameMac

	// Vif metric map. This should have a union of currently existing
	// vifs and previously deleted vifs.
	// XXX When a vif is removed from bridge (app instance delete case),
	// device might start reporting smaller statistic values. To avoid this
	// from happening, we keep a list of all vifs that were ever connected
	// to this bridge and their statistics.
	// We add statistics from all vifs while reporting to cloud.
	VifMetricMap map[string]NetworkMetric

	// Maintain a map of all access vlan ids to their counts, used by apps
	// connected to this network instance.
	VlanMap map[uint32]uint32
	// Counts the number of trunk ports attached to this network instance
	NumTrunkPorts uint32
}

func (instanceInfo *NetworkInstanceInfo) IsVifInBridge(
	vifName string) bool {
	for _, vif := range instanceInfo.Vifs {
		if vif.Name == vifName {
			return true
		}
	}
	return false
}

func (instanceInfo *NetworkInstanceInfo) RemoveVif(log *base.LogObject,
	vifName string) {
	log.Functionf("RemoveVif(%s, %s)", instanceInfo.BridgeName, vifName)

	found := false
	var vifs []VifNameMac
	for _, vif := range instanceInfo.Vifs {
		if vif.Name != vifName {
			vifs = append(vifs, vif)
		} else {
			found = true
		}
	}
	if !found {
		log.Errorf("RemoveVif(%x, %x) not found",
			instanceInfo.BridgeName, vifName)
	}
	instanceInfo.Vifs = vifs
}

func (instanceInfo *NetworkInstanceInfo) AddVif(log *base.LogObject,
	vifName string, appMac net.HardwareAddr, appID uuid.UUID) {

	log.Functionf("AddVif(%s, %s, %s, %s)",
		instanceInfo.BridgeName, vifName, appMac, appID.String())
	// XXX Should we just overwrite it? There is a lookup function
	//	anyways if the caller wants "check and add" semantics
	if instanceInfo.IsVifInBridge(vifName) {
		log.Errorf("AddVif(%s, %s) exists",
			instanceInfo.BridgeName, vifName)
		return
	}
	info := VifNameMac{
		Name:    vifName,
		MacAddr: appMac,
		AppID:   appID,
	}
	instanceInfo.Vifs = append(instanceInfo.Vifs, info)
}

type NetworkInstanceMetrics struct {
	UUIDandVersion UUIDandVersion
	DisplayName    string
	Type           NetworkInstanceType
	NetworkMetrics NetworkMetrics
	ProbeMetrics   ProbeMetrics
	VlanMetrics    VlanMetrics
}

// VlanMetrics :
type VlanMetrics struct {
	NumTrunkPorts uint32
	VlanCounts    map[uint32]uint32
}

// ProbeMetrics - NI probe metrics
type ProbeMetrics struct {
	SelectedUplinkIntf string             // the uplink interface that probing picked
	RemoteEndpoints    []string           // remote IP/URL addresses used for probing
	LocalPingIntvl     uint32             // local ping interval in seconds
	RemotePingIntvl    uint32             // remote probing interval in seconds
	UplinkCount        uint32             // number of possible uplink interfaces
	IntfProbeStats     []ProbeIntfMetrics // per dom0 intf uplink probing metrics
}

// ProbeIntfMetrics - per dom0 network uplink interface probing
type ProbeIntfMetrics struct {
	IntfName        string   // dom0 uplink interface name
	NexthopIPs      []net.IP // interface local next-hop address(es) used for probing
	NexthopUP       bool     // Is local next-hop in UP status
	RemoteUP        bool     // Is remote endpoint in UP status
	NexthopUPCnt    uint32   // local ping UP count
	NexthopDownCnt  uint32   // local ping DOWN count
	RemoteUPCnt     uint32   // remote probe UP count
	RemoteDownCnt   uint32   // remote probe DOWN count
	LatencyToRemote uint32   // probe latency to remote in msec
}

func (metrics NetworkInstanceMetrics) Key() string {
	return metrics.UUIDandVersion.UUID.String()
}

// LogCreate :
func (metrics NetworkInstanceMetrics) LogCreate(logBase *base.LogObject) {
	logObject := base.NewLogObject(logBase, base.NetworkInstanceMetricsLogType, "",
		metrics.UUIDandVersion.UUID, metrics.LogKey())
	if logObject == nil {
		return
	}
	logObject.Metricf("Network instance metrics create")
}

// LogModify :
func (metrics NetworkInstanceMetrics) LogModify(logBase *base.LogObject, old interface{}) {
	logObject := base.EnsureLogObject(logBase, base.NetworkInstanceMetricsLogType, "",
		metrics.UUIDandVersion.UUID, metrics.LogKey())

	oldMetrics, ok := old.(NetworkInstanceMetrics)
	if !ok {
		logObject.Clone().Fatalf("LogModify: Old object interface passed is not of NetworkInstanceMetrics type")
	}
	// XXX remove?
	logObject.CloneAndAddField("diff", cmp.Diff(oldMetrics, metrics)).
		Metricf("Network instance metrics modify")
}

// LogDelete :
func (metrics NetworkInstanceMetrics) LogDelete(logBase *base.LogObject) {
	logObject := base.EnsureLogObject(logBase, base.NetworkInstanceMetricsLogType, "",
		metrics.UUIDandVersion.UUID, metrics.LogKey())
	logObject.Metricf("Network instance metrics delete")

	base.DeleteLogObject(logBase, metrics.LogKey())
}

// LogKey :
func (metrics NetworkInstanceMetrics) LogKey() string {
	return string(base.NetworkInstanceMetricsLogType) + "-" + metrics.Key()
}

// Network metrics for overlay and underlay
// Matches networkMetrics protobuf message
type NetworkMetrics struct {
	MetricList     []NetworkMetric
	TotalRuleCount uint64
}

// Key is used for pubsub
func (nms NetworkMetrics) Key() string {
	return "global"
}

// LogCreate :
func (nms NetworkMetrics) LogCreate(logBase *base.LogObject) {
	logObject := base.NewLogObject(logBase, base.NetworkMetricsLogType, "",
		nilUUID, nms.LogKey())
	if logObject == nil {
		return
	}
	logObject.Metricf("Network metrics create")
}

// LogModify :
func (nms NetworkMetrics) LogModify(logBase *base.LogObject, old interface{}) {
	logObject := base.EnsureLogObject(logBase, base.NetworkMetricsLogType, "",
		nilUUID, nms.LogKey())

	oldNms, ok := old.(NetworkMetrics)
	if !ok {
		logObject.Clone().Fatalf("LogModify: Old object interface passed is not of NetworkMetrics type")
	}
	// XXX remove?
	logObject.CloneAndAddField("diff", cmp.Diff(oldNms, nms)).
		Metricf("Network metrics modify")
}

// LogDelete :
func (nms NetworkMetrics) LogDelete(logBase *base.LogObject) {
	logObject := base.EnsureLogObject(logBase, base.NetworkMetricsLogType, "",
		nilUUID, nms.LogKey())
	logObject.Metricf("Network metrics delete")

	base.DeleteLogObject(logBase, nms.LogKey())
}

// LogKey :
func (nms NetworkMetrics) LogKey() string {
	return string(base.NetworkMetricsLogType) + "-" + nms.Key()
}

func (nms *NetworkMetrics) LookupNetworkMetrics(ifName string) (NetworkMetric, bool) {
	for _, metric := range nms.MetricList {
		if ifName == metric.IfName {
			return metric, true
		}
	}
	return NetworkMetric{}, false
}

type NetworkMetric struct {
	IfName              string
	TxBytes             uint64
	RxBytes             uint64
	TxDrops             uint64
	RxDrops             uint64
	TxPkts              uint64
	RxPkts              uint64
	TxErrors            uint64
	RxErrors            uint64
	TxAclDrops          uint64 // For implicit deny/drop at end
	RxAclDrops          uint64 // For implicit deny/drop at end
	TxAclRateLimitDrops uint64 // For all rate limited rules
	RxAclRateLimitDrops uint64 // For all rate limited rules
}

type NetworkInstanceType int32

// These values should be same as the ones defined in zconfig.ZNetworkInstType
const (
	NetworkInstanceTypeFirst       NetworkInstanceType = 0
	NetworkInstanceTypeSwitch      NetworkInstanceType = 1
	NetworkInstanceTypeLocal       NetworkInstanceType = 2
	NetworkInstanceTypeCloud       NetworkInstanceType = 3
	NetworkInstanceTypeHoneyPot    NetworkInstanceType = 5
	NetworkInstanceTypeTransparent NetworkInstanceType = 6
	NetworkInstanceTypeLast        NetworkInstanceType = 255
)

type AddressType int32

// The values here should be same as the ones defined in zconfig.AddressType
const (
	AddressTypeNone       AddressType = 0 // For switch networks
	AddressTypeIPV4       AddressType = 1
	AddressTypeIPV6       AddressType = 2
	AddressTypeCryptoIPV4 AddressType = 3
	AddressTypeCryptoIPV6 AddressType = 4
	AddressTypeLast       AddressType = 255
)

// NetworkInstanceConfig
//
//	Config Object for NetworkInstance
//	Extracted from the protobuf NetworkInstanceConfig
type NetworkInstanceConfig struct {
	UUIDandVersion
	DisplayName string

	Type NetworkInstanceType

	// Activate - Activate the config.
	Activate bool

	// PortLogicalLabel - references port(s) from DevicePortConfig.
	// Can be a specific logicallabel for an interface, or a tag like "uplink"
	PortLogicalLabel string

	// IP configuration for the Application
	IpType          AddressType
	Subnet          net.IPNet
	Gateway         net.IP
	DomainName      string
	NtpServer       net.IP
	DnsServers      []net.IP // If not set we use Gateway as DNS server
	DhcpRange       IpRange
	DnsNameToIPList []DnsNameToIP // Used for DNS and ACL ipset

	// Any errors from the parser
	// ErrorAndTime provides SetErrorNow() and ClearError()
	ErrorAndTime
}

func (config *NetworkInstanceConfig) Key() string {
	return config.UUID.String()
}

// LogCreate :
func (config NetworkInstanceConfig) LogCreate(logBase *base.LogObject) {
	logObject := base.NewLogObject(logBase, base.NetworkInstanceConfigLogType, "",
		config.UUIDandVersion.UUID, config.LogKey())
	if logObject == nil {
		return
	}
	logObject.Noticef("Network instance config create")
}

// LogModify :
func (config NetworkInstanceConfig) LogModify(logBase *base.LogObject, old interface{}) {
	logObject := base.EnsureLogObject(logBase, base.NetworkInstanceConfigLogType, "",
		config.UUIDandVersion.UUID, config.LogKey())

	oldConfig, ok := old.(NetworkInstanceConfig)
	if !ok {
		logObject.Clone().Fatalf("LogModify: Old object interface passed is not of NetworkInstanceConfig type")
	}
	// XXX remove?
	logObject.CloneAndAddField("diff", cmp.Diff(oldConfig, config)).
		Noticef("Network instance config modify")
}

// LogDelete :
func (config NetworkInstanceConfig) LogDelete(logBase *base.LogObject) {
	logObject := base.EnsureLogObject(logBase, base.NetworkInstanceConfigLogType, "",
		config.UUIDandVersion.UUID, config.LogKey())
	logObject.Noticef("Network instance config delete")

	base.DeleteLogObject(logBase, config.LogKey())
}

// LogKey :
func (config NetworkInstanceConfig) LogKey() string {
	return string(base.NetworkInstanceConfigLogType) + "-" + config.Key()
}

func (config *NetworkInstanceConfig) IsIPv6() bool {
	switch config.IpType {
	case AddressTypeIPV6:
		return true
	case AddressTypeCryptoIPV6:
		return true
	}
	return false
}

// WithUplinkProbing returns true if the network instance is eligible for uplink
// probing (see pkg/pillar/uplinkprober).
// Uplink probing is performed only for L3 networks with non-empty "shared" uplink
// label, matching a subset of uplink ports.
// Even if a network instance is eligible for probing as determined by this method,
// the actual process of connectivity probing may still be inactive if there are
// no uplink ports available that match the label.
func (config *NetworkInstanceConfig) WithUplinkProbing() bool {
	switch config.Type {
	case NetworkInstanceTypeLocal:
		return IsSharedPortLabel(config.PortLogicalLabel)
	default:
		return false
	}
}

// IsUsingUplinkBridge returns true if the network instance is using the bridge
// created (by NIM) for the uplink port, instead of creating its own bridge.
func (config *NetworkInstanceConfig) IsUsingUplinkBridge() bool {
	switch config.Type {
	case NetworkInstanceTypeSwitch:
		airGapped := config.PortLogicalLabel == ""
		return !airGapped
	default:
		return false
	}
}

const (
	// UplinkLabel references all management interfaces.
	UplinkLabel = "uplink"
	// FreeUplinkLabel references all management interfaces with 0 cost.
	FreeUplinkLabel = "freeuplink"
)

// IsSharedPortLabel : returns true if the logical label references multiple
// ports.
// Currently used labels are:
//   - "uplink": any management interface
//   - "freeuplink": any management interface with 0 cost
func IsSharedPortLabel(label string) bool {
	switch label {
	case UplinkLabel:
		return true
	case FreeUplinkLabel:
		return true
	}
	return false
}

type ChangeInProgressType int32

const (
	ChangeInProgressTypeNone   ChangeInProgressType = 0
	ChangeInProgressTypeCreate ChangeInProgressType = 1
	ChangeInProgressTypeModify ChangeInProgressType = 2
	ChangeInProgressTypeDelete ChangeInProgressType = 3
	ChangeInProgressTypeLast   ChangeInProgressType = 255
)

// NetworkInstanceStatus
//
//	Config Object for NetworkInstance
//	Extracted from the protobuf NetworkInstanceConfig
type NetworkInstanceStatus struct {
	NetworkInstanceConfig
	// Make sure the Activate from the config isn't exposed as a boolean
	Activate uint64

	ChangeInProgress ChangeInProgressType

	// Activated is true if the network instance has been created in the network stack.
	Activated bool

	NetworkInstanceInfo

	// Decided by local/remote probing
	SelectedUplinkLogicalLabel string
	SelectedUplinkIntfName     string

	// True if uplink probing is running
	RunningUplinkProbing bool

	// True if NI is not activated only because of (currently) missing uplink.
	WaitingForUplink bool
}

// LogCreate :
func (status NetworkInstanceStatus) LogCreate(logBase *base.LogObject) {
	logObject := base.NewLogObject(logBase, base.NetworkInstanceStatusLogType, "",
		status.UUIDandVersion.UUID, status.LogKey())
	if logObject == nil {
		return
	}
	logObject.Noticef("Network instance status create")
}

// LogModify :
func (status NetworkInstanceStatus) LogModify(logBase *base.LogObject, old interface{}) {
	logObject := base.EnsureLogObject(logBase, base.NetworkInstanceStatusLogType, "",
		status.UUIDandVersion.UUID, status.LogKey())

	oldStatus, ok := old.(NetworkInstanceStatus)
	if !ok {
		logObject.Clone().Fatalf("LogModify: Old object interface passed is not of NetworkInstanceStatus type")
	}
	// XXX remove?
	logObject.CloneAndAddField("diff", cmp.Diff(oldStatus, status)).
		Noticef("Network instance status modify")
}

// LogDelete :
func (status NetworkInstanceStatus) LogDelete(logBase *base.LogObject) {
	logObject := base.EnsureLogObject(logBase, base.NetworkInstanceStatusLogType, "",
		status.UUIDandVersion.UUID, status.LogKey())
	logObject.Noticef("Network instance status delete")

	base.DeleteLogObject(logBase, status.LogKey())
}

// LogKey :
func (status NetworkInstanceStatus) LogKey() string {
	return string(base.NetworkInstanceStatusLogType) + "-" + status.Key()
}

type VifNameMac struct {
	Name    string
	MacAddr net.HardwareAddr
	AppID   uuid.UUID
}

/*
 * Tx/Rx of bridge is equal to the total of Tx/Rx on all member
 * virtual interfaces excluding the bridge itself.
 *
 * Drops/Errors/AclDrops of bridge is equal to total of Drops/Errors/AclDrops
 * on all member virtual interface including the bridge.
 */
func (status *NetworkInstanceStatus) UpdateNetworkMetrics(log *base.LogObject,
	nms *NetworkMetrics) (brNetMetric *NetworkMetric) {

	brNetMetric = &NetworkMetric{IfName: status.BridgeName}
	status.VifMetricMap = make(map[string]NetworkMetric) // clear previous metrics
	for _, vif := range status.Vifs {
		metric, found := nms.LookupNetworkMetrics(vif.Name)
		if !found {
			log.Tracef("No metrics found for interface %s",
				vif.Name)
			continue
		}
		status.VifMetricMap[vif.Name] = metric
	}
	for _, metric := range status.VifMetricMap {
		brNetMetric.TxBytes += metric.TxBytes
		brNetMetric.RxBytes += metric.RxBytes
		brNetMetric.TxPkts += metric.TxPkts
		brNetMetric.RxPkts += metric.RxPkts
		brNetMetric.TxErrors += metric.TxErrors
		brNetMetric.RxErrors += metric.RxErrors
		brNetMetric.TxDrops += metric.TxDrops
		brNetMetric.RxDrops += metric.RxDrops
		brNetMetric.TxAclDrops += metric.TxAclDrops
		brNetMetric.RxAclDrops += metric.RxAclDrops
		brNetMetric.TxAclRateLimitDrops += metric.TxAclRateLimitDrops
		brNetMetric.RxAclRateLimitDrops += metric.RxAclRateLimitDrops
	}
	return brNetMetric
}

/*
 * Tx/Rx of bridge is equal to the total of Tx/Rx on all member
 * virtual interfaces excluding the bridge itself.
 *
 * Drops/Errors/AclDrops of bridge is equal to total of Drops/Errors/AclDrops
 * on all member virtual interface including the bridge.
 */
func (status *NetworkInstanceStatus) UpdateBridgeMetrics(log *base.LogObject,
	nms *NetworkMetrics, netMetric *NetworkMetric) {
	// Get bridge metrics
	bridgeMetric, found := nms.LookupNetworkMetrics(status.BridgeName)
	if !found {
		log.Tracef("No metrics found for Bridge %s",
			status.BridgeName)
	} else {
		netMetric.TxErrors += bridgeMetric.TxErrors
		netMetric.RxErrors += bridgeMetric.RxErrors
		netMetric.TxDrops += bridgeMetric.TxDrops
		netMetric.RxDrops += bridgeMetric.RxDrops
		netMetric.TxAclDrops += bridgeMetric.TxAclDrops
		netMetric.RxAclDrops += bridgeMetric.RxAclDrops
		netMetric.TxAclRateLimitDrops += bridgeMetric.TxAclRateLimitDrops
		netMetric.RxAclRateLimitDrops += bridgeMetric.RxAclRateLimitDrops
	}
}

// Returns true if found
func (status *NetworkInstanceStatus) IsIpAssigned(ip net.IP) bool {
	for _, assignments := range status.IPAssignments {
		if ip.Equal(assignments.IPv4Addr) {
			return true
		}
		for _, nip := range assignments.IPv6Addrs {
			if ip.Equal(nip) {
				return true
			}
		}
	}
	return false
}

// ACEDirection :
// Rule direction
type ACEDirection uint8

const (
	// AceDirBoth : Rule applies in both directions
	AceDirBoth ACEDirection = iota
	// AceDirIngress : Rules applies in Ingress direction (from internet to app)
	AceDirIngress ACEDirection = 1
	// AceDirEgress : Rules applies in Egress direction (from app to internet)
	AceDirEgress ACEDirection = 2
)

// Similar support as in draft-ietf-netmod-acl-model
type ACE struct {
	Matches []ACEMatch
	Actions []ACEAction
	Name    string
	RuleID  int32
	Dir     ACEDirection
}

// The Type can be "ip" or "host" (aka domain name), "eidset", "protocol",
// "fport", or "lport" for now. The ip and host matches the remote IP/hostname.
// The host matching is suffix-matching thus zededa.net matches *.zededa.net.
// XXX Need "interface"... e.g. "uplink" or "eth1"? Implicit in network used?
// For now the matches are bidirectional.
// XXX Add directionality? Different rate limits in different directions?
// Value is always a string.
// There is an implicit reject rule at the end.
// The "eidset" type is special for the overlay. Matches all the IPs which
// are part of the DnsNameToIPList.
type ACEMatch struct {
	Type  string
	Value string
}

type ACEAction struct {
	Drop bool // Otherwise accept

	Limit      bool   // Is limiter enabled?
	LimitRate  int    // Packets per unit
	LimitUnit  string // "s", "m", "h", for second, minute, hour
	LimitBurst int    // Packets

	PortMap    bool // Is port mapping part of action?
	TargetPort int  // Internal port
}

// IPTuple :
type IPTuple struct {
	Src     net.IP // local App IP address
	Dst     net.IP // remote IP address
	SrcPort int32  // local App IP Port
	DstPort int32  // remote IP Port
	Proto   int32
}

// FlowScope :
type FlowScope struct {
	AppUUID        uuid.UUID
	NetAdapterName string // logical name for VIF (set by controller in NetworkAdapter.Name)
	BrIfName       string
	NetUUID        uuid.UUID
	Sequence       string // used internally for limit and pkt size per app/bn
}

// Key identifies flow.
func (fs FlowScope) Key() string {
	// Use adapter name instead of NI UUID because application can be connected to the same
	// network instance with multiple interfaces.
	key := fs.AppUUID.String() + "-" + fs.NetAdapterName
	if fs.Sequence != "" {
		key += "-" + fs.Sequence
	}
	return key
}

// ACLActionType - action
type ACLActionType uint8

// ACLAction Enum
const (
	ACLActionNone ACLActionType = iota
	ACLActionAccept
	ACLActionDrop
)

// FlowRec :
type FlowRec struct {
	Flow      IPTuple
	Inbound   bool
	ACLID     int32
	Action    ACLActionType
	StartTime int64
	StopTime  int64
	TxBytes   int64
	TxPkts    int64
	RxBytes   int64
	RxPkts    int64
}

// DNSReq :
type DNSReq struct {
	HostName    string
	Addrs       []net.IP
	RequestTime int64
	ACLNum      int32
}

// IPFlow :
type IPFlow struct {
	Scope   FlowScope
	Flows   []FlowRec
	DNSReqs []DNSReq
}

// Key :
func (flows IPFlow) Key() string {
	return flows.Scope.Key()
}

// LogCreate : we treat IPFlow as Metrics for logging
func (flows IPFlow) LogCreate(logBase *base.LogObject) {
	logObject := base.NewLogObject(logBase, base.IPFlowLogType, "",
		nilUUID, flows.LogKey())
	if logObject == nil {
		return
	}
	logObject.Metricf("IP flow create")
}

// LogModify :
func (flows IPFlow) LogModify(logBase *base.LogObject, old interface{}) {
	logObject := base.EnsureLogObject(logBase, base.IPFlowLogType, "",
		nilUUID, flows.LogKey())

	oldFlows, ok := old.(IPFlow)
	if !ok {
		logObject.Clone().Fatalf("LogModify: Old object interface passed is not of IPFlow type")
	}
	// XXX remove?
	logObject.CloneAndAddField("diff", cmp.Diff(oldFlows, flows)).
		Metricf("IP flow modify")
}

// LogDelete :
func (flows IPFlow) LogDelete(logBase *base.LogObject) {
	logObject := base.EnsureLogObject(logBase, base.IPFlowLogType, "",
		nilUUID, flows.LogKey())
	logObject.Metricf("IP flow delete")

	base.DeleteLogObject(logBase, flows.LogKey())
}

// LogKey :
func (flows IPFlow) LogKey() string {
	return string(base.IPFlowLogType) + "-" + flows.Key()
}

// AppInstMetaDataType - types of app meta data
type AppInstMetaDataType uint8

// enum app metadata type
const (
	AppInstMetaDataTypeNone AppInstMetaDataType = iota // enum for app inst metadata type
	AppInstMetaDataTypeKubeConfig
	AppInstMetaDataCustomStatus
)

// AppInstMetaData : App Instance Metadata
type AppInstMetaData struct {
	AppInstUUID uuid.UUID
	Data        []byte
	Type        AppInstMetaDataType
}

// Key : App Instance Metadata unique key
func (data AppInstMetaData) Key() string {
	return data.AppInstUUID.String() + "-" + string(data.Type)
}

// AppBlobsAvailable provides a list of AppCustom blobs which has been provided
// from the cloud
type AppBlobsAvailable struct {
	CustomMeta  string
	DownloadURL string
}

// AppInfo provides various information to the application
type AppInfo struct {
	AppBlobs []AppBlobsAvailable
}
