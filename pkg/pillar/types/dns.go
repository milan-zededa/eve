// Copyright (c) 2023 Zededa, Inc.
// SPDX-License-Identifier: Apache-2.0

// DNS (Device Network Status) = state of physical network interfaces
// used for management or as app-shared (not app-direct).

package types

import (
	"bytes"
	"fmt"
	"net"
	"reflect"
	"sort"
	"time"

	"github.com/eriknordmark/ipinfo"
	"github.com/lf-edge/eve/pkg/pillar/base"
	"github.com/lf-edge/eve/pkg/pillar/utils/generics"
	"github.com/lf-edge/eve/pkg/pillar/utils/netutils"
)

// WirelessStatus : state information for a single wireless device
type WirelessStatus struct {
	WType    WirelessType
	Cellular WwanNetworkStatus
	// TODO: Wifi status
}

type NetworkPortStatus struct {
	IfName         string
	Phylabel       string // Physical name set by controller/model
	Logicallabel   string
	Alias          string // From SystemAdapter's alias
	IsMgmt         bool   // Used to talk to controller
	IsL3Port       bool   // True if port is applicable to operate on the network layer
	Cost           uint8
	Dhcp           DhcpType
	Type           NetworkType // IPv4 or IPv6 or Dual stack
	Subnet         net.IPNet
	NtpServer      net.IP // This comes from network instance configuration
	DomainName     string
	DNSServers     []net.IP // If not set we use Gateway as DNS server
	NtpServers     []net.IP // This comes from DHCP done on uplink port
	AddrInfoList   []AddrInfo
	Up             bool
	MacAddr        net.HardwareAddr
	DefaultRouters []net.IP
	MTU            uint16
	WirelessCfg    WirelessConfig
	WirelessStatus WirelessStatus
	ProxyConfig
	L2LinkConfig
	// TestResults provides recording of failure and success
	TestResults
}

// HasIPAndDNS - Check if the given port has a valid unicast IP along with DNS & Gateway.
func (port NetworkPortStatus) HasIPAndDNS() bool {
	foundUnicast := false
	for _, addr := range port.AddrInfoList {
		if !addr.Addr.IsLinkLocalUnicast() {
			foundUnicast = true
		}
	}
	if foundUnicast && len(port.DefaultRouters) > 0 && len(port.DNSServers) > 0 {
		return true
	}
	return false
}

// CountAddrsExceptLinkLocal return number of IP addresses assigned to the given port
// excluding link-local addresses
func (port NetworkPortStatus) CountAddrsExceptLinkLocal() (int, error) {
	addrs, err := port.getPortAddrs(false, 0)
	return len(addrs), err
}

// CountIPv4AddrsExceptLinkLocal is like CountAddrsExceptLinkLocal but
// only IPv4 addresses are counted
func (port NetworkPortStatus) CountIPv4AddrsExceptLinkLocal() (int, error) {
	addrs, err := port.getPortAddrs(false, 4)
	return len(addrs), err
}

// PickAddrExceptLinkLocal is used to pick one address from those assigned to the port,
// excluding link-local addresses.
func (port NetworkPortStatus) PickAddrExceptLinkLocal(pickNum int) (net.IP, error) {
	addrs, err := port.getPortAddrs(false, 0)
	if err != nil {
		return net.IP{}, err
	}
	numAddrs := len(addrs)
	if numAddrs == 0 {
		return net.IP{}, fmt.Errorf("no addresses")
	}
	pickNum = pickNum % numAddrs
	return addrs[pickNum], nil
}

// getPortAddrs returns (potentially filtered) IP addresses assigned to the port.
// includeLinkLocal and af can be used to exclude addresses.
func (port NetworkPortStatus) getPortAddrs(
	includeLinkLocal bool, af uint) ([]net.IP, error) {
	var addrs []net.IP
	for _, i := range port.AddrInfoList {
		if !includeLinkLocal && i.Addr.IsLinkLocalUnicast() {
			continue
		}
		if i.Addr == nil {
			continue
		}
		switch af {
		case 0:
			// Accept any
		case 4:
			if i.Addr.To4() == nil {
				continue
			}
		case 6:
			if i.Addr.To4() != nil {
				continue
			}
		}
		addrs = append(addrs, i.Addr)
	}
	if len(addrs) != 0 {
		return addrs, nil
	} else {
		return []net.IP{}, &IPAddrNotAvail{PortLL: port.Logicallabel}
	}
}

