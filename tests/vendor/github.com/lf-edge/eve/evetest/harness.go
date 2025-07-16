package evetest

import (
	"context"
	"crypto/ecdsa"
	"crypto/rsa"
	"crypto/x509"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math/big"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"testing"
	"time"

	eveinfo "github.com/lf-edge/eve-api/go/info"
	"github.com/lf-edge/eve/evetest/constants"
	"github.com/lf-edge/eve/evetest/controller"
	api "github.com/lf-edge/eve/evetest/grpcapi/go"
	"github.com/lf-edge/eve/evetest/logger"
	"github.com/lf-edge/eve/evetest/utils"
	pillartypes "github.com/lf-edge/eve/pkg/pillar/types"
	"github.com/lf-edge/eve/pkg/pillar/utils/generics"
	uuid "github.com/satori/go.uuid"
	"github.com/sirupsen/logrus"
	"github.com/spf13/viper"
	"github.com/vishvananda/netlink"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/protobuf/proto"
)

const (
	// Timeout for establishing a connection to the Broker gRPC service.
	brokerConnectTimeout = 5 * time.Minute

	// Timeout for closing a broker connection, including tearing down
	// all resources associated with the client.
	brokerCloseTimeout = time.Minute

	// Timeout for the broker to build an EVE VM image.
	brokerBuildImageTimeout = 5 * time.Minute

	// Timeout for uploading an EVE Docker image to the broker.
	brokerPushEVEImageTimeout = 10 * time.Minute

	// Timeout for the broker to set up requested devices.
	// This includes starting the SDN VM and waiting for it to acquire
	// IP addresses via DHCP.
	brokerSetupDevicesTimeout = 5 * time.Minute

	// Timeout for powering on an EVE VM (not for waiting for it to boot).
	brokerPowerOnEVEDeviceTimeout = time.Minute

	// Timeout for triggering an EVE VM reboot (not for waiting for it to boot).
	brokerRebootEVEDeviceTimeout = time.Minute

	// Timeout for the broker to tear-down all devices.
	brokerTeardownDevicesTimeout = 2 * time.Minute

	// Timeout for retrieving the full console output for a single EVE device.
	brokerGetConsoleOutputTimeout = time.Minute

	// Timeout for a device to boot and complete onboarding to the controller.
	deviceOnboardTimeout = 10 * time.Minute

	// Timeout for a device to attest (or at least to publish its certificates).
	deviceAttestTimeout = 2 * time.Minute

	// Timeout for a device to fetch the latest configuration from Adam.
	deviceApplyConfigTimeout = 2 * time.Minute

	// Timeout for a device to be removed from the Adam controller.
	deviceRemoveTimeout = 20 * time.Second

	// Timeout for EVE device to perform reboot.
	deviceRebootTimeout = 3 * time.Minute

	// Timeout for establishing a connection to the SDN gRPC service.
	sdnConnectTimeout = 5 * time.Minute

	// Timeout for SDN to test Internet connectivity.
	sdnTestInternetConnTimeout = time.Minute

	// Timeout for updating the SDN network model.
	sdnApplyNetModelTimeout = time.Minute

	// Timeout for submitting device configuration to Adam.
	adamApplyConfigTimeout = 20 * time.Second

	// Timeout for fetching all device logs.
	gatherLogsTimeout = 1 * time.Minute

	// Timeout for fetching all published device info messages.
	gatherInfoMsgsTimeout = 1 * time.Minute

	// Timeout for automatically running collect-info.sh on an EVE device and
	// downloading the tarball. This does not apply to `evetest eve collect-info`,
	// where the user controls command termination.
	collectInfoTimeout = 5 * time.Minute
)

const (
	controllerIntfName = "controller"

	sdnTunName = "sdn-tun"
	sdnTunMTU  = 1500
)

// Used as constants.
var (
	// IPv4 addressing for the controller and the SDN tunnel uses subnets from
	// 240.0.0.0/4. This address space is reserved for future use and is not
	// routable on the public Internet. Using it avoids conflicts with commonly
	// used private IPv4 ranges (RFC 1918) that may already be present on the host
	// or within test environments.
	//
	// For IPv6, randomly generated Unique Local Address (ULA) subnets are used.
	// The probability of collisions with other ULA prefixes used across EVE
	// tests or on the host system is negligibly small.
	sdnTunContainerIPv4 = net.ParseIP("250.250.250.1").To4()
	sdnTunVMIPv4        = net.ParseIP("250.250.250.2").To4()

	sdnTunContainerIPv6 = net.ParseIP("fdd8:bec2:f2b1:1000::1").To16()
	sdnTunVMIPv6        = net.ParseIP("fdd8:bec2:f2b1:1000::2").To16()

	controllerIPv4 = net.ParseIP("245.245.245.245").To4()
	controllerIPv6 = net.ParseIP("fd24:1ac2:e355::1").To16()
)

const (
	sdnTunIPv4Prefix = "/30"
	sdnTunIPv6Prefix = "/64"
)

// TestHarness is the central runtime state for executing tests and test suites.
// It owns the gRPC server lifecycle and tracks the currently executing test
// and optional test-suite context.
type TestHarness struct {
	api.UnimplementedEvetestServer

	t         *T
	log       *logrus.Logger
	userLog   *logrus.Logger
	brokerLog *logrus.Logger

	artifactDir string

	dockerIntf     string // typically eth0
	dockerIntfIdx  int
	dockerIntfIPv4 net.IP
	dockerGwIPv4   net.IP
	dockerGwIPv6   net.IP

	// Test being executed
	testM sync.Mutex
	test  testState
	suite *testSuiteState // nil if running a single test

	// Go routines management
	ctx    context.Context
	cancel context.CancelFunc
	wg     sync.WaitGroup

	// gRPC server
	grpcServer *grpc.Server
	listener   net.Listener

	// CA
	caCert *x509.Certificate
	caKey  *rsa.PrivateKey

	// Broker
	brokerInContainer bool
	brokerConn        *grpc.ClientConn
	brokerClient      api.BrokerClient
	brokerClientID    string

	// Controller
	adamClient   *controller.AdamClient
	adamStatusCh chan controller.AdamState

	// SDN
	sdnConn   *grpc.ClientConn
	sdnClient api.SDNClient

	// Tunnel between SDN and the evetest container
	sdnTunCtx    context.Context
	sdnTunCancel context.CancelFunc
	sdnTunWG     sync.WaitGroup
	sdnTunStream grpc.BidiStreamingClient[
		api.ConnectTunnelToSDNRequest, api.ConnectTunnelToSDNResponse]
	sdnTunIntf *os.File
	sdnTunIdx  int

	// Network model.
	netModelM sync.Mutex
	netModel  *api.NetworkModel

	// EVE devices
	devicesM sync.Mutex
	devices  map[string]*deviceState // key: device name

	// TCP proxies
	tcpProxiesM sync.Mutex
	tcpProxies  map[int]*tcpProxy // key: port number

	// Checkpoint / Failure
	checkpointM        sync.Mutex
	pauseAtCheckpoint  string
	pausedAtCheckpoint string
	pauseOnFailure     bool
	pausedOnFailure    string
	resume             chan struct{}
}

// testState contains per-test execution state, including parameter
// definitions, resolved parameter values, and initialization tracking.
type testState struct {
	// name is the name of the currently executing test or variant.
	name string

	// paramDefs are the parameter definitions available to the test.
	paramDefs []TestParameterDefinition

	// paramVals are the concrete parameter values applied to the test.
	paramVals []TestParameterValue

	// Directory where artifacts specific to this test should be saved.
	artifactDir string

	// This channel is closed when the test is marked as failed.
	failedCh chan struct{}

	// initialized indicates whether Init has been called for this test.
	initialized bool

	// This is enabled after running RunTestSuite from inside the test.
	executedTestSuite bool
}

// testSuiteState contains shared state for a running test suite.
// It is nil when executing a standalone test.
type testSuiteState struct {
	// name is the name of the test suite.
	name string

	// paramDefs are parameter definitions shared across all tests in the suite.
	paramDefs []TestParameterDefinition
}

type deviceState struct {
	name         string
	requirement  RequireEdgeDevice
	imageRef     *api.ImageRef
	imageName    string
	spec         *api.EVEDevice
	ID           uuid.UUID
	onboardCert  *x509.Certificate
	onboardKey   *ecdsa.PrivateKey
	ecdhCert     *x509.Certificate
	serial       string
	config       *EdgeDeviceConfig
	state        EdgeDeviceState // TODO
	consoleInUse bool
}

type tcpProxy struct {
	listener   net.Listener
	devName    string // empty for SDN
	forConsole bool
}

// Global test harness instance.
// It is initialized once per top-level test execution.
// Do not use directly, instead call getTestHarness().
var _globalTH *TestHarness

func getTestHarness() *TestHarness {
	if _globalTH == nil {
		panic(fmt.Sprintf("%s called before Init", utils.FuncNameFromStackTrace(1)))
	}
	return _globalTH
}

