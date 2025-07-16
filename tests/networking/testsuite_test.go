package networking_test

import (
	"testing"

	"github.com/lf-edge/eve/evetest"
)

func TestBootstrapSuite(test *testing.T) {
	evetest.Init(test)
	defer evetest.Close()

	// Define parameters for the entire test suite.
	// Default values set here override the defaults used by individual tests
	// for the same parameters.
	evetest.DefineTestParameters(
		evetest.HypervisorParameter(),
	)

	// This below will be implemented using t.Run()
	// Note that evetest.Close needs to behave differently when test is part of
	// a test suite and there are more tests to execute.
	evetest.RunTestSuite(
		evetest.TestCase{
			Test: TestBootstrapWithLastResort,
			Variants: []evetest.TestVariant{
				{
					Name: "TestBootstrapWithLastResortDisabled",
					Parameters: []evetest.TestParameterValue{
						{Key: lastResortParamKey, Value: false},
					},
				},
				{
					Name: "TestBootstrapWithLastResortEnabled",
					Parameters: []evetest.TestParameterValue{
						{Key: lastResortParamKey, Value: true},
					},
				},
			},
		},
		evetest.TestCase{
			Test: TestBootstrapWithStaticIP,
			Variants: []evetest.TestVariant{
				{
					Name: "TestBootstrapWithStaticIP",
					Parameters: []evetest.TestParameterValue{
						{Key: useOverrideJSONParamKey, Value: false},
					},
				},
				{
					Name: "TestBootstrapWithStaticIPUsingOverrideJSON",
					Parameters: []evetest.TestParameterValue{
						{Key: useOverrideJSONParamKey, Value: true},
					},
				},
			},
		},
		evetest.TestCase{
			Test: TestBootstrapWithProxy,
			Variants: []evetest.TestVariant{
				{
					Name: "TestBootstrapWithManualProxyConfig",
					Parameters: []evetest.TestParameterValue{
						{Key: useOverrideJSONParamKey, Value: false},
						{Key: proxyConfigTypeParamKey, Value: ProxyConfigManual},
					},
				},
				{
					Name: "TestBootstrapWithManualProxyConfigUsingOverrideJSON",
					Parameters: []evetest.TestParameterValue{
						{Key: useOverrideJSONParamKey, Value: true},
						{Key: proxyConfigTypeParamKey, Value: ProxyConfigManual},
					},
				},
				{
					Name: "TestBootstrapWithTransparentProxy",
					Parameters: []evetest.TestParameterValue{
						{Key: useOverrideJSONParamKey, Value: false},
						{Key: proxyConfigTypeParamKey, Value: ProxyConfigTransparent},
					},
				},
				{
					Name: "TestBootstrapWithTransparentProxyUsingOverrideJSON",
					Parameters: []evetest.TestParameterValue{
						{Key: useOverrideJSONParamKey, Value: true},
						{Key: proxyConfigTypeParamKey, Value: ProxyConfigTransparent},
					},
				},
				{
					Name: "TestBootstrapWithAutoDiscoveredProxy",
					Parameters: []evetest.TestParameterValue{
						{Key: useOverrideJSONParamKey, Value: false},
						{Key: proxyConfigTypeParamKey, Value: ProxyConfigAutoDiscovery},
					},
				},
				{
					Name: "TestBootstrapWithAutoDiscoveredProxyUsingOverrideJSON",
					Parameters: []evetest.TestParameterValue{
						{Key: useOverrideJSONParamKey, Value: true},
						{Key: proxyConfigTypeParamKey, Value: ProxyConfigAutoDiscovery},
					},
				},
			},
		},
	)
}

func TestDeviceConnectivitySuite(test *testing.T) {
	evetest.Init(test)
	defer evetest.Close()

	// Define parameters for the entire test suite.
	evetest.DefineTestParameters(
		evetest.HypervisorParameter(),
	)

	evetest.RunTestSuite(
		evetest.TestCase{
			Test: TestPNAC,
			Variants: []evetest.TestVariant{
				{
					Name: "TestPNACWithoutProxy",
					Parameters: []evetest.TestParameterValue{
						{Key: requireSCEPProxyParamKey, Value: false},
					},
				},
				{
					Name: "TestPNACWithProxy",
					Parameters: []evetest.TestParameterValue{
						{Key: requireSCEPProxyParamKey, Value: true},
					},
				},
			},
		},
		evetest.TestCase{
			Test: TestDHCPIPv4Only,
		},
		evetest.TestCase{
			Test: TestStaticIPv4Only,
		},
		evetest.TestCase{
			Test: TestDNSFunctionality,
		},
		evetest.TestCase{
			Test: TestPortFailover,
		},
		evetest.TestCase{
			Test: TestNetworkConfigFallback,
		},
		evetest.TestCase{
			Test: TestIntermittentConnectivity,
		},
		evetest.TestCase{
			Test: TestDeviceIPv6Connectivity,
		},
		evetest.TestCase{
			Test: TestDeviceNTPConfig,
		},
		evetest.TestCase{
			Test: TestVLANSubinterfaces,
		},
		evetest.TestCase{
			Test: TestCellularConnectivity,
		},
		evetest.TestCase{
			Test: TestWifiConnectivity,
		},
	)
}

func TestApplicationConnectivitySuite(test *testing.T) {
	evetest.Init(test)
	defer evetest.Close()

	// Define parameters for the entire test suite.
	evetest.DefineTestParameters(
		evetest.HypervisorParameter(),
	)

	evetest.RunTestSuite(
		evetest.TestCase{
			Test: TestLocalNI,
		},
		evetest.TestCase{
			Test: TestSwitchNI,
		},
		evetest.TestCase{
			Test: TestFlowLog,
		},
		evetest.TestCase{
			Test: TestLocalNetInstanceACLs,
		},
		evetest.TestCase{
			Test: TestSwitchNetInstanceACLs,
		},
		evetest.TestCase{
			Test: TestApplicationIPv6Connectivity,
		},
		evetest.TestCase{
			Test: TestApplicationNTPConfig,
		},
		evetest.TestCase{
			Test: TestPropagatedRoutes,
		},
		evetest.TestCase{
			Test: TestLocalNIWithMultiplePorts,
		},
		evetest.TestCase{
			Test: TestApplicationGateway,
		},
		evetest.TestCase{
			Test: TestSwitchNIWithMultiplePorts,
		},
		evetest.TestCase{
			Test: TestAccessVLANs,
		},
		evetest.TestCase{
			Test: TestNetworkAdapterPassthrough,
		},
	)
}

func TestDatastoreSuite(test *testing.T) {
	evetest.Init(test)
	defer evetest.Close()

	evetest.RunTestSuite(
		evetest.TestCase{
			Test: TestHTTPDatastore,
		},
		evetest.TestCase{
			Test: TestHTTPSDatastore,
		},
		evetest.TestCase{
			Test: TestAWSDatastore,
		},
		evetest.TestCase{
			Test: TestSFTPDatastore,
		},
		evetest.TestCase{
			Test: TestAzureDatastore,
		},
		evetest.TestCase{
			Test: TestContainerRegistry,
		},
	)
}