type AddrInfo struct {
	Addr             net.IP
	Geo              ipinfo.IPInfo
	LastGeoTimestamp time.Time
}

// DeviceNetworkStatus is published to microservices which needs to know about ports and IP addresses
// It is published under the key "global" only
type DeviceNetworkStatus struct {
	DPCKey       string                  // For logs/testing
	Version      DevicePortConfigVersion // From DevicePortConfig
	Testing      bool                    // Ignore since it is not yet verified
	State        DPCState                // Details about testing state
	CurrentIndex int                     // For logs
	RadioSilence RadioSilence            // The actual state of the radio-silence mode
	Ports        []NetworkPortStatus
}

// Key is used for pubsub
func (status DeviceNetworkStatus) Key() string {
	return "global"
}

// LogCreate :
func (status DeviceNetworkStatus) LogCreate(logBase *base.LogObject) {
	logObject := base.NewLogObject(logBase, base.DeviceNetworkStatusLogType, "",
		nilUUID, status.LogKey())
	if logObject == nil {
		return
	}
	logObject.CloneAndAddField("testing-bool", status.Testing).
		AddField("ports-int64", len(status.Ports)).
		AddField("state", status.State.String()).
		AddField("current-index-int64", status.CurrentIndex).
		Noticef("DeviceNetworkStatus create")
	for _, p := range status.Ports {
		// XXX different logobject for a particular port?
		logObject.CloneAndAddField("ifname", p.IfName).
			AddField("logical-label", p.Logicallabel).
			AddField("last-error", p.LastError).
			AddField("last-succeeded", p.LastSucceeded).
			AddField("last-failed", p.LastFailed).
			Noticef("DeviceNetworkStatus port create")
	}
}

// LogModify :
func (status DeviceNetworkStatus) LogModify(logBase *base.LogObject, old interface{}) {
	logObject := base.EnsureLogObject(logBase, base.DeviceNetworkStatusLogType, "",
		nilUUID, status.LogKey())

	oldStatus, ok := old.(DeviceNetworkStatus)
	if !ok {
		logObject.Clone().Fatalf("LogModify: Old object interface passed is not of DeviceNetworkStatus type")
	}
	if oldStatus.Testing != status.Testing ||
		oldStatus.State != status.State ||
		oldStatus.CurrentIndex != status.CurrentIndex ||
		len(oldStatus.Ports) != len(status.Ports) {

		logData := logObject.CloneAndAddField("testing-bool", status.Testing).
			AddField("ports-int64", len(status.Ports)).
			AddField("state", status.State.String()).
			AddField("current-index-int64", status.CurrentIndex).
			AddField("old-testing-bool", oldStatus.Testing).
			AddField("old-ports-int64", len(oldStatus.Ports)).
			AddField("old-state", oldStatus.State.String()).
			AddField("old-current-index-int64", oldStatus.CurrentIndex)

		if oldStatus.State == status.State && oldStatus.CurrentIndex == status.CurrentIndex &&
			len(oldStatus.Ports) == len(status.Ports) {
			// if only testing state changed, reduce log level
			logData.Function("DeviceNetworkStatus modify")
		} else {
			logData.Notice("DeviceNetworkStatus modify")
		}
	}
	// XXX which fields to compare/log?
	for i, p := range status.Ports {
		if len(oldStatus.Ports) <= i {
			continue
		}
		op := oldStatus.Ports[i]
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
				logData.Function("DeviceNetworkStatus port modify")
			} else {
				logData.Notice("DeviceNetworkStatus port modify")
			}
		}
	}
}