// Init initializes the test harness and must be called exactly once per test.
// When used inside a test suite, Init may be called multiple times, once per
// test case, but only a single harness instance will be created.
func Init(t *testing.T) *T {
	if _globalTH != nil {
		// Test harness already exists, meaning this test is likely running as part
		// of a test suite.
		th := _globalTH
		th.testM.Lock()
		defer th.testM.Unlock()
		if th.test.initialized {
			th.t.Fatalf("Multiple Init calls detected")
		}
		th.test.artifactDir = filepath.Join(th.artifactDir, th.test.name)
		if err := os.MkdirAll(th.test.artifactDir, 0o755); err != nil {
			th.t.Fatalf("failed to create directory for test artifacts: %v", err)
		}
		th.test.failedCh = make(chan struct{})
		th.test.initialized = true
		th.t = &T{T: t, th: th}
		return th.t
	}

	constants.InitViperConfig()
	th := &TestHarness{}
	_globalTH = th
	th.t = &T{T: t, th: th}
	th.ctx, th.cancel = context.WithCancel(context.Background())
	th.artifactDir = viper.GetString(constants.InternalArtifactDirEnv)
	th.devices = make(map[string]*deviceState)
	th.tcpProxies = make(map[int]*tcpProxy)
	th.pauseAtCheckpoint = viper.GetString(constants.PauseOnCheckpointEnv)
	th.pauseOnFailure = viper.GetBool(constants.PauseOnFailureEnv)
	th.resume = make(chan struct{})

	// Set the test name to the calling test function name.
	// Init is expected to be called directly from a TestXxx function.
	th.test.name = utils.FuncNameFromStackTrace(2)
	th.test.artifactDir = th.artifactDir
	if err := os.MkdirAll(th.test.artifactDir, 0o755); err != nil {
		th.t.Fatalf("failed to create directory for test artifacts: %v", err)
	}
	th.test.failedCh = make(chan struct{})
	th.test.initialized = true

	// Setup logging
	logLevelStr := viper.GetString(constants.LogLevelEnv)
	logLevel, err := logrus.ParseLevel(logLevelStr)
	if err != nil {
		th.t.Fatalf("Failed to parse log level %q: %v", logLevelStr, err)
	}
	th.log = logrus.New()
	th.log.SetFormatter(&logger.PrefixedFormatter{
		Prefix: "HARNESS ",
		Color:  logger.PrefixColorBlue,
	})
	th.log.SetLevel(logLevel)
	th.userLog = logrus.New()
	th.userLog.SetFormatter(&logger.PrefixedFormatter{
		Prefix: "TEST ",
		Color:  logger.PrefixColorCyan,
	})
	th.userLog.SetLevel(logLevel)

	// Setup logging for logs coming from the broker.
	th.brokerLog = logrus.New()
	th.brokerLog.SetFormatter(&logger.PrefixedFormatter{
		Prefix: "BROKER ",
		Color:  logger.PrefixColorPurple,
	})
	th.brokerLog.SetLevel(logLevel)

	// Determine the default IP gateway and the output interface name.
	gwIPv4, link, err := utils.GetDefaultGateway(netlink.FAMILY_V4)
	if err != nil {
		th.t.Fatalf("failed to get IPv4 default gateway: %v", err)
	}
	th.dockerIntf = link.Attrs().Name
	th.dockerIntfIdx = link.Attrs().Index
	th.dockerIntfIPv4 = GetSrcIPv4ForInternetAccess()
	th.dockerGwIPv4 = gwIPv4
	gwIPv6, _, err := utils.GetDefaultGateway(netlink.FAMILY_V6)
	if err != nil {
		// IPv6 connectivity is not available.
		// Just log warning and continue
		th.log.Warnf("failed to get IPv6 default gateway: %v", err)
	} else {
		th.dockerGwIPv6 = gwIPv6
	}

	// Generate CA root certificate.
	certDir := filepath.Join(th.artifactDir, "ca-cert")
	if err = os.MkdirAll(certDir, 0o755); err != nil {
		th.t.Fatalf("failed to create CA certificate directory: %v", err)
	}
	caCertPath := filepath.Join(certDir, "ca.pem")
	caKeyPath := filepath.Join(certDir, "ca-key.pem")
	th.caCert, th.caKey, err = utils.GenCARoot()
	if err != nil {
		th.t.Fatalf("failed to generate CA certificate/key: %v", err)
	}
	err = utils.OutputCertAndKey(th.caCert, th.caKey, caCertPath, caKeyPath)
	if err != nil {
		th.t.Fatalf("failed to output CA certificate/key: %v", err)
	}

	// Start the Adam controller, listening on a dedicated dummy interface.
	adamIPs := []net.IP{
		GetControllerIPv4(),
		GetControllerIPv6(),
	}
	adamIPNets := []net.IPNet{
		{IP: GetControllerIPv4(), Mask: net.CIDRMask(32, 32)},
		{IP: GetControllerIPv6(), Mask: net.CIDRMask(128, 128)},
	}
	th.adamStatusCh = make(chan controller.AdamState, 5)
	adamLog := th.log.WithField("component", "adam")
	err = utils.CreateDummyInterface(controllerIntfName, adamIPNets)
	if err != nil {
		th.t.Fatalf("failed to create controller interface: %v", err)
	}
	th.adamClient = controller.NewAdamClient(
		adamLog, th.artifactDir, GetControllerHostname(),
		adamIPs, GetControllerPort(), th.caCert, th.caKey, th.adamStatusCh)
	err = th.adamClient.Start()
	if err != nil {
		th.t.Fatalf("Failed to start Adam controller: %v", err)
	}
	th.wg.Add(1)
	go th.monitorAdam(adamLog)

	// Create broker client.
	brokerAddr := viper.GetString(constants.BrokerAddressEnv)
	if brokerAddr == "" {
		th.log.Infof("Connecting to the broker running inside the container")
		brokerAddr = "localhost"
		th.brokerInContainer = true
	}
	brokerPort := strconv.Itoa(viper.GetInt(constants.BrokerPortEnv))
	brokerAddr = net.JoinHostPort(brokerAddr, brokerPort)
	// Note: no I/O is performed by grpc.NewClient, connection to broker
	// is established with the first RPC call, which is Connect() -- see below.
	th.brokerConn, err = grpc.NewClient(brokerAddr,
		grpc.WithTransportCredentials(insecure.NewCredentials()))
	if err != nil {
		th.t.Fatalf("failed to create broker client: %v", err)
	}
	th.brokerClient = api.NewBrokerClient(th.brokerConn)

	// Connect to the broker.
	ctx, cancel := context.WithTimeout(th.ctx, brokerConnectTimeout)
	connectResp, err := th.brokerClient.Connect(ctx, &api.ConnectRequest{})
	cancel()
	if err != nil {
		th.t.Fatalf("failed to connect to the broker at %s: %v", brokerAddr, err)
	}
	th.brokerClientID = connectResp.ClientId
	th.log.Infof("Connected to broker at %s (client_id=%s, version=%s)",
		brokerAddr, th.brokerClientID, connectResp.BrokerVersion)

	// Run broker keep-alive stream.
	th.wg.Add(1)
	go th.runBrokerKeepAlive()

	// Stream broker logs.
	th.wg.Add(1)
	go th.runBrokerLogStream()

	// Start TCP listener for the gRPC API.
	port := viper.GetInt(constants.APIPortEnv)
	listenAddr := fmt.Sprintf(":%d", port)
	listener, err := net.Listen("tcp", listenAddr)
	if err != nil {
		th.t.Fatalf("Failed to start TCP listener: %v", err)
	}

	// Create and start gRPC server.
	server := grpc.NewServer()
	api.RegisterEvetestServer(server, th)

	th.grpcServer = server
	th.listener = listener

	th.log.Infof("Evetest gRPC server listening on %s", listenAddr)
	th.wg.Add(1)
	go func() {
		defer th.wg.Done()
		if err := server.Serve(listener); err != nil {
			th.log.Infof("Evetest gRPC server stopped: %v", err)
		}
	}()

	// TODO: start HTTP server

	return th.t
}

// Close gracefully shuts down the test harness and releases all resources
// created during Init and Setup.
//
// If the test is running as part of a suite, Close performs no teardown and
// returns immediately, allowing shared resources (such as VMs and network
// infrastructure) to be reused by subsequent test cases.
//
// Otherwise, Close stops all background goroutines, shuts down the internal
// gRPC server, disconnects from the broker (triggering cleanup of all
// associated EVE and SDN devices), removes any SDN tunnel interfaces created
// by the test, and stops the Adam controller.
func Close() error {
	panicErr := recover()
	th := getTestHarness()

	// Collect artifacts (if requested by user) unless this is Close for a test suite.
	// (in which case the artifacts were already collected by Close of the last
	// executed test).
	collectArtifacts := viper.GetString(constants.ExternalArtifactDirEnv) != ""
	if collectArtifacts && !th.test.executedTestSuite {
		th.gatherLogsFromAllDevices()
		th.gatherConsoleOutputFromAllDevices()
		th.gatherInfoMsgsFromAllDevices()

		if th.suite != nil {
			// If running test-suite with the QEMU provider, copy QEMU artifacts
			// to the subtest directory.
			if st, err := os.Stat(th.qemuArtifactsDir()); err == nil && st.IsDir() {
				dst := filepath.Join(th.test.artifactDir, constants.QemuArtifactsDirname)
				if err = utils.CopyFolder(th.qemuArtifactsDir(), dst); err != nil {
					th.log.Warnf("Failed to copy QEMU artifacts to %s: %v", dst, err)
				}
			}
		}

		if th.t.Failed() || panicErr != nil {
			// If this test failed, collect diagnostic information from all devices
			// to aid troubleshooting.
			th.collectInfoFromAllDevices()
		}
	}

	if th.suite != nil {
		// When running as part of a test suite, resource teardown is deferred.
		// Shared resources (e.g., VMs) may be reused by subsequent test cases
		// within the same suite and must not be destroyed here.
		return nil
	}

	// Close any open TCP proxies.
	th.closeAllOpenTCPProxies()

	// Stop all goroutines started inside Init().
	if th.grpcServer != nil {
		th.grpcServer.GracefulStop()
	}
	th.cancel()
	th.wg.Wait()

	// Close the gRPC listener.
	if th.listener != nil {
		_ = th.listener.Close()
	}

	// Disconnect from the broker.
	// Broker will remove all devices (EVE and SDN) associated with the client session.
	ctx, cancel := context.WithTimeout(context.Background(), brokerCloseTimeout)
	_, err := th.brokerClient.Close(ctx,
		&api.CloseRequest{ClientId: th.brokerClientID})
	cancel()
	if err != nil {
		th.log.Warnf("Failed to close broker client: %v", err)
	}
	err = th.brokerConn.Close()
	if err != nil {
		th.log.Warnf("Failed to close broker connection: %v", err)
	} else {
		th.log.Infof("Closed broker connection")
	}

	// Stop the Adam controller.
	// Not really necessary, Adam would die together with the evetest container.
	err = th.adamClient.Stop()
	if err != nil {
		th.log.Warnf("Failed to stop Adam controller: %v", err)
	}

	if th.test.executedTestSuite {
		// After running test-suite with the QEMU provider, remove the shared QEMU
		// artifacts directory. Artifacts from sub-tests have already been copied into
		// their test-specific artifact subdirectories during Close().
		if err := os.RemoveAll(th.qemuArtifactsDir()); err != nil {
			th.log.Warnf("Failed to remove the QEMU artifact directory: %v", err)
		}
	}

	th = nil
	if panicErr != nil {
		panic(panicErr)
	}
	return nil
}

// Logger returns the logrus logger associated with the current test harness.
//
// Tests should use this logger for all test-related logging so that output
// is consistently formatted and integrated with the harness lifecycle
// (artifacts, verbosity settings, etc.)
func Logger() *logrus.Logger {
	th := getTestHarness()
	return th.userLog
}

