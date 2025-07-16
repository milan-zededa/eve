// Copyright (c) 2026 Zededa, Inc.
// SPDX-License-Identifier: Apache-2.0

package networking_test

import (
	"fmt"
	"net"
	"testing"
	"time"

	. "github.com/onsi/gomega"

	eveconfig "github.com/lf-edge/eve-api/go/config"
	"github.com/lf-edge/eve-api/go/evecommon"
	eveinfo "github.com/lf-edge/eve-api/go/info"
	evemetrics "github.com/lf-edge/eve-api/go/metrics"
	"github.com/lf-edge/eve/evetest"
	api "github.com/lf-edge/eve/evetest/grpcapi/go"
	"github.com/lf-edge/eve/evetest/tests/matchers"
	"github.com/lf-edge/eve/evetest/tests/netmodels"
	"github.com/lf-edge/eve/pkg/pillar/utils/generics"
	"google.golang.org/protobuf/proto"
)

func TestActiveBackupBond(test *testing.T) {
	evetestT := evetest.Init(test)
	t := NewGomegaWithT(evetestT)
	defer evetest.Close()

	// Define configurable parameters available for the test.
	evetest.DefineTestParameters(
		evetest.HypervisorParameter(),
	)

	// Get parameter values set for this test execution.
	hypervisor := evetest.GetHypervisorParameterValue()

	devName := "edge-dev"
	requiredDevice := evetest.RequireEdgeDevice{
		Name:              devName,
		WithHypervisor:    hypervisor,
		DeviceReusePolicy: evetest.ResetDeviceConfig,
	}
	// Active-backup bond is transparent to the network switch -- only one
	// member transmits at a time, so no switch-side LAG/bond is needed.
	// We just need both ports on the same bridge to reach the same network.
	// (For 802.3ad/LACP, the SDN network model would need a Bond on the
	// bridge side to participate in LACP negotiation.)
	requiredNetModel := evetest.RequireNetworkModel{
		NetworkModel: netmodels.TwoMgmtPortsOneBridge,
	}
	evetest.Setup(requiredDevice, requiredNetModel)

	device := evetest.GetEdgeDevice(devName)
	evetest.Checkpoint("setup-done")

	// Build device config: two physical adapters without direct network
	// assignment, bonded together as active-backup.
	devConfig := evetest.NewEdgeDeviceConfig(devName)
	dhcpNet := devConfig.AddNetwork(
		evetest.DHCPNetworkConfig{
			NetworkType: evecommon.NetworkType_V4,
		})
	devConfig.AddNetworkAdapter(
		evetest.NetworkAdapterConfig{
			LogicalLabel:  "ethernet0",
			PhysicalLabel: "eth0",
			InterfaceName: "eth0",
			Usage:         evecommon.PhyIoMemberUsage_PhyIoUsageMgmtAndApps,
		})
	devConfig.AddNetworkAdapter(
		evetest.NetworkAdapterConfig{
			LogicalLabel:  "ethernet1",
			PhysicalLabel: "eth1",
			InterfaceName: "eth1",
			Usage:         evecommon.PhyIoMemberUsage_PhyIoUsageMgmtAndApps,
		})
	bondConfig := evetest.BondConfig{
		LogicalLabel:  "active-backup-bond",
		InterfaceName: "bond1", // bond0 is reserved in Linux
		MemberLabels:  []string{"ethernet0", "ethernet1"},
		BondMode:      evecommon.BondMode_BOND_MODE_ACTIVE_BACKUP,
		// Use ARP monitoring instead of MII because virtio-net does not
		// propagate link-down state from the SDN side to the EVE VM.
		// ARP monitoring actively probes the gateway and will detect
		// the failure when the SDN port is set AdminUp=false.
		ARPMonitor: &eveconfig.ArpMonitor{
			Interval:  1000,
			IpTargets: []string{"172.20.20.1"}, // gateway from TwoMgmtPortsOneBridge
		},
		NetworkUUID: dhcpNet,
		Usage:       evecommon.PhyIoMemberUsage_PhyIoUsageMgmtAndApps,
	}
	devConfig.AddBond(bondConfig)

	devUpdates, stopDevWatch := device.WatchDeviceInfo()
	defer stopDevWatch()
	devMetrics, stopDevMetricsWatch := device.WatchDeviceMetrics()
	defer stopDevMetricsWatch()
	device.ApplyConfig(devConfig, true)
	evetest.Checkpoint("config-applied")

	// Wait for device info to report the bond interface with an IP address,
	// no errors, and an active member.
	timeout := 5 * time.Minute
	var bondIP net.IP
	var activeMember string
	var bondStatus *eveinfo.BondStatus
	t.Eventually(devUpdates, timeout).Should(Receive(matchers.SatisfyPredicate(
		"Bond interface has IP, no errors and reports active member",
		func(info *eveinfo.ZInfoDevice) bool {
			port := getDevicePort("active-backup-bond", info)
			if port == nil {
				return false
			}
			if port.GetErr() != nil && port.GetErr().GetDescription() != "" {
				return false
			}
			bondIP = getPortIPv4Addr("active-backup-bond", info)
			if bondIP == nil {
				return false
			}
			bondStatus = port.GetBondStatus()
			if bondStatus == nil || len(bondStatus.GetMembers()) != 2 {
				return false
			}
			activeMember = bondStatus.GetActiveMember()
			return activeMember != ""
		})))
	evetest.Checkpoint("bond-has-ip")
	t.Expect(bondStatus.GetMode()).To(Equal(evecommon.BondMode_BOND_MODE_ACTIVE_BACKUP))
	t.Expect(bondStatus.GetArpMonitor().GetEnabled()).To(BeTrue())
	t.Expect(bondStatus.GetArpMonitor().GetIpTargets()).To(Equal([]string{"172.20.20.1"}))
	t.Expect(bondStatus.GetArpMonitor().GetPollingInterval()).To(BeEquivalentTo(1000))
	t.Expect(bondStatus.GetMiiMonitor().GetEnabled()).To(BeFalse())
	netSubnet := evetest.IPSubnet("172.20.20.0/24") // from the network model
	t.Expect(netSubnet.Contains(bondIP)).To(BeTrue())
	evetest.Logger().Infof("Currently active member: %s", activeMember)
	t.Expect(activeMember).To(BeElementOf("ethernet0", "ethernet1"))

	// Verify we can SSH into EVE through the bond interface.
	var stdout string
	t.Eventually(func() error {
		var stderr string
		var err error
		stdout, stderr, err = device.RunShellScript("echo bond-ssh-ok", 0, 5*time.Second)
		if err != nil {
			return fmt.Errorf("SSH over bond failed: %s", stderr)
		}
		return nil
	}, time.Minute, 5*time.Second).Should(Succeed())
	t.Expect(stdout).To(ContainSubstring("bond-ssh-ok"))
	evetest.Checkpoint("ssh-over-bond-works")

	// Verify that bond config change is applied.
	bondConfig.ARPMonitor.Interval = 1500
	devConfig.UpdateBond(bondConfig)
	device.ApplyConfig(devConfig, true)
	evetest.Checkpoint("bond-config-updated")
	t.Eventually(devUpdates, timeout).Should(Receive(matchers.SatisfyPredicate(
		"Bond ARP monitor interval is updated",
		func(info *eveinfo.ZInfoDevice) bool {
			port := getDevicePort("active-backup-bond", info)
			if port == nil {
				return false
			}
			bondStatus = port.GetBondStatus()
			return bondStatus.GetArpMonitor().GetPollingInterval() == 1500
		})))

	// Simulate link failure on the active member by setting AdminUp=false
	// in the network model.
	// Map the active member logical label to the SDN port index.
	var activeMemberIdx int
	switch activeMember {
	case "ethernet0":
		activeMemberIdx = 0
	case "ethernet1":
		activeMemberIdx = 1
	}
	updatedModel := proto.Clone(netmodels.TwoMgmtPortsOneBridge).(*api.NetworkModel)
	updatedModel.Ports[activeMemberIdx].AdminUp = false
	evetest.UpdateNetworkModel(updatedModel)
	evetest.Checkpoint("active-member-link-down")

	// Eventually SSH to EVE should work again (after failover).
	t.Eventually(func() error {
		var stderr string
		var err error
		stdout, stderr, err = device.RunShellScript("echo failover-ok", 0, 5*time.Second)
		if err != nil {
			return fmt.Errorf("SSH after failover failed: %s", stderr)
		}
		return nil
	}, time.Minute, 5*time.Second).Should(Succeed())
	t.Expect(stdout).To(ContainSubstring("failover-ok"))
	evetest.Checkpoint("ssh-after-failover-works")

	// Verify the active member has changed and the failed member reports MII down.
	var newActiveMember string
	t.Eventually(devUpdates, timeout).Should(Receive(matchers.SatisfyPredicate(
		"Bond failover to different member with MII down on failed member",
		func(info *eveinfo.ZInfoDevice) bool {
			port := getDevicePort("active-backup-bond", info)
			if port == nil {
				return false
			}
			bs := port.GetBondStatus()
			if bs == nil {
				return false
			}
			newActiveMember = bs.GetActiveMember()
			if newActiveMember == "" || newActiveMember == activeMember {
				return false
			}
			// Check that the original active member (now failed) has MII down.
			for _, member := range bs.GetMembers() {
				if member.GetLogicallabel() == activeMember {
					return !member.GetMiiUp()
				}
			}
			return false
		})))
	evetest.Logger().Infof("Active member after failover: %s", newActiveMember)
	evetest.Checkpoint("failover-verified")

	// Verify that bond metrics report a non-zero link failure count
	// for the member that went down.
	t.Eventually(devMetrics, timeout).Should(Receive(matchers.SatisfyPredicate(
		"Bond metrics report link failure for failed member",
		func(metrics *evemetrics.DeviceMetric) bool {
			for _, bm := range metrics.GetBondMetrics() {
				if bm.GetLogicallabel() != "active-backup-bond" {
					continue
				}
				for _, member := range bm.GetMembers() {
					if member.GetLogicallabel() == activeMember &&
						member.GetLinkFailureCount() > 0 {
						return true
					}
				}
			}
			return false
		})))
	evetest.Checkpoint("bond-metrics-verified")

	// Restore the network model (bring the port back up).
	evetest.UpdateNetworkModel(netmodels.TwoMgmtPortsOneBridge)
	evetest.Checkpoint("link-restored")
}