// LogDelete :
func (status DeviceNetworkStatus) LogDelete(logBase *base.LogObject) {
	logObject := base.EnsureLogObject(logBase, base.DeviceNetworkStatusLogType, "",
		nilUUID, status.LogKey())
	logObject.CloneAndAddField("testing-bool", status.Testing).
		AddField("ports-int64", len(status.Ports)).
		AddField("state", status.State.String()).
		Noticef("DeviceNetworkStatus instance status delete")
	for _, p := range status.Ports {
		// XXX different logobject for a particular port?
		logObject.CloneAndAddField("ifname", p.IfName).
			AddField("logical-label", p.Logicallabel).
			AddField("last-error", p.LastError).
			AddField("last-succeeded", p.LastSucceeded).
			AddField("last-failed", p.LastFailed).
			Noticef("DeviceNetworkStatus port delete")
	}

	base.DeleteLogObject(logBase, status.LogKey())
}

// LogKey :
func (status DeviceNetworkStatus) LogKey() string {
	return string(base.DeviceNetworkStatusLogType) + "-" + status.Key()
}

// MostlyEqual compares two DeviceNetworkStatus but skips things the test status/results aspects, including State and Testing.
// We compare the Ports in array order.
func (status DeviceNetworkStatus) MostlyEqual(status2 DeviceNetworkStatus) bool {

	if len(status.Ports) != len(status2.Ports) {
		return false
	}
	for i, p1 := range status.Ports {
		p2 := status2.Ports[i]
		if p1.IfName != p2.IfName ||
			p1.Phylabel != p2.Phylabel ||
			p1.Logicallabel != p2.Logicallabel ||
			p1.Alias != p2.Alias ||
			p1.IsMgmt != p2.IsMgmt ||
			p1.Cost != p2.Cost {
			return false
		}
		if p1.Dhcp != p2.Dhcp ||
			!EqualSubnet(p1.Subnet, p2.Subnet) ||
			!p1.NtpServer.Equal(p2.NtpServer) ||
			p1.DomainName != p2.DomainName {
			return false
		}
		if !generics.EqualSetsFn(p1.DNSServers, p2.DNSServers, netutils.EqualIPs) {
			return false
		}
		if !generics.EqualSetsFn(p1.AddrInfoList, p2.AddrInfoList,
			func(ai1, ai2 AddrInfo) bool {
				return ai1.Addr.Equal(ai2.Addr)
			}) {
			return false
		}
		if p1.Up != p2.Up ||
			!bytes.Equal(p1.MacAddr, p2.MacAddr) {
			return false
		}
		if !generics.EqualSetsFn(p1.DefaultRouters, p2.DefaultRouters, netutils.EqualIPs) {
			return false
		}

		if !reflect.DeepEqual(p1.ProxyConfig, p2.ProxyConfig) ||
			!reflect.DeepEqual(p1.WirelessStatus, p2.WirelessStatus) {
			return false
		}
	}
	if !reflect.DeepEqual(status.RadioSilence, status2.RadioSilence) {
		return false
	}
	return true
}

// MostlyEqualStatus compares two DeviceNetworkStatus but skips things that are
// unimportant like just an increase in the success timestamp, but detects
// when a port changes to/from a failure.
func (status *DeviceNetworkStatus) MostlyEqualStatus(status2 DeviceNetworkStatus) bool {

	if !status.MostlyEqual(status2) {
		return false
	}
	if status.State != status2.State || status.Testing != status2.Testing ||
		status.CurrentIndex != status2.CurrentIndex {
		return false
	}
	if len(status.Ports) != len(status2.Ports) {
		return false
	}
	for i, p1 := range status.Ports {
		p2 := status2.Ports[i]
		// Did we change to/from failure?
		if p1.HasError() != p2.HasError() {
			return false
		}
	}
	return true
}

// EqualSubnet compares two subnets; silently assumes contiguous masks
func EqualSubnet(subnet1, subnet2 net.IPNet) bool {
	if !subnet1.IP.Equal(subnet2.IP) {
		return false
	}
	len1, _ := subnet1.Mask.Size()
	len2, _ := subnet2.Mask.Size()
	return len1 == len2
}