// Setup evaluates and enforces the provided test requirements and prepares
// the test environment accordingly.
//
// The function first validates all supplied requirements. Next, it prepares
// (or generates) a network model, provisions and configures the required EVE device(s),
// and ensures that the SDN and broker infrastructure are running and reachable.
//
// Depending on the test context, Setup will start or reuse existing EVE and
// SDN virtual machines, establish connectivity to the SDN gRPC service using
// a broker-proxied IP-over-TCP tunnel, and apply the requested network model.
//
// When Setup returns successfully, all requirements are satisfied and the
// EVE device(s) is/are powered on and onboarded into the controller. Any failure
// during setup results in the test being failed or skipped.
func Setup(requirements ...Requirement) {
	th := getTestHarness()
	defer th.log.Infof("Setup complete")

	// Collect test requirements.
	edgeDevReqs := make(map[string]RequireEdgeDevice) // key: device name
	var netModel *api.NetworkModel
	var internetReq *RequireInternetConnectivity

	for _, requirement := range requirements {
		switch req := requirement.(type) {
		case RequireEdgeDevice:
			if _, duplicate := edgeDevReqs[req.Name]; duplicate {
				th.t.Fatalf("Duplicate edge device name: %s", req.Name)
			}
			edgeDevReqs[req.Name] = req
		case RequireEdgeDeviceCluster:
			// TODO
			th.t.Fatalf("Edge device cluster is not yet supported")
		case RequireNetworkModel:
			netModel = proto.CloneOf(req.NetworkModel)
		case RequireInternetConnectivity:
			internetReq = &req
		default:
			th.t.Fatalf("Unsupported requirement: %T", req)
		}
	}

	// Validate edge device requirement.
	if len(edgeDevReqs) == 0 {
		th.t.Fatalf("Missing edge device requirement")
	}
	for _, devReq := range edgeDevReqs {
		if devReq.Name == "" {
			th.t.Fatalf("Missing edge device name")
		}
	}

	// Prepare network model.
	if netModel == nil {
		var devNames []string
		for devName := range edgeDevReqs {
			devNames = append(devNames, devName)
		}
		netModel = th.genDefaultNetworkModel(devNames)
	} else {
		// Make sure that device names and MAC addresses are defined.
		for _, port := range netModel.Ports {
			if port.GetEveDeviceName() == "" {
				if len(edgeDevReqs) > 1 {
					th.t.Fatalf(
						"Network model port is missing EveDeviceName; " +
							"with multiple onboarded EVE devices the target device " +
							"must be specified explicitly",
					)
				}
				// len(edgeDevReqs) == 1
				for name := range edgeDevReqs {
					port.EveDeviceName = name
				}
			}
			if port.GetSdnMacAddress() == "" {
				mac := utils.GenerateMAC(constants.SDNDeviceName, port.LogicalLabel)
				port.SdnMacAddress = mac.String()
			}
			if port.GetEveMacAddress() == "" {
				mac := utils.GenerateMAC(port.EveDeviceName, port.LogicalLabel)
				port.EveMacAddress = mac.String()
			}
		}
	}

	// Add controller configuration into the network model.
	netModel.ControllerConfig = &api.ControllerConfig{
		ControllerIps: []string{
			GetControllerIPv4().String(),
			GetControllerIPv6().String(),
		},
		ControllerPort: uint32(GetControllerPort()),
	}

	// Reuse devices if the requirements match the previous test
	// and the reuse policy for this test allows it.
	if th.maybeReuseDevices(edgeDevReqs, netModel) {
		th.log.Infof("Reusing devices from the previous test")
		// No need to set up devices; they are reused from the previous test.
		// Just check Internet connectivity if required.
		if internetReq != nil {
			th.checkInternetConnectivity(*internetReq)
		}
		return
	} else if len(th.devices) > 0 {
		th.log.Infof("Tearing down devices from the previous test")
		th.teardownDevices()
		// When running with the QEMU provider, remove the shared QEMU artifacts
		// directory. Artifacts from previous tests have already been copied into
		// their test-specific artifact subdirectories during Close().
		if err := os.RemoveAll(th.qemuArtifactsDir()); err != nil {
			th.log.Warnf("Failed to remove the QEMU artifact directory: %v", err)
		}
	}

	// Setup EVE devices.
	devices := make(map[string]*deviceState)
	withIPv6 := internetReq != nil && internetReq.RequireIPv6
	for devName, devReq := range edgeDevReqs {
		devState := &deviceState{name: devName, requirement: devReq}
		devices[devName] = devState
		th.prepareEVEDeviceForOnboarding(devState)
		th.prepareImageForEVEDevice(devState)
	}
	sdnUplinkIPs := th.setupEVEDevices(devices, netModel, withIPv6)
	th.devicesM.Lock()
	th.devices = devices
	th.devicesM.Unlock()

	// Establish an IP tunnel to SDN used for EVE <-> controller/test connectivity.
	th.openTunnelToSDN()
	th.setupSDNTunnelRoutes(sdnUplinkIPs)

	// Connect to the SDN gRPC server.
	// This function sets th.sdnConn and th.sdnClient (or fails with Fatal)
	th.connectToSDN(sdnUplinkIPs)

	// Check Internet connectivity if required.
	if internetReq != nil {
		th.checkInternetConnectivity(*internetReq)
	}

	// Apply the network model.
	ctx, cancel := context.WithTimeout(th.ctx, sdnApplyNetModelTimeout)
	th.log.Debugf("Submitting request to apply network model: %s", netModel)
	_, err := th.sdnClient.SetNetworkModel(ctx, &api.SDNSetNetworkModelRequest{
		NetworkModel: netModel,
	})
	cancel()
	if err != nil {
		th.t.Fatal(err)
	}
	th.netModelM.Lock()
	th.netModel = netModel
	th.netModelM.Unlock()
	th.log.Info("Successfully applied the network model")

	// Onboard EVE devices.
	th.onboardEVEDevices()
}

// RunParallel runs workerFunc concurrently in numOfWorkers goroutines.
// workerFunc is invoked once per worker and is passed a zero-based
// workerIdx in the range [0, numOfWorkers).
// The function blocks until either:
//   - all workers complete successfully, or
//   - the test is marked as failed, in which case it returns immediately without
//     waiting for the remaining workers.
//
// This enables fail-fast behavior for parallel test execution.
func RunParallel(numOfWorkers int, workerFunc func(workerIdx int)) {
	th := getTestHarness()
	doneCh := make(chan struct{}, numOfWorkers)

	for i := 0; i < numOfWorkers; i++ {
		go func() {
			workerFunc(i)
			doneCh <- struct{}{}
		}()
	}

	th.testM.Lock()
	failedCh := th.test.failedCh
	th.testM.Unlock()

	waitCount := numOfWorkers
	for {
		select {
		case <-doneCh:
			waitCount--
			if waitCount == 0 {
				return
			}
		case <-failedCh:
			// A worker triggered T.Fatal;
			// fail fast and do not wait for the remaining workers.
			th.t.FailNow()
		}
	}
}

// Checkpoint marks a significant execution point in a test.
//
// Each checkpoint is identified by a name. If the environment variable
// EVETEST_PAUSE_ON_CHECKPOINT is set to the same name, test execution will pause
// when this checkpoint is reached.
//
// The test can be resumed via the CLI command:
//
//	evetest continue [--until <next-checkpoint>]
//
// This mechanism is primarily intended for interactive debugging and
// step-by-step inspection of long-running or complex tests.
func Checkpoint(name string) {
	th := getTestHarness()
	if name == "" {
		th.t.Fatalf("missing checkpoint name")
	}
	th.checkpointM.Lock()
	shouldPause := th.pauseAtCheckpoint == name
	if shouldPause {
		th.pausedAtCheckpoint = name
	}
	th.checkpointM.Unlock()

	if shouldPause {
		th.log.Infof("Paused at checkpoint %q", name)
		<-th.resume
		th.log.Infof("Resumed after checkpoint %q", name)
	}
}

// GetEdgeDevice returns a handle to an onboarded EdgeDevice identified by devName.
func GetEdgeDevice(devName string) *EdgeDevice {
	th := getTestHarness()
	if !th.isDeviceOnboarded(devName) {
		th.t.Fatalf("Unknown device %q", devName)
	}
	return &EdgeDevice{th: th, devName: devName}
}

// GetAllEdgeDevices returns handles for all EdgeDevices currently known to the
// test th.
func GetAllEdgeDevices() (devices []*EdgeDevice) {
	th := getTestHarness()
	th.devicesM.Lock()
	defer th.devicesM.Unlock()
	for _, devState := range th.devices {
		devices = append(devices, &EdgeDevice{devName: devState.name})
	}
	return devices
}

// UpdateNetworkModel updates the current network model,
// enforcing that device network ports cannot change at runtime.
func UpdateNetworkModel(netModel *api.NetworkModel) {
	th := getTestHarness()
	th.netModelM.Lock()
	defer th.netModelM.Unlock()
	samePorts := generics.EqualSetsFn(th.netModel.GetPorts(), netModel.GetPorts(),
		func(a, b *api.Port) bool { return proto.Equal(a, b) })
	if !samePorts {
		th.t.Fatalf(
			"It is not allowed to change the set of device network ports in runtime")
	}

	// Apply the new network model.
	ctx, cancel := context.WithTimeout(th.ctx, sdnApplyNetModelTimeout)
	th.log.Debugf("Submitting request to apply network model: %s", netModel)
	_, err := th.sdnClient.SetNetworkModel(ctx, &api.SDNSetNetworkModelRequest{
		NetworkModel: netModel,
	})
	cancel()
	if err != nil {
		th.t.Fatal(err)
	}

	th.netModel = netModel
	th.log.Info("Successfully applied the new network model")
}

// ChangeSigningCert replaces the controller signing certificate
// with the provided one.
func ChangeSigningCert(newSignCertPEM string) error {
	th := getTestHarness()
	// TODO
	th.t.Fatalf("ChangeSigningCert is not implemented")
	return nil
}

// GetControllerHostname returns the controller hostname (stored inside /config/server)
func GetControllerHostname() string {
	return "adam.evetest"
}

// GetControllerPort returns the port number on which the controller listens.
func GetControllerPort() uint16 {
	return 443
}

// GetControllerIPv4 returns the controller (Adam) IPv4 address.
func GetControllerIPv4() net.IP {
	return controllerIPv4
}

// GetControllerIPv6 returns the controller (Adam) IPv6 address and subnet
// associated with the container's default IPv6 route, if present.
func GetControllerIPv6() net.IP {
	return controllerIPv6
}

// GetSrcIPv4ForInternetAccess returns the first non-link-local IPv4 address
// of the interface connecting container with the docker network.
// This IP should be used as the source IP when tests
// need to access the Internet from the evetest container.
func GetSrcIPv4ForInternetAccess() net.IP {
	th := getTestHarness()
	ips, err := utils.GetInterfaceIPs(th.dockerIntf)
	if err != nil {
		th.t.Fatal(err)
	}
	for _, ip := range ips {
		if ip.To4() == nil || !ip.IsGlobalUnicast() {
			continue
		}
		return ip
	}
	th.t.Fatalf("No suitable global-unicast IPv4 address found on %s",
		th.dockerIntf)
	return nil
}

// GetSrcIPv6ForInternetAccess returns the first global unicast IPv6 address
// assigned to the interface connecting container with the docker network.
// This IP should be used as the source address when tests need IPv6 Internet
// access from the evetest container.
func GetSrcIPv6ForInternetAccess() net.IP {
	th := getTestHarness()
	ips, err := utils.GetInterfaceIPs(th.dockerIntf)
	if err != nil {
		th.t.Fatal(err)
	}
	for _, ip := range ips {
		if ip.To4() != nil || !ip.IsGlobalUnicast() {
			continue
		}
		return ip
	}

	th.t.Fatalf("No suitable global-unicast IPv6 address found on %s",
		th.dockerIntf)
	return nil
}

// GetSrcIPv4ForEVEAccess returns the IPv4 address used as the source IP
// when a test communicates with EVE management services or EVE applications.
// This is exposed to allow network-model firewall rules (when enabled)
// to permit traffic between the test environment and EVE/app endpoints.
func GetSrcIPv4ForEVEAccess() net.IP {
	return sdnTunContainerIPv4
}

// GetSrcIPv6ForEVEAccess returns the IPv6 address used as the source IP
// when a test communicates with EVE management services or EVE applications.
// This is exposed to allow network-model firewall rules (when enabled)
// to permit traffic between the test environment and EVE/app endpoints.
func GetSrcIPv6ForEVEAccess() net.IP {
	return sdnTunContainerIPv6
}

// GetHTTPDatastoreIPv4 returns the IPv4 address of the HTTP datastore.
func GetHTTPDatastoreIPv4() *net.IPNet {
	th := getTestHarness()
	// TODO
	th.t.Fatalf("GetHTTPDatastoreIPv4 is not implemented")
	return nil
}

