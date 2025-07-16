// Copyright (c) 2026 Zededa, Inc.
// SPDX-License-Identifier: Apache-2.0

package lps_test

import (
	"fmt"
	"strings"
	"testing"
	"time"

	. "github.com/onsi/gomega"

	uuid "github.com/satori/go.uuid"
	"google.golang.org/protobuf/encoding/protojson"

	"github.com/lf-edge/eve-api/go/evecommon"
	"github.com/lf-edge/eve-api/go/profile"
	"github.com/lf-edge/eve/evetest"
	"github.com/lf-edge/eve/evetest/tests/netmodels"
)

const (
	lpsServerToken        = "evetest-lps-token"
	lpsLocalBaseURL       = "http://localhost:8888"
	lpsManageURL          = lpsLocalBaseURL + "/manage/v1"
	lpsManageNetConfigURL = lpsManageURL + "/network-config"
)

// TestNetworkLocalChanges tests the LPS /api/v1/network endpoint,
// verifying that local network configuration changes are accepted only
// for adapters with AllowLocalModifications enabled.
//
// Scenario:
//  1. Deploy LPS app on a device with two management ports (eth0, eth1).
//  2. Initially only eth1 has AllowLocalModifications enabled.
//  3. Submit local network config: DNS override for eth0, MTU override for eth1.
//  4. Verify eth0 changes are rejected, eth1 changes are applied (MTU=9000).
//  5. Enable AllowLocalModifications for eth0 and re-apply config.
//  6. Verify eth0 changes are now applied (custom DNS).
//  7. Submit empty config to revert all local changes.
//  8. Verify both ports revert to controller config (default DNS, MTU=1500).
func TestNetworkLocalChanges(test *testing.T) {
	evetestT := evetest.Init(test)
	t := NewGomegaWithT(evetestT)
	defer evetest.Close()

	// Define configurable parameters available for the test.
	evetest.DefineTestParameters(
		evetest.HypervisorParameter(),
	)

	// Get parameter values set for this test execution.
	hypervisor := evetest.GetHypervisorParameterValue()

	// Set up the test harness and specify the test prerequisites.
	devName := "edge-dev"
	evetest.Setup(
		evetest.RequireEdgeDevice{
			Name:              devName,
			WithHypervisor:    hypervisor,
			DeviceReusePolicy: evetest.ResetDeviceConfig,
		},
		evetest.RequireNetworkModel{
			NetworkModel: netmodels.TwoMgmtPorts,
		},
	)
	evetest.Checkpoint("setup-done")

	// Build initial device configuration with two management ports.
	// Only eth1 has AllowLocalModifications enabled at first.
	devConfig := evetest.NewEdgeDeviceConfig(devName)
	dhcpNet0 := devConfig.AddNetwork(
		evetest.DHCPNetworkConfig{
			NetworkType: evecommon.NetworkType_V4Only,
		})
	dhcpNet1 := devConfig.AddNetwork(
		evetest.DHCPNetworkConfig{
			NetworkType: evecommon.NetworkType_V4Only,
		})
	devConfig.AddNetworkAdapter(
		evetest.NetworkAdapterConfig{
			LogicalLabel:  "ethernet0",
			PhysicalLabel: "eth0",
			InterfaceName: "eth0",
			NetworkUUID:   dhcpNet0,
			Usage:         evecommon.PhyIoMemberUsage_PhyIoUsageMgmtAndApps,
		})
	devConfig.AddNetworkAdapter(
		evetest.NetworkAdapterConfig{
			LogicalLabel:            "ethernet1",
			PhysicalLabel:           "eth1",
			InterfaceName:           "eth1",
			NetworkUUID:             dhcpNet1,
			Usage:                   evecommon.PhyIoMemberUsage_PhyIoUsageMgmtAndApps,
			AllowLocalModifications: true,
		})

	// Deploy the LPS application, connected to a local NI with port forwarding.
	niUUID := devConfig.AddNetworkInstance(evetest.LocalNetworkInstanceConfig{
		DisplayName: "local-ni",
		Port:        "ethernet0",
		Subnet:      evetest.IPSubnet("10.11.12.0/24"),
		Gateway:     evetest.IPAddress("10.11.12.1"),
		MTU:         1500,
	})
	appUUID := devConfig.AddApplication(evetest.ApplicationInstanceConfig{
		DisplayName: "lps-app",
		Activate:    true,
		Image: evetest.DockerContainer{
			ImageName: "milan4zededa/evetest-lps",
			Tag:       "1.0",
		},
		CPUs:        1,
		MemoryBytes: 512 * evetest.MB,
		NetworkAdapters: []evetest.AppNetworkAdapter{
			evetest.VirtualNetworkAdapter{
				LogicalLabel:        "vif0",
				NetworkInstanceUUID: niUUID,
				PortFwdRules: []evetest.PortFwdRule{
					{
						// SSH access
						Protocol:     evetest.NetworkProtocolTCP,
						EdgeNodePort: 2222,
						AppPort:      22,
					},
					{
						// For developers troubleshooting LPS who need access to the UI:
						// Pause test after LPS is deployed (at checkpoint
						// "lps-app-is-running" or later), then run:
						// $ evetest eve portfwd 8888:8888
						// And open http://localhost:8888 in your browser.
						Protocol:     evetest.NetworkProtocolTCP,
						EdgeNodePort: 8888,
						AppPort:      8888,
					},
				},
				ACLAllowRules: []evetest.ACLAllowRule{
					{
						Protocol:     evetest.NetworkProtocolAny,
						RemoteSubnet: evetest.IPSubnet("0.0.0.0/0"),
					},
				},
			},
		},
	})

	device := evetest.GetEdgeDevice(devName)
	device.ApplyConfig(devConfig, true)
	evetest.Checkpoint("initial-config-applied")

	device.WaitUntilAppIsRunning(appUUID, 10*time.Minute)
	evetest.Checkpoint("lps-app-is-running")

	// Wait for the LPS app to become reachable via SSH.
	appAuth := evetest.UsernamePasswordAuth{
		Username: "root",
		Password: "testpassword",
	}
	log := evetest.Logger()
	sshTimeout := 20 * time.Second
	polling := 5 * time.Second
	timeout := 3 * time.Minute
	log.Infof("Waiting for LPS app SSH to become reachable...")
	t.Eventually(func(t Gomega) {
		output, _, err := device.RunShellScriptInsideApp(appUUID, appAuth,
			"echo hello", sshTimeout, 0)
		t.Expect(err).ToNot(HaveOccurred())
		t.Expect(output).To(ContainSubstring("hello"))
	}, timeout, polling).Should(Succeed())
	evetest.Checkpoint("lps-app-ssh-reachable")

	// Configure the server token via the LPS management API.
	_, _, err := device.RunShellScriptInsideApp(appUUID, appAuth,
		fmt.Sprintf(`curl -sS -X PUT -d '{"token":"%s"}' `+lpsManageURL+`/token`,
			lpsServerToken), sshTimeout, 0)
	t.Expect(err).ToNot(HaveOccurred())

	// Get the application's IP (LPS is reachable at this IP from EVE).
	output, _, err := device.RunShellScriptInsideApp(appUUID, appAuth,
		"hostname -I | awk '{print $1}'", sshTimeout, 0)
	t.Expect(err).ToNot(HaveOccurred())
	lpsIP := strings.TrimSpace(output)
	log.Infof("LPS app IP: %s", lpsIP)

	// Configure EVE to use the LPS.
	devConfig.SetLPS(evetest.LPSConfig{
		Address:   fmt.Sprintf("%s:8888", lpsIP),
		AuthToken: lpsServerToken,
	})
	device.ApplyConfig(devConfig, true)
	evetest.Checkpoint("lps-configured")

	// Wait for EVE to start posting network info to the LPS.
	configChangeTimeout := 2 * time.Minute
	log.Infof("Waiting for LPS to receive network info from EVE...")
	t.Eventually(func(t Gomega) {
		output, _, err := device.RunShellScriptInsideApp(appUUID, appAuth,
			"curl -sS -o /dev/null -w '%{http_code}' "+lpsManageURL+"/network",
			sshTimeout, 0)
		t.Expect(err).ToNot(HaveOccurred())
		t.Expect(output).To(Equal("200"))
	}, configChangeTimeout, polling).Should(Succeed())
	evetest.Checkpoint("lps-receiving-network-info")

	// Apply local config: DNS override for eth0, MTU override for eth1.
	localNetworkConfig := fmt.Sprintf(`{
		"serverToken": "%s",
		"ports": [
			{
				"logicalLabel": "ethernet0",
				"useDhcp": true,
				"dnsServers": ["8.8.8.8", "1.1.1.1"]
			},
			{
				"logicalLabel": "ethernet1",
				"useDhcp": true,
				"mtu": 9000
			}
		]
	}`, lpsServerToken)
	log.Infof("Submitting local network config via LPS management API")
	_, _, err = device.RunShellScriptInsideApp(appUUID, appAuth,
		fmt.Sprintf(`curl -sS -X PUT -H 'Content-Type: application/json' -d '%s' %s`,
			localNetworkConfig, lpsManageNetConfigURL), sshTimeout, 0)
	t.Expect(err).ToNot(HaveOccurred())
	evetest.Checkpoint("local-config-submitted")

	// Verify eth0 changes are rejected, eth1 changes are applied.
	// Wait until the NetworkInfo posted by EVE to LPS shows that the local config
	// for eth1 was applied (MTU=9000) and eth0 was rejected (not permitted).
	log.Infof("Verifying eth1 local changes are applied and eth0 is rejected...")
	t.Eventually(func(t Gomega) {
		netInfo := getLPSNetworkInfo(t, device, appUUID, appAuth, sshTimeout)
		t.Expect(netInfo.LocalConfig).ToNot(BeNil())
		for _, port := range netInfo.LocalConfig.Ports {
			switch port.LogicalLabel {
			case "ethernet0":
				t.Expect(port.ErrorMessage).To(
					ContainSubstring("not permitted"),
					"eth0 local config should be rejected")
				t.Expect(port.ConfigApplied).To(BeFalse(),
					"eth0 local config should not be applied")
			case "ethernet1":
				t.Expect(port.ErrorMessage).To(BeEmpty(),
					"eth1 local config should have no error")
				t.Expect(port.ConfigApplied).To(BeTrue(),
					"eth1 local config should be applied")
				t.Expect(port.Mtu).To(Equal(uint32(9000)))
			}
		}

		// Verify on the EVE device itself: eth0 should NOT have custom DNS,
		// eth1 should have MTU 9000.
		output, _, err = device.RunShellScript(
			"cat /etc/resolv.conf", sshTimeout, 0)
		t.Expect(err).ToNot(HaveOccurred())
		t.Expect(output).ToNot(ContainSubstring("8.8.8.8"),
			"eth0 DNS should not be applied")

		output, _, err = device.RunShellScript(
			"cat /sys/class/net/eth1/mtu", sshTimeout, 0)
		t.Expect(err).ToNot(HaveOccurred())
		t.Expect(strings.TrimSpace(output)).To(Equal("9000"),
			"eth1 MTU should be 9000")
	}, configChangeTimeout, polling).Should(Succeed())

	// Enable AllowLocalModifications for eth0
	log.Infof("Enabling AllowLocalModifications for eth0...")
	devConfig.UpdateNetworkAdapter(
		evetest.NetworkAdapterConfig{
			LogicalLabel:            "ethernet0",
			PhysicalLabel:           "eth0",
			InterfaceName:           "eth0",
			NetworkUUID:             dhcpNet0,
			Usage:                   evecommon.PhyIoMemberUsage_PhyIoUsageMgmtAndApps,
			AllowLocalModifications: true,
		})
	device.ApplyConfig(devConfig, true)
	evetest.Checkpoint("eth0-allow-local-mods-enabled")

	// Verify eth0 changes are now applied
	log.Infof("Verifying eth0 local changes are now applied...")
	t.Eventually(func(t Gomega) {
		netInfo := getLPSNetworkInfo(t, device, appUUID, appAuth, sshTimeout)
		t.Expect(netInfo.LocalConfig).ToNot(BeNil())
		for _, port := range netInfo.LocalConfig.Ports {
			if port.LogicalLabel == "ethernet0" {
				t.Expect(port.ErrorMessage).To(BeEmpty(),
					"eth0 local config should now have no error")
				t.Expect(port.ConfigApplied).To(BeTrue(),
					"eth0 local config should now be applied")
			}
		}
		// Verify on the EVE device: eth0 should now have custom DNS.
		output, _, err = device.RunShellScript(
			"cat /etc/resolv.conf", sshTimeout, 0)
		t.Expect(err).ToNot(HaveOccurred())
		t.Expect(output).To(ContainSubstring("8.8.8.8"),
			"eth0 DNS should now be applied")
		t.Expect(output).To(ContainSubstring("1.1.1.1"),
			"eth0 DNS should now be applied")
	}, configChangeTimeout, polling).Should(Succeed())

	// Revert local changes by submitting empty config
	log.Infof("Reverting local network config by submitting empty config...")
	emptyConfig := fmt.Sprintf(`{
		"serverToken": "%s",
		"ports": []
	}`, lpsServerToken)
	_, _, err = device.RunShellScriptInsideApp(appUUID, appAuth,
		fmt.Sprintf(`curl -sS -X PUT -H 'Content-Type: application/json' -d '%s' %s`,
			emptyConfig, lpsManageNetConfigURL), sshTimeout, 0)
	t.Expect(err).ToNot(HaveOccurred())
	evetest.Checkpoint("local-changes-reverted")

	// Verify both ports revert to controller config
	log.Infof("Verifying both ports reverted to controller config...")
	t.Eventually(func(t Gomega) {
		netInfo := getLPSNetworkInfo(t, device, appUUID, appAuth, sshTimeout)
		// After submitting empty config, LocalConfig should have no ports
		// or all ports should show controller config applied.
		for _, port := range netInfo.LatestConfig {
			switch port.LogicalLabel {
			case "ethernet0":
				t.Expect(port.ConfigApplied).To(BeTrue(),
					"eth0 should have controller config applied")
			case "ethernet1":
				t.Expect(port.ConfigApplied).To(BeTrue(),
					"eth1 should have controller config applied")
				t.Expect(port.Mtu).ToNot(Equal(uint32(9000)),
					"eth1 MTU should have reverted from 9000")
			}
		}

		// Verify on the EVE device: DNS reverted, MTU back to 1500.
		output, _, err = device.RunShellScript(
			"cat /etc/resolv.conf", sshTimeout, 0)
		t.Expect(err).ToNot(HaveOccurred())
		t.Expect(output).ToNot(ContainSubstring("8.8.8.8"),
			"eth0 DNS should have reverted")
		t.Expect(output).ToNot(ContainSubstring("1.1.1.1"),
			"eth0 DNS should have reverted")

		output, _, err = device.RunShellScript(
			"cat /sys/class/net/eth1/mtu", sshTimeout, 0)
		t.Expect(err).ToNot(HaveOccurred())
		t.Expect(strings.TrimSpace(output)).To(Equal("1500"),
			"eth1 MTU should have reverted to 1500")
	}, configChangeTimeout, polling).Should(Succeed())
}

// getLPSNetworkInfo retrieves and parses the network info that EVE posted to the LPS.
func getLPSNetworkInfo(t Gomega, device *evetest.EdgeDevice,
	appUUID uuid.UUID, auth evetest.AuthMethod,
	timeout time.Duration) *profile.NetworkInfo {
	output, _, err := device.RunShellScriptInsideApp(appUUID, auth,
		"curl -sS "+lpsManageURL+"/network", timeout, 0)
	t.Expect(err).ToNot(HaveOccurred())
	var netInfo profile.NetworkInfo
	err = protojson.Unmarshal([]byte(output), &netInfo)
	t.Expect(err).ToNot(HaveOccurred())
	return &netInfo
}
