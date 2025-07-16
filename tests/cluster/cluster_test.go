package cluster_test

import (
	"fmt"
	"net"
	"testing"

	"github.com/lf-edge/eve-api/go/evecommon"
	"github.com/lf-edge/eve/evetest"
)

// TODO : remove this test eventually. It is just a stepping stone towards clustering.
func TestMultipleEVEDevices(test *testing.T) {
	t := evetest.Init(test)
	defer evetest.Close()

	// Define configurable parameters available for the test.
	evetest.DefineTestParameters(
		evetest.HypervisorParameter(),
	)

	// Get parameter values set for this test execution.
	hypervisor := evetest.GetHypervisorParameterValue()

	// Set up the test harness and specify the test prerequisites.
	var requiredDevices []evetest.Requirement
	for i := 0; i < 3; i++ {
		devName := fmt.Sprintf("edge-dev%d", i+1)
		requiredDevices = append(requiredDevices,
			evetest.RequireEdgeDevice{
				Name:              devName,
				WithHypervisor:    hypervisor,
				DeviceReusePolicy: evetest.ResetDeviceConfig,
			})
	}

	// Set up the test harness. Let evetest to use the default network model.
	evetest.Setup(requiredDevices...)
	evetest.Checkpoint("setup-done")

	// Apply the initial config for each device.
	evetest.RunParallel(3, func(i int) {
		devName := fmt.Sprintf("edge-dev%d", i+1)
		devConfig := evetest.NewEdgeDeviceConfig(devName)
		staticNet := devConfig.AddNetwork(
			evetest.StaticNetworkConfig{
				NetworkType: evecommon.NetworkType_V4Only,
				// IP addressing from the default network model:
				Subnet:     evetest.IPSubnet("172.20.20.0/24"),
				Gateway:    evetest.IPAddress("172.20.20.1"),
				DNSServers: []net.IP{evetest.IPAddress("10.16.16.25")},
			})
		devConfig.AddNetworkAdapter(
			evetest.NetworkAdapterConfig{
				LogicalLabel:  "ethernet0",
				PhysicalLabel: "eth0",
				InterfaceName: "eth0",
				NetworkUUID:   staticNet,
				Usage:         evecommon.PhyIoMemberUsage_PhyIoUsageMgmtAndApps,
				StaticIP:      evetest.IPAddress(fmt.Sprintf("172.20.20.%d", 100+i)),
			})
		device := evetest.GetEdgeDevice(devName)
		device.ApplyConfig(devConfig, true)
	})
	evetest.Checkpoint("config-applied")

	t.Fatalf("fake failure")
}

func TestSingleNodeCluster(test *testing.T) {
	// TODO
}

func TestThreeNodesCluster(test *testing.T) {
	// TODO
}