// GetHTTPDatastoreIPv6 returns the IPv6 address of the HTTP datastore.
func GetHTTPDatastoreIPv6() *net.IPNet {
	th := getTestHarness()
	// TODO
	th.t.Fatalf("GetHTTPDatastoreIPv6 is not implemented")
	return nil
}

// GetHTTPDatastoreWelcomeMsg returns the welcome message served by the
// evetest HTTP datastore.
func GetHTTPDatastoreWelcomeMsg() string {
	return "Hello from evetest HTTP server!"
}

func (th *TestHarness) qemuArtifactsDir() string {
	return filepath.Join(th.artifactDir, constants.QemuArtifactsDirname)
}

func (th *TestHarness) monitorAdam(adamLog *logrus.Entry) {
	defer th.wg.Done()
	for {
		select {
		case <-th.ctx.Done():
			adamLog.Info("Test harness context canceled, stopping Adam monitoring")
			return
		case status := <-th.adamStatusCh:
			if status.Type == controller.AdamStateCrashed {
				// TODO: Calling th.t.Fatalf here will not stop the main test,
				// because it is running in a different goroutine than the one executing the test.
				// Consider signaling the main test goroutine to fail instead.
				th.t.Fatalf("Adam crashed: %v", status.Err)
			}
		}
	}
}

func (th *TestHarness) runBrokerKeepAlive() {
	defer th.wg.Done()

	stream, err := th.brokerClient.KeepAlive(th.ctx)
	if err != nil {
		th.log.Errorf("KeepAlive failed: %v", err)
		return
	}

	// Send initial ping with client ID
	err = stream.Send(&api.KeepAlivePing{ClientId: th.brokerClientID})
	if err != nil {
		th.log.Errorf("Failed to send initial keepalive ping: %v", err)
		return
	}

	ticker := time.NewTicker(time.Minute)
	defer ticker.Stop()

	// Loop to send periodic pings and receive pongs
	for {
		select {
		case <-th.ctx.Done():
			th.log.Info("Test harness context canceled, stopping keep-alive stream")
			return
		case <-ticker.C:
			// Send ping
			err = stream.Send(&api.KeepAlivePing{ClientId: th.brokerClientID})
			if err != nil {
				th.log.Errorf("Failed to send keepalive ping: %v", err)
				return
			}
			// Receive pong
			_, err = stream.Recv()
			if err != nil {
				th.log.Errorf("Failed to receive keepalive pong: %v", err)
				return
			}
			th.log.Tracef("Received keepalive pong")
		}
	}
}

func (th *TestHarness) runBrokerLogStream() {
	defer th.wg.Done()

	req := &api.LogsRequest{ClientId: th.brokerClientID}
	stream, err := th.brokerClient.StreamLogs(th.ctx, req)
	if err != nil {
		th.log.Errorf("StreamLogs failed: %v", err)
		return
	}

	for {
		m, err := stream.Recv()
		if err == io.EOF {
			return
		}
		if err != nil {
			if th.ctx.Err() != nil {
				th.log.Info(
					"Test harness context canceled, stopping broker log stream")
				return
			}
			th.log.Errorf("Broker log stream error: %v", err)
			return
		}
		level := logger.APILogSeverityToLogrusLevel(m.Severity)
		th.brokerLog.WithTime(m.Timestamp.AsTime()).Log(level, m.Message)
	}
}

func (th *TestHarness) closeAllOpenTCPProxies() {
	var closedConsoles []string
	th.tcpProxiesM.Lock()
	for port, proxy := range th.tcpProxies {
		err := proxy.listener.Close()
		if err != nil {
			th.log.Warnf("Failed to close TCP proxy running on port %d: %v",
				port, err)
		}
		if proxy.forConsole && proxy.devName != "" {
			closedConsoles = append(closedConsoles, proxy.devName)
		}
	}
	th.tcpProxies = make(map[int]*tcpProxy)
	th.tcpProxiesM.Unlock()

	th.devicesM.Lock()
	for _, devName := range closedConsoles {
		th.devices[devName].consoleInUse = false
	}
	th.devicesM.Unlock()
}

// Tear down any previously set up devices.
func (th *TestHarness) teardownDevices() {
	if len(th.devices) == 0 {
		return
	}

	// Remove SDN tunnel interface.
	if th.sdnTunCancel != nil {
		th.sdnTunCancel()
		th.sdnTunWG.Wait()
		th.sdnTunCancel = nil
	}
	th.sdnTunStream = nil
	if th.sdnTunIntf != nil {
		err := th.sdnTunIntf.Close()
		if err != nil {
			th.log.Warnf("Failed to close descriptor for SDN tunnel interface %q",
				th.sdnTunIntf.Name())
		}
		if link, err := netlink.LinkByName(th.sdnTunIntf.Name()); err == nil {
			err = netlink.LinkDel(link)
			if err != nil {
				th.log.Warnf("Failed to remove SDN tunnel interface %q",
					th.sdnTunIntf.Name())
			}
		}
		th.sdnTunIntf = nil
	}

	// Close SDN client.
	if th.sdnClient != nil {
		err := th.sdnConn.Close()
		if err != nil {
			th.log.Warnf("Failed to close SDN client connection: %v", err)
		}
		th.sdnClient = nil
		th.sdnConn = nil
	}

	// Forget previously applied network model.
	th.netModelM.Lock()
	th.netModel = nil
	th.netModelM.Unlock()

	// Close open TCP proxies.
	th.closeAllOpenTCPProxies()

	th.devicesM.Lock()
	defer th.devicesM.Unlock()

	// Remove EVE device from the controller.
	for _, dev := range th.devices {
		if dev.ID == uuid.Nil {
			continue
		}
		ctx, cancel := context.WithTimeout(th.ctx, deviceRemoveTimeout)
		err := th.adamClient.RemoveDevice(ctx, dev.ID)
		cancel()
		if err != nil {
			th.t.Fatalf(
				"Failed to remove device %q from the controller: %v",
				dev.name, err)
		}
	}

	// Tear-down all deployed EVE devices and the SDN VM.
	ctx, cancel := context.WithTimeout(context.Background(), brokerTeardownDevicesTimeout)
	_, err := th.brokerClient.TeardownDevices(ctx,
		&api.TeardownDevicesRequest{ClientId: th.brokerClientID})
	cancel()
	if err != nil {
		th.t.Fatalf("Failed to tear-down all devices: %v", err)
	}
	th.devices = make(map[string]*deviceState)
}

// prepareEVEDeviceForOnboarding generates serial number and onboarding certificates
// for the device.
func (th *TestHarness) prepareEVEDeviceForOnboarding(dev *deviceState) {
	var err error
	dev.serial, err = utils.RandomDeviceSerial(8)
	if err != nil {
		th.t.Fatalf("Failed to generate serial number for device %q: %v",
			dev.name, err)
	}
	onboardUUID, err := uuid.NewV4()
	if err != nil {
		th.t.Fatalf("Failed to generate UUID for the onboarding certificate: %v",
			err)
	}
	dev.onboardCert, dev.onboardKey, err = utils.GenServerCertElliptic(
		th.caCert, th.caKey, big.NewInt(2), nil, nil, onboardUUID.String())
	if err != nil {
		th.t.Fatalf("Failed to generate onboarding certificate for device %s: %v",
			dev.name, err)
	}
}

// prepareImageForEVEDevice prepares an EVE image reference for the given device
// and ensures that the corresponding EVE (live or installer) VM image is built
// on the broker.
//
// The method determines the EVE version and hypervisor to use (from the device
// requirements and environment), constructs an ImageRef, and requests the broker
// to build the image. If the broker reports that the required EVE container image
// is missing, the image is extracted from the local Docker daemon, pushed to the
// broker, and the build is retried.
//
// Any failure is considered fatal for the test and will terminate execution.
func (th *TestHarness) prepareImageForEVEDevice(dev *deviceState) {
	eveVersion := dev.requirement.WithEVEVersion
	if eveVersion == "" {
		eveVersion = viper.GetString(constants.EVEVersionEnv)
	}
	if eveVersion == "" {
		th.t.Fatalf("EVE version is not defined")
	}
	var err error
	var hypervisor api.HypervisorType
	switch dev.requirement.WithHypervisor {
	case HypervisorUndefined:
		// Use KVM by default.
		hypervisor = api.HypervisorType_HV_KVM
	case HypervisorKVM:
		hypervisor = api.HypervisorType_HV_KVM
	case HypervisorXen:
		hypervisor = api.HypervisorType_HV_XEN
	case HypervisorKubevirt:
		hypervisor = api.HypervisorType_HV_KUBEVIRT
	}
	dev.imageRef = &api.ImageRef{
		Repo:       viper.GetString(constants.EVERepoEnv),
		Version:    eveVersion,
		Hypervisor: hypervisor,
		// TODO: add broker method to obtain supported architectures (?)
		Arch: api.ArchType_ARCH_AMD64,
	}
	dev.imageName, err = utils.EVEDockerImageName(dev.imageRef)
	if err != nil {
		th.t.Fatalf("Invalid EVE image reference: %w", dev.imageRef)
	}

	diskSizeInMB := dev.requirement.MinDiskSizeInMB
	if diskSizeInMB == 0 {
		diskSizeInMB = constants.DefaultEVEDeviceDiskSizeInMB
	}
	onboardKeyPEM, err := utils.ECDSAPrivateKeyToPEM(dev.onboardKey)
	if err != nil {
		th.t.Fatalf(
			"Failed to PEM-encode the onboarding certificate for device %s: %v",
			dev.name, err)
	}
	rootCert := utils.CertToPEM(th.caCert)
	var bootstrapConfigPb []byte
	withBoostrapConfig := dev.requirement.WithInjectedBootstrapConfig
	if withBoostrapConfig != nil {
		bootstrapConfig := withBoostrapConfig.MakeBootstrapConfig()
		bootstrapConfigPb, err = proto.Marshal(bootstrapConfig)
		if err != nil {
			th.t.Fatalf("Failed to marshal bootstrap config to protobuf: %v", err)
		}
	}
	var overrideJson string
	withInjectedNetworkOverride := dev.requirement.WithInjectedNetworkOverride
	if withInjectedNetworkOverride != nil {
		overrideBytes, err := json.Marshal(withInjectedNetworkOverride)
		if err != nil {
			th.t.Fatalf("Failed to marshal network override to JSON: %v", err)
		}
		overrideJson = string(overrideBytes)
	}
	// Make sure that already during onboarding we have debug logs and SSH access.
	globalProperties := pillartypes.NewConfigItemValueMap()
	globalProperties.SetGlobalValueString(pillartypes.DefaultLogLevel, "debug")
	globalProperties.SetGlobalValueString(pillartypes.DefaultRemoteLogLevel, "debug")
	globalProperties.SetGlobalValueString(
		pillartypes.SSHAuthorizedKeys, constants.EVESSHPublickKey)
	// We do not want to wait too long for the first /config request.
	globalProperties.SetGlobalValueInt(pillartypes.ConfigInterval, 5)
	withInjectedConfigProperties := dev.requirement.WithInjectedConfigProperties
	if withInjectedConfigProperties != nil {
		globalProperties.UpdateItemValues(withInjectedConfigProperties)
	}
	globalPropertiesBytes, err := json.Marshal(globalProperties)
	if err != nil {
		th.t.Fatalf(
			"Failed to marshal global configuration properties to JSON: %v", err)
	}
	globalPropertiesJson := string(globalPropertiesBytes)
	buildReq := &api.BuildImageRequest{
		ClientId:      th.brokerClientID,
		DeviceName:    dev.name,
		Image:         dev.imageRef,
		MakeInstaller: dev.requirement.DeviceReusePolicy == CreateFromScratchWithInstaller,
		DiskBytes:     uint64(diskSizeInMB) << 20,
		Config: &api.EveConfig{
			ServerName:        "https://" + GetControllerHostname(),
			SoftSerial:        dev.requirement.WithSoftSerial,
			OnboardCertPem:    string(utils.CertToPEM(dev.onboardCert)),
			OnboardKeyPem:     string(onboardKeyPEM),
			V2TlsCertsPem:     []string{string(rootCert)},
			RootCertPem:       string(rootCert),
			BootstrapConfigPb: bootstrapConfigPb,
			OverrideJson:      overrideJson,
			GlobalJson:        globalPropertiesJson,
		},
	}
	ctx, cancel := context.WithTimeout(th.ctx, brokerBuildImageTimeout)
	buildResp, err := th.brokerClient.BuildImage(ctx, buildReq)
	cancel()
	if err != nil {
		th.t.Fatalf("BuildImage %q failed: %v", dev.imageName, err)
	}

	if buildResp.MissingEveContainerImage {
		th.log.Warn("Broker is missing EVE container image — pushing it now...")
		th.pushEVEImageToBroker(dev.imageRef)

		// Retry build
		ctx, cancel = context.WithTimeout(th.ctx, brokerBuildImageTimeout)
		buildResp, err = th.brokerClient.BuildImage(ctx, buildReq)
		cancel()
		if err != nil {
			th.t.Fatalf("BuildImage %q (retry) failed: %v", dev.imageName, err)
		}
		if buildResp.MissingEveContainerImage {
			th.t.Fatalf("Broker is missing EVE container image even after push.")
		}
		th.log.Infof("BuildImage %q succeeded after pushing image.",
			dev.imageName)
	} else {
		th.log.Infof("BuildImage %q succeeded (docker image was already present).",
			dev.imageName)
	}
}