// GetPortByIfName - Get Port Status for port with given Ifname
func (status *DeviceNetworkStatus) GetPortByIfName(ifname string) *NetworkPortStatus {
	for i := range status.Ports {
		if status.Ports[i].IfName == ifname {
			return &status.Ports[i]
		}
	}
	return nil
}

// GetPortByLogicallabel - get pointer to port with the given logical label.
// Shared labels are not supported by this function (consider using method
// GetPortsByLogicallabel instead).
func (config *DeviceNetworkStatus) GetPortByLogicallabel(label string) *NetworkPortStatus {
	for i := range config.Ports {
		portPtr := &config.Ports[i]
		if label == portPtr.Logicallabel {
			return portPtr
		}
	}
	return nil
}

// GetPortsByLogicallabel - Get Port Status for all ports matching the given label.
// The label can be shared, matching multiple ports, or uniquely identifying a single port.
func (status *DeviceNetworkStatus) GetPortsByLogicallabel(label string) []*NetworkPortStatus {
	var ports []*NetworkPortStatus
	// Check for shared labels first.
	switch label {
	case UplinkLabel:
		for i := range status.Ports {
			if !status.Ports[i].IsMgmt {
				continue
			}
			ports = append(ports, &status.Ports[i])
		}
		return ports
	case FreeUplinkLabel:
		for i := range status.Ports {
			if !status.Ports[i].IsMgmt {
				continue
			}
			if status.Ports[i].Cost > 0 {
				continue
			}
			ports = append(ports, &status.Ports[i])
		}
		return ports
	}
	// Label is referencing single port.
	for i := range status.Ports {
		if status.Ports[i].Logicallabel == label {
			ports = append(ports, &status.Ports[i])
			return ports
		}
	}
	return nil
}

// HasErrors - DeviceNetworkStatus has errors on any of it's ports?
func (status DeviceNetworkStatus) HasErrors() bool {
	for _, port := range status.Ports {
		if port.HasError() {
			return true
		}
	}
	return false
}

// GetPortAddrInfo returns address info for a given interface and its IP address.
func (status DeviceNetworkStatus) GetPortAddrInfo(logicalLabel string, addr net.IP) *AddrInfo {
	portStatus := status.GetPortByLogicallabel(logicalLabel)
	if portStatus == nil {
		return nil
	}
	for i := range portStatus.AddrInfoList {
		if portStatus.AddrInfoList[i].Addr.Equal(addr) {
			return &portStatus.AddrInfoList[i]
		}
	}
	return nil
}

// GetMgmtPortsAny returns all management ports
func (status DeviceNetworkStatus) GetMgmtPortsAny(rotation int) []*NetworkPortStatus {
	return status.getPortsImpl(rotation, false, 0, true, false)
}

// GetMgmtPortsByCost returns all management ports with a given port cost
func (status DeviceNetworkStatus) GetMgmtPortsByCost(cost uint8) []*NetworkPortStatus {
	return status.getPortsImpl(0, true, cost, true, false)
}

// GetMgmtPortsSortedByCost returns all management ports sorted by port cost.
// Rotation causes rotation/round-robin within each cost.
func (status DeviceNetworkStatus) GetMgmtPortsSortedByCost(
	rotation int) []*NetworkPortStatus {
	return status.getPortsSortedByCostImpl(rotation, PortCostMax, true, false)
}

// GetAllPortsSortedByCost returns all ports (management and app shared) sorted by port cost.
// Rotation causes rotation/round-robin within each cost.
func (status DeviceNetworkStatus) GetAllPortsSortedByCost(
	rotation int) []*NetworkPortStatus {
	return status.getPortsSortedByCostImpl(rotation, PortCostMax, false, false)
}

// GetMgmtPortsSortedByCostWithoutFailed returns all management ports sorted by port cost
// ignoring ports with failures.
// Rotation causes rotation/round-robin within each cost.
func (status DeviceNetworkStatus) GetMgmtPortsSortedByCostWithoutFailed(
	rotation int) []*NetworkPortStatus {
	return status.getPortsSortedByCostImpl(rotation, PortCostMax, true, true)
}