func TestLACPBond(test *testing.T) {
	evetestT := evetest.Init(test)
	t := NewGomegaWithT(evetestT)
	defer evetest.Close()

	evetest.DefineTestParameters(
		evetest.HypervisorParameter(),
	)
	hypervisor := evetest.GetHypervisorParameterValue()

	devName := "edge-dev"
	requiredDevice := evetest.RequireEdgeDevice{
		Name:              devName,
		WithHypervisor:    hypervisor,
		DeviceReusePolicy: evetest.ResetDeviceConfig,
	}
	// Start with individual ports on a bridge so that EVE can onboard
	// using standalone interfaces. The SDN-side LACP bond will be
	// configured after EVE applies the bond config.
	// Note that we do this to avoid the bootstrapping challenge,
	// which is for LACP bonds already covered by TestBootstrapWithLACPBond.
	requiredNetModel := evetest.RequireNetworkModel{
		NetworkModel: netmodels.TwoMgmtPortsOneBridge,
	}
	evetest.Setup(requiredDevice, requiredNetModel)

	device := evetest.GetEdgeDevice(devName)
	evetest.Checkpoint("setup-done")

	devConfig := evetest.NewEdgeDeviceConfig(devName)
	dhcpNet := devConfig.AddNetwork(
		evetest.DHCPNetworkConfig{
			NetworkType: evecommon.NetworkType_V4,
		})
	devConfig.AddNetworkAdapter(
		evetest.NetworkAdapterConfig{
			LogicalLabel:  "ethernet0",
			PhysicalLabel: "eth0",
			InterfaceName: "eth0",
			Usage:         evecommon.PhyIoMemberUsage_PhyIoUsageMgmtAndApps,
		})
	devConfig.AddNetworkAdapter(
		evetest.NetworkAdapterConfig{
			LogicalLabel:  "ethernet1",
			PhysicalLabel: "eth1",
			InterfaceName: "eth1",
			Usage:         evecommon.PhyIoMemberUsage_PhyIoUsageMgmtAndApps,
		})
	devConfig.AddBond(
		evetest.BondConfig{
			LogicalLabel:  "lacp-bond",
			InterfaceName: "bond1",
			MemberLabels:  []string{"ethernet0", "ethernet1"},
			BondMode:      evecommon.BondMode_BOND_MODE_802_3AD,
			LACPRate:      evecommon.LacpRate_LACP_RATE_FAST,
			MIIMonitor: &eveconfig.MIIMonitor{
				Interval: 100,
			},
			NetworkUUID: dhcpNet,
			Usage:       evecommon.PhyIoMemberUsage_PhyIoUsageMgmtAndApps,
		})

	devUpdates, stopDevWatch := device.WatchDeviceInfo()
	defer stopDevWatch()
	devMetrics, stopDevMetricsWatch := device.WatchDeviceMetrics()
	defer stopDevMetricsWatch()
	device.ApplyConfig(devConfig, true)
	evetest.Checkpoint("config-applied")

	// Now switch the SDN side to LACP so both ends can negotiate.
	// EVE has already created its LACP bond from the applied config;
	// the SDN side needs a matching LACP bond for negotiation to succeed.
	evetest.UpdateNetworkModel(netmodels.TwoMgmtPortsWithLACPBond)
	evetest.Checkpoint("sdn-lacp-enabled")

	// Wait for device info to report the LACP bond interface with an IP address,
	// no errors, and LACP status.
	timeout := 5 * time.Minute
	var bondIP net.IP
	var bondStatus *eveinfo.BondStatus
	t.Eventually(devUpdates, timeout).Should(Receive(matchers.SatisfyPredicate(
		"LACP bond has IP, no errors and reports LACP status",
		func(info *eveinfo.ZInfoDevice) bool {
			port := getDevicePort("lacp-bond", info)
			if port == nil {
				return false
			}
			if port.GetErr() != nil && port.GetErr().GetDescription() != "" {
				return false
			}
			bondIP = getPortIPv4Addr("lacp-bond", info)
			if bondIP == nil {
				return false
			}
			bondStatus = port.GetBondStatus()
			if bondStatus == nil {
				return false
			}
			lacpStatus := bondStatus.GetLacp()
			if lacpStatus == nil {
				return false
			}
			// Ensure LACP negotiation has completed — partner MAC must be
			// a valid non-zero address (not 00:00:00:00:00:00).
			partnerMac, err := net.ParseMAC(lacpStatus.GetPartnerMac())
			if err != nil {
				return false
			}
			// Check not all zeros.
			for _, b := range partnerMac {
				if b != 0 {
					return true
				}
			}
			return false
		})))
	evetest.Checkpoint("lacp-bond-has-ip")
	netSubnet := evetest.IPSubnet("172.20.20.0/24")
	t.Expect(netSubnet.Contains(bondIP)).To(BeTrue())
	t.Expect(bondStatus.GetMode()).To(Equal(evecommon.BondMode_BOND_MODE_802_3AD))
	t.Expect(bondStatus.GetArpMonitor().GetEnabled()).To(BeFalse())
	t.Expect(bondStatus.GetMiiMonitor().GetEnabled()).To(BeTrue())
	t.Expect(bondStatus.GetMiiMonitor().GetPollingInterval()).To(BeEquivalentTo(100))
	t.Expect(bondStatus.GetMiiMonitor().GetUpdelay()).To(BeZero())
	t.Expect(bondStatus.GetMiiMonitor().GetDowndelay()).To(BeZero())
	activeAggID := bondStatus.GetLacp().GetActiveAggregatorId()
	t.Expect(activeAggID).ToNot(BeZero())
	t.Expect(bondStatus.GetLacp().GetLacpRate()).To(Equal(
		evecommon.LacpRate_LACP_RATE_FAST))
	// Verify all members are in the active aggregator (no split aggregation).
	for _, member := range bondStatus.GetMembers() {
		t.Expect(member.GetLogicallabel()).To(BeElementOf("ethernet0", "ethernet1"))
		t.Expect(member.GetMiiUp()).To(BeTrue())
		t.Expect(member.GetLacp().GetAggregatorId()).To(Equal(activeAggID))
	}

	// Verify we can SSH into EVE through the LACP bond.
	var stdout string
	t.Eventually(func() error {
		var stderr string
		var err error
		stdout, stderr, err = device.RunShellScript("echo lacp-ssh-ok", 0, 5*time.Second)
		if err != nil {
			return fmt.Errorf("SSH over LACP bond failed: %s", stderr)
		}
		return nil
	}, time.Minute, 5*time.Second).Should(Succeed())
	t.Expect(stdout).To(ContainSubstring("lacp-ssh-ok"))
	evetest.Checkpoint("ssh-over-lacp-bond-works")

	// Verify that bond metrics are reported with both members and LACP sub-metrics.
	t.Eventually(devMetrics, timeout).Should(Receive(matchers.SatisfyPredicate(
		"Bond metrics report both members with LACP metrics",
		func(metrics *evemetrics.DeviceMetric) bool {
			for _, bm := range metrics.GetBondMetrics() {
				if bm.GetLogicallabel() != "lacp-bond" {
					continue
				}
				var memberLabels []string
				for _, member := range bm.GetMembers() {
					if member.GetLacp() == nil {
						return false
					}
					memberLabels = append(memberLabels, member.GetLogicallabel())
				}
				return generics.EqualSets(memberLabels,
					[]string{"ethernet0", "ethernet1"})
			}
			return false
		})))
	evetest.Checkpoint("lacp-metrics-verified")
}