// pushEVEImageToBroker streams a pre-built EVE container image to the broker
// using a client-side streaming gRPC call.
//
// The function first sends image metadata to initiate the upload, then streams
// the gzipped Docker image data in fixed-size chunks directly from the local
// Docker daemon. The image data is produced lazily and streamed without loading
// the entire image into memory.
//
// The upload is bounded by a timeout and relies on stream EOF to signal
// completion. On success, the broker responds indicating whether the image
// already existed or was newly uploaded. Any failure during streaming or upload
// results in a fatal error.
func (th *TestHarness) pushEVEImageToBroker(imageRef *api.ImageRef) {
	ctx, cancel := context.WithTimeout(th.ctx, brokerPushEVEImageTimeout)
	defer cancel()

	stream, err := th.brokerClient.PushEVEContainerImage(ctx)
	if err != nil {
		th.t.Fatalf("PushEVEContainerImage failed: %v", err)
	}

	// First message: metadata
	err = stream.Send(&api.PushImageChunk{
		Payload: &api.PushImageChunk_Request{
			Request: &api.PushImageRequest{
				ClientId: th.brokerClientID,
				Image:    imageRef,
			},
		},
	})
	if err != nil {
		th.t.Fatalf("failed to send image metadata: %v", err)
	}

	dockerImageName, err := utils.EVEDockerImageName(imageRef)
	if err != nil {
		th.t.Fatalf("invalid EVE image reference: %v", err)
	}

	imageSize, err := utils.GetDockerImageSizeBytes(ctx, dockerImageName)
	if err != nil {
		th.t.Fatalf("failed to get docker image size: %v", err)
	}

	imageReader, err := utils.StreamDockerImageGzip(ctx, dockerImageName)
	if err != nil {
		th.t.Fatalf("failed to stream docker image: %v", err)
	}
	defer imageReader.Close()

	var sentBytes int64
	nextLogPercent := int64(10)
	buf := make([]byte, 1024*1024) // 1MB chunks

	for {
		n, err := imageReader.Read(buf)
		if n > 0 {
			err = stream.Send(&api.PushImageChunk{
				Payload: &api.PushImageChunk_DataGzipChunk{
					DataGzipChunk: buf[:n],
				},
			})
			if err != nil {
				th.t.Fatalf("failed to send image chunk: %v", err)
			}
			sentBytes += int64(n)
			percent := sentBytes * 100 / imageSize
			if percent >= nextLogPercent {
				th.log.Infof("Pushing EVE image %q to broker: %d%% done",
					dockerImageName, nextLogPercent)
				nextLogPercent += 10
			}
		}
		if err == io.EOF {
			break
		}
		if err != nil {
			th.t.Fatalf("failed to read image stream: %v", err)
		}
	}

	pushResp, err := stream.CloseAndRecv()
	if err != nil {
		th.t.Fatalf("PushEVEContainerImage failed: %v", err)
	}

	if pushResp.AlreadyExists {
		th.log.Info("EVE container image already exists on broker.")
	} else {
		th.log.Info("EVE container image pushed successfully.")
	}
}

// setupEVEDevices requests the broker to provision and configure EVE devices
// according to the provided device requirements and network model.
//
// The method computes effective CPU and memory values (applying defaults if needed),
// maps SDN ports from the network model to EVE network interfaces, and submits a
// SetupDevices request to the broker. The request includes EVE VM parameters,
// TPM configuration, image reference, and SDN image details.
//
// On success, the function returns the list of SDN uplink IP addresses assigned
// from DHCP server during the SDN setup. Any failure is considered fatal for
// the test and will terminate execution.
func (th *TestHarness) setupEVEDevices(
	devices map[string]*deviceState, netModel *api.NetworkModel,
	withIPv6 bool) (sdnUplinkIPs []string) {
	setupReq := &api.SetupDevicesRequest{
		ClientId: th.brokerClientID,
		SdnConfig: &api.SDNConfig{
			ImageRepo:    viper.GetString(constants.SDNRepoEnv),
			ImageVersion: viper.GetString(constants.SDNVersionEnv),
			EnableIpv6:   withIPv6,
		},
	}

	var devNames []string
	for _, dev := range devices {
		devNames = append(devNames, dev.name)
		cpus := dev.requirement.MinCPUs
		if cpus == 0 {
			cpus = constants.DefaultEVEDeviceCPUs
		}
		memSizeInMB := dev.requirement.MinRAMInMB
		if memSizeInMB == 0 {
			memSizeInMB = constants.DefaultEVEDeviceRAMInMB
		}
		var interfaces []*api.EVEInterface
		for i, port := range netModel.Ports {
			if port.EveDeviceName == dev.requirement.Name {
				interfaces = append(interfaces, &api.EVEInterface{
					Name:          fmt.Sprintf("eth%d", i),
					MacAddress:    port.EveMacAddress,
					SdnMacAddress: port.SdnMacAddress,
				})
			}
		}
		dev.spec = &api.EVEDevice{
			DeviceName:   dev.requirement.Name,
			Cpus:         uint32(cpus),
			MemoryBytes:  uint64(memSizeInMB) << 20,
			SerialNumber: dev.serial,
			WithTpm:      dev.requirement.WithTPM,
			Image:        dev.imageRef,
			Interfaces:   interfaces,
		}
		setupReq.Devices = append(setupReq.Devices, dev.spec)
	}

	ctx, cancel := context.WithTimeout(th.ctx, brokerSetupDevicesTimeout)
	th.log.Debugf("Submitting request to setup devices: %s", setupReq)
	setupResp, err := th.brokerClient.SetupDevices(ctx, setupReq)
	cancel()
	if err != nil {
		th.t.Fatalf("Failed to setup devices %v: %v", devNames, err)
	}
	th.log.Infof("Setup completed for devices %v", devNames)
	return setupResp.GetSdnUplinkIps()
}

// openTunnelToSDN establishes a point-to-point IP tunnel between the evetest
// container and the SDN service.
//
// The tunnel is used to route controller traffic through the SDN by:
//  1. Requesting a tunnel from the broker via gRPC.
//  2. Creating and configuring a local TUN interface.
//  3. Bridging packets between the TUN interface and the gRPC tunnel stream.
func (th *TestHarness) openTunnelToSDN() {
	// Send the initial connect request.
	var err error
	th.sdnTunStream, err = th.brokerClient.ConnectTunnelToSDN(th.ctx)
	if err != nil {
		th.t.Fatalf("Failed to connect an IP tunnel to SDN: %v", err)
	}
	connectReq := &api.ConnectTunnelToSDNRequest{
		Payload: &api.ConnectTunnelToSDNRequest_Connect{
			Connect: &api.SDNTunnel{
				ClientId: th.brokerClientID,
				IpAddresses: []string{
					sdnTunVMIPv4.String() + sdnTunIPv4Prefix,
					sdnTunVMIPv6.String() + sdnTunIPv6Prefix,
				},
				Mtu: sdnTunMTU,
				Routes: []*api.IPRoute{
					{
						DstNetwork: GetControllerIPv4().String() + "/32",
						Gateway:    sdnTunContainerIPv4.String(),
					},
					{
						DstNetwork: GetControllerIPv6().String() + "/128",
						Gateway:    sdnTunContainerIPv6.String(),
					},
				},
			},
		},
	}
	if err := th.sdnTunStream.Send(connectReq); err != nil {
		th.t.Fatalf("Failed to send SDN tunnel connect request: %v", err)
	}

	// Receive initial response (tunnel properties, empty for now).
	_, err = th.sdnTunStream.Recv()
	if err != nil {
		th.t.Fatalf("Failed to receive SDN tunnel properties: %v", err)
	}

	// Create the tunnel interface.
	th.sdnTunIntf, err = utils.CreateTUN(sdnTunName)
	if err != nil {
		th.t.Fatalf("Failed to create TUN %q: %v", sdnTunName, err)
	}

	// Configure TUN interface
	link, err := netlink.LinkByName(sdnTunName)
	if err != nil {
		th.t.Fatalf("Failed to get link for %q: %v", sdnTunName, err)
	}
	if err = netlink.LinkSetMTU(link, sdnTunMTU); err != nil {
		th.t.Fatalf("Failed to set MTU %d on %q: %v", sdnTunMTU, sdnTunName, err)
	}
	if err := netlink.LinkSetUp(link); err != nil {
		th.t.Fatalf("Failed to bring up interface %q: %v", sdnTunName, err)
	}
	th.sdnTunIdx = link.Attrs().Index
	tunAddrs := []string{
		sdnTunContainerIPv4.String() + sdnTunIPv4Prefix,
		sdnTunContainerIPv6.String() + sdnTunIPv6Prefix,
	}
	for _, tunAddr := range tunAddrs {
		addr, err := netlink.ParseAddr(tunAddr)
		if err != nil {
			th.t.Fatalf("Invalid IP address %q: %v", tunAddr, err)
		}
		err = netlink.AddrAdd(link, addr)
		if err != nil && !os.IsExist(err) {
			th.t.Fatalf("Failed to add IP %q to %q: %v", tunAddr, sdnTunName, err)
		}
	}

	// Run tunnel proxy
	grpcPipe :=
		utils.GrpcClientPipe[api.ConnectTunnelToSDNRequest, api.ConnectTunnelToSDNResponse]{
			MakeRequest: func(data []byte) *api.ConnectTunnelToSDNRequest {
				return &api.ConnectTunnelToSDNRequest{
					Payload: &api.ConnectTunnelToSDNRequest_Data{
						Data: data,
					},
				}
			},
			Stream: th.sdnTunStream,
		}
	tunPipe := utils.ReadWriterPipe{
		PipeName: "tun device",
		RW:       th.sdnTunIntf,
		Buf:      make([]byte, os.Getpagesize()),
	}
	tunLog := th.log.WithField("component", "tun")
	th.sdnTunCtx, th.sdnTunCancel = context.WithCancel(th.ctx)
	th.wg.Add(1)
	th.sdnTunWG.Add(1)
	go func() {
		defer th.wg.Done()
		defer th.sdnTunWG.Done()
		utils.RunPipeProxy(th.sdnTunCtx, tunLog, "SDN tunnel", grpcPipe, tunPipe)
	}()
}