// getPortsSortedByCostImpl returns all ports sorted by port cost
// up to and including the maxCost
func (status DeviceNetworkStatus) getPortsSortedByCostImpl(rotation int,
	maxCost uint8, mgmtOnly, dropFailed bool) (ports []*NetworkPortStatus) {
	costList := status.getPortCostListImpl(maxCost)
	for _, cost := range costList {
		ports = append(ports,
			status.getPortsImpl(rotation, true, cost, mgmtOnly, dropFailed)...)
	}
	return ports
}

// Returns statuses of ports matching requirements (cost, is-mgmt, etc.).
func (status DeviceNetworkStatus) getPortsImpl(rotation int,
	matchCost bool, cost uint8, mgmtOnly, dropFailed bool) (ports []*NetworkPortStatus) {

	for idx, us := range status.Ports {
		if matchCost && us.Cost != cost {
			continue
		}
		if mgmtOnly && !us.IsMgmt {
			continue
		}
		if dropFailed && us.HasError() {
			continue
		}
		ports = append(ports, &status.Ports[idx])
	}
	return generics.RotateList(ports, rotation)
}

// GetPortCostList returns the sorted list of port costs with cost zero entries first.
func (status DeviceNetworkStatus) GetPortCostList() []uint8 {
	return status.getPortCostListImpl(PortCostMax)
}

// getPortCostListImpl returns the sorted port costs up to and including the max.
func (status DeviceNetworkStatus) getPortCostListImpl(maxCost uint8) []uint8 {
	var costList []uint8
	for _, us := range status.Ports {
		costList = append(costList, us.Cost)
	}
	if len(costList) == 0 {
		return []uint8{}
	}
	// Need sort -u so separately we remove the duplicates
	sort.Slice(costList,
		func(i, j int) bool { return costList[i] < costList[j] })
	unique := make([]uint8, 0, len(costList))
	i := 0
	unique = append(unique, costList[0])
	for _, cost := range costList {
		if cost != unique[i] && cost <= maxCost {
			unique = append(unique, cost)
			i++
		}
	}
	return unique
}

// GetMgmtPortByAddr looks up management port by the assigned address.
func (status DeviceNetworkStatus) GetMgmtPortByAddr(addr net.IP) *NetworkPortStatus {
	for _, us := range status.Ports {
		if !us.IsMgmt {
			continue
		}
		for _, i := range us.AddrInfoList {
			if i.Addr.Equal(addr) {
				return &us
			}
		}
	}
	return nil
}

// CountAddrsExceptLinkLocal returns the number of IP addresses assigned
// to the management ports (for all port costs) excluding link-local addresses.
func (status DeviceNetworkStatus) CountAddrsExceptLinkLocal() int {
	// Count the number of addresses which apply
	addrs, _ := status.getAddrsImpl(PortCostMax, false, 0)
	return len(addrs)
}

// CountAddrsExceptLinkLocalWithCost is like CountAddrsExceptLinkLocal but in addition
// allows the caller to specify the cost between PortCostMin (0) and PortCostMax(255).
// If 0 is specified it considers only free ports.
// if 255 is specified it considers all the ports.
func (status DeviceNetworkStatus) CountAddrsExceptLinkLocalWithCost(
	maxCost uint8) int {
	// Count the number of addresses which apply
	addrs, _ := status.getAddrsImpl(maxCost, false, 0)
	return len(addrs)
}

// CountIPv4AddrsExceptLinkLocal is like CountAddrsExceptLinkLocal but only IPv4 addresses
// are counted.
func (status DeviceNetworkStatus) CountIPv4AddrsExceptLinkLocal() int {
	// Count the number of addresses which apply
	addrs, _ := status.getAddrsImpl(PortCostMax, false, 4)
	return len(addrs)
}