// Configure tunnel routes.
// These are the traffic flows that must be supported (initiator listed first):
//   - EVE <-> Controller
//   - EVE / App / Proxy <-> Internet
//   - Test <-> SDN
//   - Test <-> EVE / App
//   - Test <-> Internet
//
// And we must consider both cases of SDN running inside and outside the evetest
// container.
func (th *TestHarness) setupSDNTunnelRoutes(sdnUplinkIPs []string) {
	if len(sdnUplinkIPs) == 0 {
		th.t.Fatalf("SDN uplink IPs are empty")
	}
	sdnUplinkIP := net.ParseIP(sdnUplinkIPs[0])
	if sdnUplinkIP == nil {
		th.t.Fatalf("Invalid SDN uplink IP: %s", sdnUplinkIPs[0])
	}

	// By default, route traffic via the SDN tunnel
	// (connectivity between EVE/App/Proxy/SDN and evetest/controller).
	// IPv4
	_, anyV4, _ := net.ParseCIDR("0.0.0.0/0")
	defaultIPv4 := &netlink.Route{
		LinkIndex: th.sdnTunIdx,
		Dst:       anyV4,
		Gw:        sdnTunVMIPv4,
		Family:    netlink.FAMILY_V4,
	}
	if err := netlink.RouteReplace(defaultIPv4); err != nil {
		th.t.Fatalf("Failed to add default IPv4 route via tunnel: %v", err)
	}
	// IPv6
	_, anyV6, _ := net.ParseCIDR("::")
	defaultIPv6 := &netlink.Route{
		LinkIndex: th.sdnTunIdx,
		Dst:       anyV6,
		Gw:        sdnTunVMIPv6,
		Family:    netlink.FAMILY_V6,
	}
	if err := netlink.RouteReplace(defaultIPv6); err != nil {
		th.t.Fatalf("Failed to add default IPv6 route via tunnel: %v", err)
	}

	// Create dedicated routing table routing traffic via the docker network.
	const dockerTable = 100
	// IPv4
	if err := netlink.RouteAdd(&netlink.Route{
		LinkIndex: th.dockerIntfIdx,
		Dst:       anyV4,
		Gw:        th.dockerGwIPv4,
		Table:     dockerTable,
		Family:    netlink.FAMILY_V4,
	}); err != nil && !errors.Is(err, syscall.EEXIST) {
		th.t.Fatalf("Failed to add IPv4 default route for docker network: %v",
			err)
	}
	// IPv6
	if th.dockerGwIPv6 != nil {
		if err := netlink.RouteAdd(&netlink.Route{
			LinkIndex: th.dockerIntfIdx,
			Dst:       anyV6,
			Gw:        th.dockerGwIPv6,
			Table:     dockerTable,
			Family:    netlink.FAMILY_V6,
		}); err != nil && !errors.Is(err, syscall.EEXIST) {
			th.t.Fatalf("Failed to add IPv6 default route for docker table: %v",
				err)
		}
	}

	// Policy routing: traffic with src IP of the container → docker network
	// (used for Test <-> Internet)
	// IPv4
	ruleV4 := netlink.NewRule()
	ruleV4.Src = &net.IPNet{
		IP:   GetSrcIPv4ForInternetAccess(),
		Mask: net.CIDRMask(32, 32),
	}
	ruleV4.Table = dockerTable
	ruleV4.Priority = 500
	if err := netlink.RuleAdd(ruleV4); err != nil && !errors.Is(err, syscall.EEXIST) {
		th.t.Fatalf("Failed to add IPv4 container source rule: %v", err)
	}
	// IPv6
	if th.dockerGwIPv6 != nil {
		ruleV6 := netlink.NewRule()
		ruleV6.Src = &net.IPNet{
			IP:   GetSrcIPv6ForInternetAccess(),
			Mask: net.CIDRMask(128, 128),
		}
		ruleV6.Table = dockerTable
		ruleV6.Priority = 500
		if err := netlink.RuleAdd(ruleV6); err != nil && !errors.Is(err, syscall.EEXIST) {
			th.t.Fatalf("Failed to add IPv6 container source rule: %v", err)
		}
	}

	if !th.brokerInContainer {
		// No additional routing required if SDN is external
		return
	}

	// Determine SDN uplink interface
	sdnUplink, err := utils.GetEgressInterfaceForIP(sdnUplinkIP)
	if err != nil {
		th.t.Fatalf("Failed to determine SDN uplink interface: %v", err)
	}
	th.log.Debugf("SDN uplink interface: %s", sdnUplink)

	// Policy routing: traffic arriving from SDN uplink → docker network
	// (this is used for EVE / App / Proxy <-> Internet, when SDN is deployed inside
	// the container)
	// For both IPv4 and IPv6
	rule := netlink.NewRule()
	rule.IifName = sdnUplink
	rule.Table = dockerTable
	rule.Priority = 1000
	rule.Family = netlink.FAMILY_V4
	if err = netlink.RuleAdd(rule); err != nil && !errors.Is(err, syscall.EEXIST) {
		th.t.Fatalf("Failed to add SDN uplink IPv4 ingress rule: %v", err)
	}
	rule.Family = netlink.FAMILY_V6
	if err = netlink.RuleAdd(rule); err != nil && !errors.Is(err, syscall.EEXIST) {
		th.t.Fatalf("Failed to add SDN uplink IPv6 ingress rule: %v", err)
	}
}

// Onboard devices into Adam and potentially also apply the initial device configurations.
func (th *TestHarness) onboardEVEDevices() {
	th.devicesM.Lock()

	// Power-on EVE devices first.
	for _, dev := range th.devices {
		devCtrlReq := &api.DeviceControlRequest{
			ClientId:   th.brokerClientID,
			DeviceName: dev.name,
		}
		ctx, cancel := context.WithTimeout(th.ctx, brokerPowerOnEVEDeviceTimeout)
		_, err := th.brokerClient.PowerOnDevice(ctx, devCtrlReq)
		cancel()
		if err != nil {
			th.t.Fatalf("Failed to power on device %q: %v", dev.name, err)
		}
		th.log.Infof("Device %q powered on", dev.name)
	}

	// Perform onboarding in parallel.
	errCh := make(chan error, len(th.devices))
	for _, dev := range th.devices {
		go func(dev deviceState) {
			err := th.onboardEVEDevice(dev)
			errCh <- err
		}(*dev)
	}

	th.devicesM.Unlock()
	for range th.devices {
		err := <-errCh
		if err != nil {
			th.t.Fatal(err)
		}
	}
}

// onboardEVEDevice onboards a single EVE device into the Adam controller.
//
// It registers the device using its onboarding certificate and serial number,
// stores the assigned device UUID, and waits for the device to publish its
// ECDH certificate (required for encrypting parts of the device configuration).
//
// The provided deviceState is treated as read-only; shared state updates
// (UUID, ECDH certificate) are performed under devicesM lock.
func (th *TestHarness) onboardEVEDevice(dev deviceState) error {
	// Onboard device into Adam controller.
	ctx, cancel := context.WithTimeout(th.ctx, deviceOnboardTimeout)
	defer cancel()
	uuid, err := th.adamClient.OnboardDevice(ctx, dev.onboardCert, dev.serial)
	if err != nil {
		return fmt.Errorf("failed to onboard device %q: %v", dev.name, err)
	}

	// Input arg "dev" is intentionally read-only (not pointer).
	// Lock devicesM for a short moment and save the received device UUID.
	th.devicesM.Lock()
	th.devices[dev.name].ID = uuid
	th.devicesM.Unlock()
	th.log.Infof("Onboarded device %q (UUID: %s)", dev.name, uuid)

	// Wait for the device to publish the ECDH certificate.
	// This might be later needed to encrypt cipher blocks inside the device
	// configuration.
	ecdhCert, err := th.waitForDeviceECDHCert(dev.name, uuid)
	if err != nil {
		return err
	}
	th.devicesM.Lock()
	th.devices[dev.name].ecdhCert = ecdhCert
	th.devicesM.Unlock()
	th.log.Infof("Device %q published ECDH certificate", dev.name)
	return nil
}

func (th *TestHarness) isDeviceOnboarded(devName string) bool {
	th.devicesM.Lock()
	defer th.devicesM.Unlock()
	devState, found := th.devices[devName]
	return found && devState.ID != uuid.Nil
}

// Wait for the device to publish its ECDH certificate to the controller.
func (th *TestHarness) waitForDeviceECDHCert(
	devName string, devUUID uuid.UUID) (*x509.Certificate, error) {
	ctx, cancel := context.WithTimeout(th.ctx, deviceAttestTimeout)
	defer cancel()

	ticker := time.NewTicker(5 * time.Second)
	defer ticker.Stop()
	var errCount int

	for {
		select {
		case <-ctx.Done():
			return nil, fmt.Errorf("failed to get device %q ECDH certificate: %w",
				devName, ctx.Err())
		case <-ticker.C:
			cert, err := th.adamClient.GetDeviceECDHCert(ctx, devUUID)
			if err != nil {
				errCount++
				if errCount > 10 {
					return nil, fmt.Errorf(
						"failed to get device %q ECDH certificate: %w",
						devName, err)
				}
				th.log.Errorf("Temporary failure retrieving ECDH certificate "+
					"for device %q: %v; will retry", devName, err)
				continue
			}
			if cert != nil {
				return cert, nil
			}
			th.log.Infof("Waiting for device %q to publish ECDH certificate...",
				devName)
		}
	}
}

// If network model is not provided, generate default model
// with one interface per EVE node, all connected to the same network
// with DHCP enabled.
func (th *TestHarness) genDefaultNetworkModel(devNames []string) *api.NetworkModel {
	netModel := &api.NetworkModel{
		Bridges: []*api.Bridge{
			{
				LogicalLabel: "bridge",
			},
		},
		Networks: []*api.Network{
			{
				LogicalLabel: "network",
				Bridge:       "bridge",
				Ipv4: &api.NetworkIPConfig{
					Subnet: "172.20.20.0/24",
					GwIp:   "172.20.20.1",
					Dhcp: &api.DHCP{
						Enable:     true,
						DomainName: "evetest",
						Dns: &api.DNSClientConfig{
							PrivateDns: []string{"dns-server"},
						},
					},
				},
			},
		},
		Endpoints: &api.Endpoints{
			DnsServers: []*api.DNSServer{
				{
					Endpoint: &api.Endpoint{
						LogicalLabel: "dns-server",
						Fqdn:         "dns-server.test",
						Ipv4: &api.EndpointIPConfig{
							Subnet: "10.16.16.0/24",
							Ip:     "10.16.16.25",
						},
					},
					StaticEntries: []*api.DNSEntry{
						{
							FqdnSource: &api.DNSEntry_FqdnLiteral{
								FqdnLiteral: GetControllerHostname(),
							},
							IpSource: &api.DNSEntry_IpLiteral{
								IpLiteral: GetControllerIPv4().String(),
							},
						},
					},
					UpstreamServers: []string{"8.8.8.8", "1.1.1.1"},
				},
			},
		},
	}
	for _, devName := range devNames {
		portLabel := devName + "-eth0"
		eveMAC := utils.GenerateMAC(devName, portLabel)
		sdnMAC := utils.GenerateMAC(constants.SDNDeviceName, portLabel)
		netModel.Ports = append(netModel.Ports, &api.Port{
			LogicalLabel:  portLabel,
			EveDeviceName: devName,
			EveMacAddress: eveMAC.String(),
			SdnMacAddress: sdnMAC.String(),
			AdminUp:       true,
		})
		netModel.Bridges[0].Ports = append(netModel.Bridges[0].Ports, portLabel)
	}
	return netModel
}

// Connect to the SDN gRPC server.
func (th *TestHarness) connectToSDN(sdnUplinkIPs []string) {
	ctx, cancel := context.WithTimeout(th.ctx, sdnConnectTimeout)
	defer cancel()

	sdnGrpcPort := strconv.Itoa(int(viper.GetUint16(constants.SDNPortEnv)))
	retryInterval := 500 * time.Millisecond

	var lastErr error

	for {
		for _, sdnIP := range sdnUplinkIPs {
			select {
			case <-ctx.Done():
				// Timeout expired
				err := fmt.Errorf("unable to connect to SDN gRPC service "+
					"on any of the uplink IPs (%v): %w", sdnUplinkIPs, lastErr)
				th.t.Fatal(err)
				return
			default:
			}

			sdnAddr := net.JoinHostPort(sdnIP, sdnGrpcPort)
			// Note: no I/O is performed by grpc.NewClient, connection to SDN
			// is established with the first RPC call, which is GetStatus() -- see below.
			th.sdnConn, lastErr = grpc.NewClient(
				sdnAddr, grpc.WithTransportCredentials(insecure.NewCredentials()),
			)
			if lastErr == nil {
				th.sdnClient = api.NewSDNClient(th.sdnConn)
				var statusResp *api.SDNStatusResponse
				statusResp, lastErr = th.sdnClient.GetStatus(ctx, &api.SDNRequest{})
				if lastErr == nil {
					th.log.Infof("Connected to SDN gRPC at %s, SDN status: %s",
						sdnAddr, statusResp.String())
					return
				}
			}

			th.log.Warnf(
				"Failed to connect to SDN gRPC at %s (will retry): %v",
				sdnAddr, lastErr)
		}

		// Wait before the next retry round
		select {
		case <-ctx.Done():
			err := fmt.Errorf(
				"unable to connect to SDN gRPC service on any of the uplink IPs (%v): %w",
				sdnUplinkIPs, lastErr)
			th.t.Fatal(err)
			return
		case <-time.After(retryInterval):
		}
	}
}

// checkInternetConnectivity verifies that the test environment has the required
// Internet connectivity before running a test.
//
// It asks the SDN service to probe connectivity to a well-known external endpoint
// (currently www.google.com:443) and evaluates IPv4 and optionally IPv6 reachability.
// If the required connectivity is not available, the test is skipped rather than failed.
func (th *TestHarness) checkInternetConnectivity(req RequireInternetConnectivity) {
	ctx, cancel := context.WithTimeout(th.ctx, sdnTestInternetConnTimeout)
	th.log.Debug("Submitting request to test Internet connectivity")
	resp, err := th.sdnClient.CheckConnectivity(ctx, &api.SDNConnectivityRequest{
		Hostname: "www.google.com",
		Port:     443,
	})
	cancel()
	if err != nil {
		th.t.Fatal(err)
	}
	if !resp.ReachableOverIpv4 {
		th.t.Skipf("Test %q requires IPv4 Internet connectivity "+
			"which is (currently) not available", th.t.Name())
	}
	if !resp.ReachableOverIpv6 && req.RequireIPv6 {
		th.t.Skipf("Test %q requires IPv6 Internet connectivity "+
			"which is (currently) not available", th.t.Name())
	}
}

// Reuse devices if the requirements match the previous test
// and the reuse policy for this test allows it.
func (th *TestHarness) maybeReuseDevices(
	edgeDevReqs map[string]RequireEdgeDevice, netModel *api.NetworkModel) bool {
	// For simplicity, we will avoid reusing devices between tests when the network
	// model differs.
	th.netModelM.Lock()
	sameNetModel := proto.Equal(th.netModel, netModel)
	th.netModelM.Unlock()
	if !sameNetModel {
		return false
	}

	// Check edge device requirements in parallel.
	th.devicesM.Lock()
	if len(th.devices) != len(edgeDevReqs) {
		th.devicesM.Unlock()
		return false
	}
	for devName := range edgeDevReqs {
		_, devFound := th.devices[devName]
		if !devFound {
			th.devicesM.Unlock()
			return false
		}
	}
	errCh := make(chan error, len(th.devices))
	for devName, devReq := range edgeDevReqs {
		dev := th.devices[devName]
		go func(dev deviceState, newReq RequireEdgeDevice) {
			err := th.maybeReuseDevice(dev, devReq)
			errCh <- err
		}(*dev, devReq)
	}

	th.devicesM.Unlock()
	reuse := true
	for range th.devices {
		err := <-errCh
		if err != nil {
			if err == cannotReuseErr {
				reuse = false
			} else {
				th.t.Fatal(err)
			}
		}
	}
	return reuse
}

// cannotReuseErr is returned by maybeReuseDevice to signal that the device
// does not satisfy the new test requirements or reuse policy and therefore
// must be recreated instead of reused.
var cannotReuseErr = errors.New("cannot reuse device")

// maybeReuseDevice attempts to reuse an existing device for a new test run
// according to the provided requirements and reuse policy.
//
// It verifies that hardware/software requirements have not changed, performs
// any cleanup or lifecycle actions required by the reuse policy (e.g. clearing
// state, re-onboarding, rebooting), and ensures that the device is ready to
// fetch the latest configuration from the controller.
func (th *TestHarness) maybeReuseDevice(dev deviceState, newReq RequireEdgeDevice) error {

	// Cannot reuse device if requirements changed.
	prevReq := dev.requirement
	equalReqs := newReq.MinCPUs == prevReq.MinCPUs &&
		newReq.MinRAMInMB == prevReq.MinRAMInMB &&
		newReq.MinDiskSizeInMB == prevReq.MinDiskSizeInMB &&
		newReq.WithEVEVersion == prevReq.WithEVEVersion &&
		newReq.WithHypervisor == prevReq.WithHypervisor &&
		newReq.WithFilesystem == prevReq.WithFilesystem &&
		newReq.WithTPM == prevReq.WithTPM &&
		newReq.WithSoftSerial == prevReq.WithSoftSerial &&
		generics.EqualSets(newReq.WithUSBPassthrough, prevReq.WithUSBPassthrough) &&
		generics.EqualSets(newReq.WithPCIPassthrough, prevReq.WithPCIPassthrough)
	if !equalReqs {
		return cannotReuseErr
	}

	// Remove collect-info tarballs produced during the previous test.
	ctx, cancel := context.WithTimeout(th.ctx, 10*time.Second)
	err := th.runScriptOnEVEOverSSH(
		ctx, dev.name, "rm -rf /persist/eve-info/*", nil, nil, 0)
	cancel()
	if err != nil {
		// Just log warning and continue. This step is not crucial.
		th.log.Warnf("failed to clear /persist/eve-info for device %q: %v",
			dev.name, err)
	}

	// Re-use policies with no action in this method.
	switch newReq.DeviceReusePolicy {
	case UseAsIs:
		return nil
	case CreateFromScratchWithInstaller, CreateFromScratchWithLiveImage:
		return cannotReuseErr
	}

	// Trigger device re-onboarding.
	if newReq.DeviceReusePolicy == ReonboardEdgeDevice {
		th.log.Infof("Re-onboarding device %q for reuse purposes.", dev.name)

		// Clear device onboarding state, remove device certificates and clear TPM.
		shellScript := "rm -rf /persist/status/zedclient/OnboardingStatus && " +
			"mkdir -p /mnt && " +
			"eve config mount /mnt && " +
			"rm -f /mnt/device.* && " +
			"eve config unmount && " +
			"eve enter vtpm && " +
			"tpm2 clear"
		ctx, cancel = context.WithTimeout(th.ctx, 10*time.Second)
		err = th.runScriptOnEVEOverSSH(
			ctx, dev.name, shellScript, nil, nil, 0)
		cancel()
		if err != nil {
			return fmt.Errorf("failed to clear device onboarding state: %v", err)
		}

		// Remove device from the controller.
		ctx, cancel = context.WithTimeout(th.ctx, deviceRemoveTimeout)
		err = th.adamClient.RemoveDevice(ctx, dev.ID)
		cancel()
		if err != nil {
			return fmt.Errorf("failed to remove device %q from the controller: %v",
				dev.name, err)
		}

		// Forget device UUID.
		th.devicesM.Lock()
		th.devices[dev.name].ID = NilUUID
		th.devicesM.Unlock()
	}

	// Reboot device.
	switch newReq.DeviceReusePolicy {
	case RebootEdgeDevice, ResetDeviceConfigAndReboot, ReonboardEdgeDevice:
		th.log.Infof("Rebooting device %q for reuse purposes.", dev.name)
		devCtrlReq := &api.DeviceControlRequest{
			ClientId:   th.brokerClientID,
			DeviceName: dev.name,
		}
		ctx, cancel := context.WithTimeout(th.ctx, brokerRebootEVEDeviceTimeout)
		_, err := th.brokerClient.RebootDevice(ctx, devCtrlReq)
		cancel()
		if err != nil {
			return fmt.Errorf("failed to reboot device %q: %v", dev.name, err)
		}
	}

	if newReq.DeviceReusePolicy == ReonboardEdgeDevice {
		// Wait for device to onboard.
		if err = th.onboardEVEDevice(dev); err != nil {
			return err
		}
	}

	// Reset device config.
	switch newReq.DeviceReusePolicy {
	case ResetDeviceConfig, ResetDeviceConfigAndReboot, ReonboardEdgeDevice:
		// TODO: apply "empty" config into the controller (without apps, NIs, volumes)
		return fmt.Errorf("device config reseting is not implemented")
	}

	// Wait for device to fetch the latest (potentially cleared) config.
	fetchConfigTimeout := deviceApplyConfigTimeout
	switch newReq.DeviceReusePolicy {
	case RebootEdgeDevice, ResetDeviceConfigAndReboot:
		fetchConfigTimeout += deviceRebootTimeout
	}
	th.log.Infof(
		"Waiting for (reused) device %q to fetch the latest config...",
		dev.name)
	ctx, cancel = context.WithTimeout(th.ctx, fetchConfigTimeout)
	err = th.adamClient.WaitUntilDevRequest(ctx, dev.ID, "/config")
	cancel()
	if err != nil {
		return fmt.Errorf(
			"re-used device %q failed to fetch the latest config: %v",
			dev.name, err)
	}
	return nil
}