// PickAddrExceptLinkLocal is used to pick one address assigned to any of the management
// ports, excluding link-local addresses.
func (status DeviceNetworkStatus) PickAddrExceptLinkLocal(pickNum int) (net.IP, error) {
	addrs, err := status.getAddrsImpl(PortCostMax, false, 0)
	if err != nil {
		return net.IP{}, err
	}
	numAddrs := len(addrs)
	if numAddrs == 0 {
		return net.IP{}, fmt.Errorf("no addresses")
	}
	pickNum = pickNum % numAddrs
	return addrs[pickNum], nil
}

// PickAddrExceptLinkLocalWithCost is just like PickAddrExceptLinkLocal,
// except that it allows to filter out port exceeding the given cost.
func (status DeviceNetworkStatus) PickAddrExceptLinkLocalWithCost(
	pickNum int, maxCost uint8) (net.IP, error) {
	addrs, err := status.getAddrsImpl(maxCost, false, 0)
	if err != nil {
		return net.IP{}, err
	}
	numAddrs := len(addrs)
	if numAddrs == 0 {
		return net.IP{}, fmt.Errorf("no addresses")
	}
	pickNum = pickNum % numAddrs
	return addrs[pickNum], nil
}

// getAddrsImpl returns a list of IP addresses assigned to management ports,
// in order sorted by port cost.
// Can be filtered by address family and the address scope.
func (status DeviceNetworkStatus) getAddrsImpl(maxCost uint8, includeLinkLocal bool,
	af uint) ([]net.IP, error) {
	// Get ports in cost order.
	ports := status.getPortsSortedByCostImpl(0, maxCost, true, false)
	var addrs []net.IP
	for _, p := range ports {
		portAddrs, _ := p.getPortAddrs(includeLinkLocal, af)
		addrs = append(addrs, portAddrs...)
	}
	return addrs, nil
}

// CountDNSServers returns the number of DNS servers from all management ports
// or just from a specific port referenced by its logical label.
func (status *DeviceNetworkStatus) CountDNSServers(logicalLabel string) int {
	return len(status.GetDNSServers(logicalLabel))
}

// GetDNSServers returns the list of DNS servers from all management ports
// or just from a specific port referenced by its logical label.
func (status *DeviceNetworkStatus) GetDNSServers(logicalLabel string) []net.IP {
	var servers []net.IP
	for _, us := range status.Ports {
		if !us.IsMgmt && logicalLabel == "" {
			continue
		}
		if logicalLabel != "" && us.Logicallabel != logicalLabel {
			continue
		}
		for _, server := range us.DNSServers {
			servers = append(servers, server)
		}
	}
	return servers
}

// GetNTPServers returns the list of NTP servers from all management ports
// or just from a specific port referenced by its logical label.
func (status *DeviceNetworkStatus) GetNTPServers(logicalLabel string) []net.IP {

	var servers []net.IP
	for _, us := range status.Ports {
		if !us.IsMgmt && logicalLabel == "" {
			continue
		}
		if logicalLabel != "" && us.Logicallabel != logicalLabel {
			continue
		}
		servers = append(servers, us.NtpServers...)
		// Add statically configured NTP server as well, but avoid duplicates.
		if us.NtpServer != nil {
			var found bool
			for _, server := range servers {
				if server.Equal(us.NtpServer) {
					found = true
					break
				}
			}
			if !found {
				servers = append(servers, us.NtpServer)
			}
		}
	}
	return servers
}

// UpdatePortStatusFromIntfStatusMap - Set TestResults for ports in DeviceNetworkStatus to
// those from intfStatusMap. If a port is not found in intfStatusMap, it means
// the port was not tested, so we retain the original TestResults for the port.
func (status *DeviceNetworkStatus) UpdatePortStatusFromIntfStatusMap(
	intfStatusMap IntfStatusMap) {
	for indx := range status.Ports {
		portPtr := &status.Ports[indx]
		tr, ok := intfStatusMap.StatusMap[portPtr.Logicallabel]
		if ok {
			portPtr.TestResults.Update(tr)
		}
		// Else - Port not tested hence no change
	}
}