// gatherLogsFromAllDevices retrieves device logs from all known EVE devices
// and stores them as artifact files under the test artifact directory.
//
// For each device, logs are fetched via Adam with a bounded timeout and
// written into a file named "<device-name>.log". Failures for individual
// devices are logged but do not abort processing of other devices.
func (th *TestHarness) gatherLogsFromAllDevices() {
	th.log.Infof("Gathering logs from all EVE devices...")
	th.devicesM.Lock()
	defer th.devicesM.Unlock()

	for _, dev := range th.devices {
		filePath := filepath.Join(
			th.test.artifactDir, fmt.Sprintf("%s.log", dev.name))

		outFile, err := os.Create(filePath)
		if err != nil {
			th.log.Errorf(
				"Unable to create log artifact file %q for device %q: %v",
				filePath, dev.name, err)
			continue
		}

		ctx, cancel := context.WithTimeout(th.ctx, gatherLogsTimeout)
		logWriter := &logger.PlainDeviceLogFile{OutFile: outFile}
		err = th.adamClient.WriteDeviceLogs(ctx, dev.ID, nil, logWriter, false)
		cancel()
		if err != nil {
			th.log.Errorf(
				"Failed to retrieve logs for device %q: %v",
				dev.name, err)
		}

		if err = outFile.Close(); err != nil {
			th.log.Errorf(
				"Failed to close log artifact file %q for device %q: %v",
				filePath, dev.name, err)
		}
	}
}

// gatherConsoleOutputFromAllDevices retrieves the current console output
// from all known EVE devices via the broker and stores it as artifact files.
//
// For each device, console output is requested with a bounded timeout and
// written into a file named "<device-name>.console". Failures are logged per
// device and do not interrupt processing of remaining devices.
func (th *TestHarness) gatherConsoleOutputFromAllDevices() {
	th.log.Infof("Gathering console output from all EVE devices...")
	th.devicesM.Lock()
	defer th.devicesM.Unlock()

	for _, dev := range th.devices {
		ctx, cancel := context.WithTimeout(
			th.ctx, brokerGetConsoleOutputTimeout)

		resp, err := th.brokerClient.GetDeviceConsoleOutput(
			ctx,
			&api.DeviceControlRequest{
				ClientId:   th.brokerClientID,
				DeviceName: dev.name,
			},
		)
		cancel()

		if err != nil {
			th.log.Errorf(
				"Failed to retrieve console output for device %q: %v",
				dev.name, err)
			continue
		}

		filePath := filepath.Join(
			th.test.artifactDir, fmt.Sprintf("%s.console", dev.name))

		if err = os.WriteFile(filePath,
			[]byte(resp.ConsoleOutput), 0666); err != nil {
			th.log.Errorf(
				"Failed to write console artifact file %q for device %q: %v",
				filePath, dev.name, err)
		}
	}
}

type infoMsgFileWriter struct {
	outFile io.Writer
}

func (w *infoMsgFileWriter) Write(msg *eveinfo.ZInfoMsg) error {
	_, err := fmt.Fprintf(w.outFile, "%s\n\n", msg.String())
	return err
}

// gatherInfoMsgsFromAllDevices retrieves published informational messages
// from all known EVE devices and stores them as artifact files under the
// test artifact directory.
//
// For each device, info messages are fetched via Adam with a bounded timeout
// and written into a file named "<device-name>.info". Errors encountered for
// individual devices are logged but do not prevent processing of others.
func (th *TestHarness) gatherInfoMsgsFromAllDevices() {
	th.log.Infof("Gathering published info messages from all EVE devices...")
	th.devicesM.Lock()
	defer th.devicesM.Unlock()

	for _, dev := range th.devices {
		filePath := filepath.Join(
			th.test.artifactDir, fmt.Sprintf("%s.info", dev.name))

		outFile, err := os.Create(filePath)
		if err != nil {
			th.log.Errorf(
				"Unable to create artifact file %q with info messages for device %q: %v",
				filePath, dev.name, err)
			continue
		}
		writer := &infoMsgFileWriter{outFile: outFile}

		ctx, cancel := context.WithTimeout(th.ctx, gatherInfoMsgsTimeout)
		err = th.adamClient.WriteDeviceInfoMsgs(ctx, dev.ID, nil, writer, false)
		cancel()

		if err != nil {
			th.log.Errorf(
				"Failed to retrieve info messages for device %q: %v",
				dev.name, err)
		}

		if err = outFile.Close(); err != nil {
			th.log.Errorf(
				"Failed to close artifact file %q with info messages for device %q: %v",
				filePath, dev.name, err)
		}
	}
}

// Try to obtain collect-info tarball from every EVE device.
func (th *TestHarness) collectInfoFromAllDevices() {
	var wg sync.WaitGroup
	th.log.Infof("Trying to obtain collect-info tarball from every EVE device...")

	const maxAttempts = 3

	th.devicesM.Lock()
	for _, dev := range th.devices {
		wg.Add(1)
		go func(devName string) {
			defer wg.Done()

			var lastErr error
			for attempt := 1; attempt <= maxAttempts; attempt++ {
				ctx, cancel := context.WithTimeout(th.ctx, collectInfoTimeout)
				_, err := th.collectInfoFromDevice(ctx, devName)
				cancel()

				if err == nil {
					if attempt > 1 {
						th.log.Infof(
							"Successfully collected info from device %q on attempt %d/%d",
							devName, attempt, maxAttempts,
						)
					}
					return
				}

				lastErr = err
				th.log.Warnf(
					"Failed to collect info from device %q (attempt %d/%d): %v",
					devName, attempt, maxAttempts, err,
				)
			}

			th.log.Errorf(
				"Giving up collecting info from device %q after %d attempts: %v",
				devName, maxAttempts, lastErr,
			)
		}(dev.name)
	}
	th.devicesM.Unlock()

	wg.Wait()
}

// Try to obtain collect-info tarball from the given EVE device.
func (th *TestHarness) collectInfoFromDevice(
	ctx context.Context, devName string) (filePath string, err error) {
	// Run collect-info.sh and capture its output.
	ciStdout := logger.LogWriter{
		Log:    th.log,
		Level:  logrus.DebugLevel,
		Prefix: fmt.Sprintf("collect-info (%s): ", devName),
	}
	// Expect collect-info.sh to produce stdout output at least once per minute.
	stdoutWatchdogTimeout := time.Minute
	err = th.runScriptOnEVEOverSSH(ctx,
		devName, "collect-info.sh", ciStdout, nil, stdoutWatchdogTimeout)
	if err != nil {
		err = fmt.Errorf("collect-info.sh failed on device %q: %v",
			devName, err)
		return "", err
	}

	// Prepare output file for the collect-info artifact.
	filePath = filepath.Join(th.test.artifactDir,
		fmt.Sprintf("eve-info-%s.tar", devName),
	)
	outFile, err := os.Create(filePath)
	if err != nil {
		err = fmt.Errorf("failed to create collect-info artifact for device %q: %v",
			devName, err)
		return "", err
	}
	defer outFile.Close()

	// Archive the collected info (alongside any other previously collected infos)
	// and stream it to the artifact file.
	// We should see a constant stream of tar-ed data coming.
	stdoutWatchdogTimeout = 20 * time.Second
	err = th.runScriptOnEVEOverSSH(ctx,
		devName, "tar -C /persist -cf - eve-info", outFile, nil, stdoutWatchdogTimeout)
	if err != nil {
		err = fmt.Errorf("failed to archive collect-info from device %q: %v",
			devName, err)
		return "", err
	}

	th.log.Infof("Received collect-info tarball from EVE device %q", devName)
	return filePath, nil
}

type watchdogWriter struct {
	dst        io.Writer
	activityCh chan struct{}
}

func (w *watchdogWriter) Write(p []byte) (int, error) {
	select {
	case w.activityCh <- struct{}{}:
	default:
	}
	return w.dst.Write(p)
}

// runScriptOnEVEOverSSH executes the provided shellScript on the target EVE device
// over SSH.
func (th *TestHarness) runScriptOnEVEOverSSH(ctx context.Context, devName string,
	shellScript string, stdout, stderr io.Writer,
	stdoutWatchdogTimeout time.Duration) error {
	eveIPs, err := th.collectEVEIPs(devName)
	if err != nil {
		return err
	}

	// Find which of the EVE IPs is reachable via SSH.
	var reachableEveIP string
	const maxRetries = 3
	for _, eveIP := range eveIPs {
		for attempt := 1; attempt <= maxRetries; attempt++ {
			dialer := net.Dialer{}
			eveConn, err := dialer.DialContext(
				ctx, "tcp", net.JoinHostPort(eveIP, "22"))
			if err != nil {
				th.log.Errorf(
					"Attempt %d/%d: failed to connect to EVE SSH endpoint %s:22: %v",
					attempt, maxRetries, eveIP, err,
				)
				continue
			}

			th.log.Debugf(
				"Connected to EVE SSH endpoint %s:22 on attempt %d/%d",
				eveIP, attempt, maxRetries,
			)
			_ = eveConn.Close()
			reachableEveIP = eveIP
			break
		}
		if reachableEveIP != "" {
			break
		}
	}
	if reachableEveIP == "" {
		return fmt.Errorf(
			"unable to establish SSH connection to EVE device %q using detected IPs",
			devName,
		)
	}

	// Execute the shell script on the reachable EVE device by piping it to `sh -s`
	// over SSH.
	cmd := exec.CommandContext(
		ctx,
		"ssh",
		"-i", "/root/.ssh/eve_rsa",
		"-o", "StrictHostKeyChecking=no",
		"-o", "UserKnownHostsFile=/dev/null",
		"-o", "IdentitiesOnly=yes",
		"root@"+reachableEveIP,
		"sh", "-s",
	)
	cmd.Stdin = strings.NewReader(shellScript)
	cmd.Stderr = stderr

	if stdoutWatchdogTimeout > 0 {
		activityCh := make(chan struct{}, 1)
		cmd.Stdout = &watchdogWriter{dst: stdout, activityCh: activityCh}
		doneCh := make(chan struct{})
		defer close(doneCh)

		go func() {
			timer := time.NewTimer(stdoutWatchdogTimeout)
			defer timer.Stop()

			for {
				select {
				case <-activityCh:
					if !timer.Stop() {
						<-timer.C
					}
					timer.Reset(stdoutWatchdogTimeout)

				case <-timer.C:
					th.log.Errorf("Killing SSH command due to stdout inactivity "+
						"(device: %s, script: %s)", devName, shellScript)
					_ = cmd.Process.Kill()
					return

				case <-doneCh:
					return
				}
			}
		}()

	} else {
		cmd.Stdout = stdout
	}
	return cmd.Run()
}
