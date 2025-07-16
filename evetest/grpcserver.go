package evetest

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net"
	"os"
	"path/filepath"
	"strings"

	eveinfo "github.com/lf-edge/eve-api/go/info"
	evemetrics "github.com/lf-edge/eve-api/go/metrics"
	"github.com/lf-edge/eve/evetest/constants"
	api "github.com/lf-edge/eve/evetest/grpcapi/go"
	"github.com/lf-edge/eve/evetest/logger"
	"github.com/lf-edge/eve/evetest/utils"
	uuid "github.com/satori/go.uuid"
	"github.com/spf13/viper"
	"google.golang.org/grpc"
)

type infoMsgGrpcIterator[T any] struct {
	stream grpc.ServerStreamingServer[T]
	mapper func(*eveinfo.ZInfoMsg) (*T, error)
}

func (w *infoMsgGrpcIterator[T]) Iterate(msg *eveinfo.ZInfoMsg) (bool, error) {
	resp, err := w.mapper(msg)
	if err != nil {
		return false, err
	}
	return false, w.stream.Send(resp)
}

type metricMsgGrpcIterator[T any] struct {
	stream grpc.ServerStreamingServer[T]
	// mapper extracts a response from a metrics message; returns nil to skip.
	mapper func(*evemetrics.ZMetricMsg) (*T, error)
}

func (w *metricMsgGrpcIterator[T]) Iterate(msg *evemetrics.ZMetricMsg) (bool, error) {
	resp, err := w.mapper(msg)
	if err != nil {
		return false, err
	}
	if resp == nil {
		return false, nil
	}
	return false, w.stream.Send(resp)
}

// Continue test execution until the end/failure or another checkpoint.
func (th *TestHarness) Continue(
	ctx context.Context, req *api.ContinueRequest) (*api.ContinueResponse, error) {
	th.checkpointM.Lock()
	defer th.checkpointM.Unlock()
	switch {
	case th.pausedAtCheckpoint != "":
		th.resume <- struct{}{}
		th.pausedAtCheckpoint = ""
	case th.pausedOnFailure != "":
		th.resume <- struct{}{}
		th.pausedOnFailure = ""
	}
	th.pauseAtCheckpoint = req.GetUntilCheckpoint()
	return &api.ContinueResponse{}, nil
}

// Exit the test early, marking it as skipped or incomplete.
func (th *TestHarness) Exit(
	ctx context.Context, req *api.ExitRequest) (*api.ExitResponse, error) {
	// TODO
	return nil, errors.New("not implemented")
}

// Restart the current test from the beginning.
func (th *TestHarness) Restart(
	ctx context.Context, req *api.RestartRequest) (*api.RestartResponse, error) {
	// TODO
	return nil, errors.New("not implemented")
}

// Status returns current test execution state and active devices.
func (th *TestHarness) Status(
	ctx context.Context, req *api.StatusRequest) (*api.StatusResponse, error) {
	// Determine test/suite name.
	var testName string
	var suiteName string
	th.testM.Lock()
	if th.suite != nil {
		suiteName = th.suite.name
	}
	testName = th.test.name
	th.testM.Unlock()

	// Collect the specifications of all deployed EVE devices.
	var eveDevices []*api.EVEDeviceStatus
	th.devicesM.Lock()
	for _, dev := range th.devices {
		eveDevices = append(eveDevices, &api.EVEDeviceStatus{
			Spec:       dev.spec,
			State:      dev.state,
			Interfaces: dev.interfaces,
		})
	}
	th.devicesM.Unlock()

	// Determine current checkpoint/failure.
	th.checkpointM.Lock()
	checkpoint := th.pausedAtCheckpoint
	failure := th.pausedOnFailure
	th.checkpointM.Unlock()

	return &api.StatusResponse{
		EvetestVersion:    viper.GetString(constants.VersionEnv),
		TestName:          testName,
		TestSuiteName:     suiteName,
		EveDevices:        eveDevices,
		Paused:            checkpoint != "" || failure != "",
		CurrentCheckpoint: checkpoint,
		TestFailure:       failure,
	}, nil
}

// HardRebootEVEDevice forces immediate power cycle of the EVE device.
func (th *TestHarness) HardRebootEVEDevice(
	ctx context.Context, req *api.EVEDeviceRequest) (*api.EVERebootResponse, error) {
	devName, _, err := th.resolveEVEDeviceName(req.GetDeviceName())
	if err != nil {
		return nil, err
	}
	_, err = th.brokerClient.RebootDevice(ctx, &api.DeviceControlRequest{
		ClientId:   th.brokerClientID,
		DeviceName: devName,
	})
	return &api.EVERebootResponse{}, err
}

// SoftRebootEVEDevice request a clean OS-level reboot on the EVE device.
func (th *TestHarness) SoftRebootEVEDevice(
	ctx context.Context, req *api.EVEDeviceRequest) (*api.EVERebootResponse, error) {
	devName, _, err := th.resolveEVEDeviceName(req.GetDeviceName())
	if err != nil {
		return nil, err
	}
	err = th.runScriptOnEVEOverSSH(ctx, devName, "reboot", nil, nil, 0)
	return &api.EVERebootResponse{}, err
}

// GetEVEConfig fetches the current configuration submitted to the EVE device.
func (th *TestHarness) GetEVEConfig(
	ctx context.Context, req *api.EVEDeviceRequest) (*api.EVEConfigResponse, error) {
	th.devicesM.Lock()
	defer th.devicesM.Unlock()
	devName, _, err := th.resolveEVEDeviceNameLocked(req.GetDeviceName())
	if err != nil {
		return nil, err
	}
	config := th.devices[devName].config.EdgeDevConfig
	return &api.EVEConfigResponse{Config: config}, nil
}

// GetEVEInfo streams real-time system info from EVE device (ZInfoDevice).
func (th *TestHarness) GetEVEInfo(
	req *api.EVEDeviceStreamableRequest, stream api.Evetest_GetEVEInfoServer) error {
	devName, devUUID, err := th.resolveEVEDeviceName(req.GetDeviceName())
	if err != nil {
		return err
	}
	if devUUID == uuid.Nil {
		return fmt.Errorf("device %q is not onboarded", devName)
	}
	matcher := func(msg *eveinfo.ZInfoMsg) bool {
		return msg.GetZtype() == eveinfo.ZInfoTypes_ZiDevice &&
			msg.GetDinfo() != nil
	}
	iterator := &infoMsgGrpcIterator[api.EVEInfoResponse]{
		stream: stream,
		mapper: func(msg *eveinfo.ZInfoMsg) (*api.EVEInfoResponse, error) {
			return &api.EVEInfoResponse{
				DeviceInfo: msg.GetDinfo(),
			}, nil
		},
	}
	return th.adamClient.IterateDeviceInfoMsgs(stream.Context(), devUUID, matcher,
		iterator, req.GetFollow())
}

// GetEVEMetrics streams real-time metrics from EVE device (deviceMetric).
func (th *TestHarness) GetEVEMetrics(
	req *api.EVEDeviceStreamableRequest, stream api.Evetest_GetEVEMetricsServer) error {
	devName, devUUID, err := th.resolveEVEDeviceName(req.GetDeviceName())
	if err != nil {
		return err
	}
	if devUUID == uuid.Nil {
		return fmt.Errorf("device %q is not onboarded", devName)
	}
	iterator := &metricMsgGrpcIterator[api.EVEMetricsResponse]{
		stream: stream,
		mapper: func(msg *evemetrics.ZMetricMsg) (*api.EVEMetricsResponse, error) {
			dm := msg.GetDm()
			if dm == nil {
				return nil, nil
			}
			return &api.EVEMetricsResponse{DeviceMetrics: dm}, nil
		},
	}
	return th.adamClient.IterateDeviceMetrics(
		stream.Context(), devUUID, iterator, req.GetFollow())
}

// GetEVELogs streams logs from EVE device (agent logs, kernel logs, etc).
func (th *TestHarness) GetEVELogs(
	req *api.EVEDeviceStreamableRequest, stream api.Evetest_GetEVELogsServer) error {
	devName, devUUID, err := th.resolveEVEDeviceName(req.GetDeviceName())
	if err != nil {
		return err
	}
	if devUUID == uuid.Nil {
		return fmt.Errorf("device %q is not onboarded", devName)
	}
	logIterator := &logger.GrpcDeviceLogStreamer{Stream: stream}
	return th.adamClient.IterateDeviceLogs(
		stream.Context(), devUUID, nil, logIterator, req.GetFollow())
}

// GetEVEConsoleOutput returns the full console output from the EVE device.
func (th *TestHarness) GetEVEConsoleOutput(
	ctx context.Context, req *api.EVEDeviceRequest) (*api.ConsoleOutputResponse, error) {
	devName, _, err := th.resolveEVEDeviceName(req.GetDeviceName())
	if err != nil {
		return nil, err
	}
	return th.brokerClient.GetDeviceConsoleOutput(ctx, &api.DeviceControlRequest{
		ClientId:   th.brokerClientID,
		DeviceName: devName,
	})
}

// GetAppInfo streams application-specific info from a device (ZInfoApp).
func (th *TestHarness) GetAppInfo(
	req *api.AppRequest, stream api.Evetest_GetAppInfoServer) error {
	devName, devUUID, err := th.resolveEVEDeviceName(req.GetDeviceName())
	if err != nil {
		return err
	}
	if devUUID == uuid.Nil {
		return fmt.Errorf("device %q is not onboarded", devName)
	}
	matcher := func(msg *eveinfo.ZInfoMsg) bool {
		if msg.GetZtype() != eveinfo.ZInfoTypes_ZiApp {
			return false
		}
		appInfo := msg.GetAinfo()
		if appInfo == nil {
			return false
		}
		return appInfo.GetAppID() == req.GetAppNameOrUuid() ||
			appInfo.GetAppName() == req.GetAppNameOrUuid()
	}
	iterator := &infoMsgGrpcIterator[api.AppInfoResponse]{
		stream: stream,
		mapper: func(msg *eveinfo.ZInfoMsg) (*api.AppInfoResponse, error) {
			return &api.AppInfoResponse{
				AppInfo: msg.GetAinfo(),
			}, nil
		},
	}
	return th.adamClient.IterateDeviceInfoMsgs(stream.Context(), devUUID, matcher,
		iterator, req.GetFollow())
}

// GetAppMetrics streams application-level metrics from a device (appMetric).
func (th *TestHarness) GetAppMetrics(
	req *api.AppRequest, stream api.Evetest_GetAppMetricsServer) error {
	devName, devUUID, err := th.resolveEVEDeviceName(req.GetDeviceName())
	if err != nil {
		return err
	}
	if devUUID == uuid.Nil {
		return fmt.Errorf("device %q is not onboarded", devName)
	}
	appNameOrUUID := req.GetAppNameOrUuid()
	iterator := &metricMsgGrpcIterator[api.AppMetricsResponse]{
		stream: stream,
		mapper: func(msg *evemetrics.ZMetricMsg) (*api.AppMetricsResponse, error) {
			for _, am := range msg.GetAm() {
				if am.GetAppID() == appNameOrUUID || am.GetAppName() == appNameOrUUID {
					return &api.AppMetricsResponse{AppMetrics: am}, nil
				}
			}
			return nil, nil
		},
	}
	return th.adamClient.IterateDeviceMetrics(
		stream.Context(), devUUID, iterator, req.GetFollow())
}

// GetAppLogs streams logs from an EVE-managed application.
func (th *TestHarness) GetAppLogs(
	req *api.AppRequest, stream api.Evetest_GetAppLogsServer) error {
	devName, devUUID, err := th.resolveEVEDeviceName(req.GetDeviceName())
	if err != nil {
		return err
	}
	if devUUID == uuid.Nil {
		return fmt.Errorf("device %q is not onboarded", devName)
	}
	appUUID, err := th.resolveAppUUID(stream.Context(), devUUID, req.GetAppNameOrUuid())
	if err != nil {
		return err
	}
	logIterator := &logger.GrpcDeviceLogStreamer{Stream: stream}
	return th.adamClient.IterateAppLogs(
		stream.Context(), devUUID, appUUID, nil, logIterator, req.GetFollow())
}

// GetAppFlowLogs streams flow logs and DNS request logs for an application.
func (th *TestHarness) GetAppFlowLogs(
	req *api.AppRequest, stream api.Evetest_GetAppFlowLogsServer) error {
	// TODO
	return errors.New("not implemented")
}

// GetNIInfo streams info (ZInfoNetworkInstance) about a network instance (NI).
func (th *TestHarness) GetNIInfo(
	req *api.NIRequest, stream api.Evetest_GetNIInfoServer) error {
	devName, devUUID, err := th.resolveEVEDeviceName(req.GetDeviceName())
	if err != nil {
		return err
	}
	if devUUID == uuid.Nil {
		return fmt.Errorf("device %q is not onboarded", devName)
	}
	matcher := func(msg *eveinfo.ZInfoMsg) bool {
		if msg.GetZtype() != eveinfo.ZInfoTypes_ZiNetworkInstance {
			return false
		}
		niInfo := msg.GetNiinfo()
		if niInfo == nil {
			return false
		}
		return niInfo.GetNetworkID() == req.GetNiNameOrUuid() ||
			niInfo.GetDisplayname() == req.GetNiNameOrUuid()
	}
	iterator := &infoMsgGrpcIterator[api.NIInfoResponse]{
		stream: stream,
		mapper: func(msg *eveinfo.ZInfoMsg) (*api.NIInfoResponse, error) {
			return &api.NIInfoResponse{
				NiInfo: msg.GetNiinfo(),
			}, nil
		},
	}
	return th.adamClient.IterateDeviceInfoMsgs(stream.Context(), devUUID, matcher,
		iterator, req.GetFollow())
}

// GetNIMetrics streams metrics (ZMetricNetworkInstance) for a network instance.
func (th *TestHarness) GetNIMetrics(
	req *api.NIRequest, stream api.Evetest_GetNIMetricsServer) error {
	devName, devUUID, err := th.resolveEVEDeviceName(req.GetDeviceName())
	if err != nil {
		return err
	}
	if devUUID == uuid.Nil {
		return fmt.Errorf("device %q is not onboarded", devName)
	}
	niNameOrUUID := req.GetNiNameOrUuid()
	iterator := &metricMsgGrpcIterator[api.NIMetricsResponse]{
		stream: stream,
		mapper: func(msg *evemetrics.ZMetricMsg) (*api.NIMetricsResponse, error) {
			for _, nm := range msg.GetNm() {
				if nm.GetNetworkID() == niNameOrUUID || nm.GetDisplayname() == niNameOrUUID {
					return &api.NIMetricsResponse{NiMetrics: nm}, nil
				}
			}
			return nil, nil
		},
	}
	return th.adamClient.IterateDeviceMetrics(
		stream.Context(), devUUID, iterator, req.GetFollow())
}

// GetClusterInfo streams summary information for the entire Kubernetes cluster.
// ClusterRequest carries no DeviceName; the single onboarded device is used
// (returns an error if multiple devices are onboarded).
func (th *TestHarness) GetClusterInfo(
	req *api.ClusterRequest, stream api.Evetest_GetClusterInfoServer) error {
	_, devUUID, err := th.resolveEVEDeviceName("")
	if err != nil {
		return err
	}
	if devUUID == uuid.Nil {
		return fmt.Errorf("no device is onboarded")
	}
	matcher := func(msg *eveinfo.ZInfoMsg) bool {
		return msg.GetZtype() == eveinfo.ZInfoTypes_ZiKubeCluster &&
			msg.GetClusterInfo() != nil
	}
	iterator := &infoMsgGrpcIterator[api.ClusterInfoResponse]{
		stream: stream,
		mapper: func(msg *eveinfo.ZInfoMsg) (*api.ClusterInfoResponse, error) {
			return &api.ClusterInfoResponse{
				ClusterInfo: msg.GetClusterInfo(),
			}, nil
		},
	}
	return th.adamClient.IterateDeviceInfoMsgs(stream.Context(), devUUID,
		matcher, iterator, req.GetFollow())
}

// GetClusterMetrics streams metrics for the Kubernetes cluster (KubeClusterMetrics).
// ClusterRequest carries no DeviceName; the single onboarded device is used
// (returns an error if multiple devices are onboarded).
func (th *TestHarness) GetClusterMetrics(
	req *api.ClusterRequest, stream api.Evetest_GetClusterMetricsServer) error {
	_, devUUID, err := th.resolveEVEDeviceName("")
	if err != nil {
		return err
	}
	if devUUID == uuid.Nil {
		return fmt.Errorf("no device is onboarded")
	}
	iterator := &metricMsgGrpcIterator[api.ClusterMetricsResponse]{
		stream: stream,
		mapper: func(msg *evemetrics.ZMetricMsg) (*api.ClusterMetricsResponse, error) {
			cm := msg.GetCm()
			if cm == nil {
				return nil, nil
			}
			return &api.ClusterMetricsResponse{ClusterMetrics: cm}, nil
		},
	}
	return th.adamClient.IterateDeviceMetrics(
		stream.Context(), devUUID, iterator, req.GetFollow())
}

// resolveAppUUID parses appNameOrUUID as a UUID.
// If it is not a valid UUID, it iterates historical info messages for devUUID
// to find an app whose name matches, and returns that app's UUID.
func (th *TestHarness) resolveAppUUID(
	ctx context.Context, devUUID uuid.UUID, appNameOrUUID string) (uuid.UUID, error) {
	if appUUID, err := uuid.FromString(appNameOrUUID); err == nil {
		return appUUID, nil
	}
	// Name lookup: iterate historical app info messages.
	var found uuid.UUID
	err := th.adamClient.IterateDeviceInfoMsgs(ctx, devUUID,
		func(msg *eveinfo.ZInfoMsg) bool {
			return msg.GetZtype() == eveinfo.ZInfoTypes_ZiApp
		},
		infoMsgIterFn(func(msg *eveinfo.ZInfoMsg) (bool, error) {
			ainfo := msg.GetAinfo()
			if ainfo.GetAppName() == appNameOrUUID {
				if id, err := uuid.FromString(ainfo.GetAppID()); err == nil {
					found = id
					return true, nil
				}
			}
			return false, nil
		}),
		false,
	)
	if err != nil {
		return uuid.Nil, fmt.Errorf("failed to look up app %q: %w", appNameOrUUID, err)
	}
	if found == uuid.Nil {
		return uuid.Nil, fmt.Errorf("app %q not found on device", appNameOrUUID)
	}
	return found, nil
}

// CollectInfo retrieves debug information from a device (eve-info tarball).
func (th *TestHarness) CollectInfo(
	ctx context.Context, req *api.EVEDeviceRequest) (*api.CollectInfoResponse, error) {
	artifactHostDir := viper.GetString(constants.ExternalArtifactDirEnv)
	if artifactHostDir == "" {
		return nil, fmt.Errorf(
			"%s is not defined: no artifact directory is mounted into the evetest "+
				"container, so collect-info output cannot be shared with the host",
			constants.EnvPrefix+constants.ExternalArtifactDirEnv,
		)
	}

	devName, _, err := th.resolveEVEDeviceName(req.GetDeviceName())
	if err != nil {
		return nil, err
	}

	// collectInfoFromDevice returns the artifact path inside the container,
	// i.e. under /artifacts
	outputFileContainer, err := th.collectInfoFromDevice(ctx, devName)
	if err != nil {
		return nil, err
	}

	// Translate container artifact path to host path.
	// The evetest container mounts:
	//   -v $(EVETEST_COLLECT_ARTIFACTS):/artifacts
	const artifactContainerDir = "/artifacts/"
	if !strings.HasPrefix(outputFileContainer, artifactContainerDir) {
		return nil, fmt.Errorf("unexpected artifact path %q (expected under %q)",
			outputFileContainer, artifactContainerDir,
		)
	}

	outputFileHost := filepath.Join(
		artifactHostDir, strings.TrimPrefix(outputFileContainer, artifactContainerDir))
	return &api.CollectInfoResponse{
		ArtifactPath: outputFileHost,
	}, nil
}

// ConnectSSHToEVE establishes TCP proxy for a SSH session into an EVE device.
func (th *TestHarness) ConnectSSHToEVE(
	ctx context.Context, req *api.EVEDeviceRequest) (*api.TCPProxyResponse, error) {
	devName, _, err := th.resolveEVEDeviceName(req.GetDeviceName())
	if err != nil {
		return nil, err
	}

	// Start listener on docker interface IP and a random unused port.
	ln, err := net.Listen(
		"tcp", net.JoinHostPort(th.dockerIntfIPv4.String(), "0"))
	if err != nil {
		return nil, fmt.Errorf(
			"failed to start EVE device %q SSH proxy listener on %s: %w",
			devName, th.dockerIntfIPv4.String(), err,
		)
	}

	// Extract the dynamically assigned port.
	addr, ok := ln.Addr().(*net.TCPAddr)
	if !ok {
		_ = ln.Close()
		return nil, fmt.Errorf("unexpected listener address type: %T", ln.Addr())
	}
	th.log.Infof("Started EVE device %q SSH proxy on %s:%d",
		devName, th.dockerIntfIPv4.String(), addr.Port)

	th.wg.Add(1)
	go th.runSSHProxyForEVE(ln, devName)

	th.tcpProxiesM.Lock()
	th.tcpProxies[addr.Port] = &tcpProxy{
		listener:   ln,
		devName:    devName,
		forConsole: false,
	}
	th.tcpProxiesM.Unlock()

	return &api.TCPProxyResponse{
		ProxyIpAddress: th.dockerIntfIPv4.String(),
		ProxyPort:      uint32(addr.Port),
	}, nil
}

func (th *TestHarness) runSSHProxyForEVE(listener net.Listener, devName string) {
	defer th.wg.Done()
	for {
		conn, err := listener.Accept()
		if err != nil {
			// Detect listener shutdown
			if errors.Is(err, net.ErrClosed) ||
				strings.Contains(err.Error(), "use of closed network connection") {
				th.log.Debugf("EVE SSH proxy listener closed, stopping accept loop")
				return
			}

			th.log.Errorf("EVE SSH proxy accept error: %v", err)
			continue
		}
		th.log.Infof("Client connected to EVE SSH proxy from %s", conn.RemoteAddr())

		th.wg.Add(1)
		go func(c net.Conn) {
			defer th.wg.Done()
			defer c.Close()

			// Get a reachable EVE IP using the existing helper
			eveIP, err := th.getReachableEVEIP(th.ctx, devName)
			if err != nil {
				th.log.Errorf("Unable to establish SSH connection to EVE device %q: %v",
					devName, err)
				return
			}

			// Dial SSH to the reachable IP
			dialer := net.Dialer{}
			addr := net.JoinHostPort(eveIP, "22")
			eveConn, err := dialer.DialContext(th.ctx, "tcp", addr)
			if err != nil {
				th.log.Errorf("Failed to connect to EVE device %q SSH endpoint %s:22: %v",
					devName, eveIP, err)
				return
			}
			defer eveConn.Close()
			th.log.Debugf("Connected to EVE device %q SSH endpoint %s:22", devName, eveIP)

			// Run TCP proxy.
			evePipe := utils.ReadWriterPipe{
				PipeName: "EVE SSH connection",
				RW:       eveConn,
				Buf:      make([]byte, os.Getpagesize()),
			}
			clientPipe := utils.ReadWriterPipe{
				PipeName: "client connection",
				RW:       c,
				Buf:      make([]byte, os.Getpagesize()),
			}
			proxyLog := th.log.WithField("component", "eve-ssh-proxy")
			utils.RunPipeProxy(th.ctx, proxyLog, "EVE SSH", evePipe, clientPipe)
		}(conn)
	}
}

// ConnectConsoleToEVE establishes TCP proxy for a console access into an EVE device.
func (th *TestHarness) ConnectConsoleToEVE(
	ctx context.Context, req *api.EVEDeviceRequest) (*api.TCPProxyResponse, error) {
	th.devicesM.Lock()
	defer th.devicesM.Unlock()
	devName, _, err := th.resolveEVEDeviceNameLocked(req.GetDeviceName())
	if err != nil {
		return nil, err
	}

	// Start listener on docker interface IP and a random unused port.
	ln, err := net.Listen(
		"tcp", net.JoinHostPort(th.dockerIntfIPv4.String(), "0"))
	if err != nil {
		return nil, fmt.Errorf(
			"failed to start EVE device %q console proxy listener on %s: %w",
			devName, th.dockerIntfIPv4.String(), err,
		)
	}

	// Extract the dynamically assigned port.
	addr, ok := ln.Addr().(*net.TCPAddr)
	if !ok {
		_ = ln.Close()
		return nil, fmt.Errorf("unexpected listener address type: %T", ln.Addr())
	}

	// Make sure that the console has only one user.
	if th.devices[devName].consoleInUse {
		ln.Close()
		return nil, fmt.Errorf("EVE device %q console is already being used", devName)
	}
	th.devices[devName].consoleInUse = true

	th.log.Infof("Started EVE device %q console proxy on %s:%d",
		devName, th.dockerIntfIPv4.String(), addr.Port)

	th.wg.Add(1)
	go th.runConsoleProxyForEVE(ln, devName)

	th.tcpProxiesM.Lock()
	th.tcpProxies[addr.Port] = &tcpProxy{
		listener:   ln,
		devName:    devName,
		forConsole: true,
	}
	th.tcpProxiesM.Unlock()

	return &api.TCPProxyResponse{
		ProxyIpAddress: th.dockerIntfIPv4.String(),
		ProxyPort:      uint32(addr.Port),
	}, nil
}

func (th *TestHarness) runConsoleProxyForEVE(listener net.Listener, devName string) {
	defer th.wg.Done()
	for {
		conn, err := listener.Accept()
		if err != nil {
			// Detect listener shutdown
			if errors.Is(err, net.ErrClosed) ||
				strings.Contains(err.Error(), "use of closed network connection") {
				th.log.Debugf(
					"EVE device %q console proxy listener closed, stopping accept loop",
					devName)
				return
			}

			th.log.Errorf("EVE device %q console proxy accept error: %v", devName, err)
			continue
		}
		th.log.Infof("Client connected to EVE device %q console proxy from %s",
			devName, conn.RemoteAddr())

		th.wg.Add(1)
		go func(c net.Conn) {
			defer th.wg.Done()
			defer c.Close()

			// Open gRPC console stream
			stream, err := th.brokerClient.ConnectConsoleToDevice(th.ctx)
			if err != nil {
				th.log.Errorf("ConnectConsoleToDevice failed: %v", err)
				return
			}
			defer stream.CloseSend()

			// Send initial connect request
			connectReq := &api.ConnectConsoleRequest{
				Payload: &api.ConnectConsoleRequest_Connect{
					Connect: &api.DeviceControlRequest{
						ClientId:   th.brokerClientID,
						DeviceName: devName,
					},
				},
			}
			if err := stream.Send(connectReq); err != nil {
				th.log.Errorf("failed to send connect request: %v", err)
				return
			}

			// Receive initial response (console properties)
			resp, err := stream.Recv()
			if err != nil {
				th.log.Errorf("failed to receive console properties: %v", err)
				return
			}
			if props := resp.GetConnectReply(); props != nil {
				th.log.Infof("Connected to EVE device %q console (echoed=%v, telnet=%v)",
					devName, props.Echoed, props.Telnet)
			}

			// Run TCP proxy.
			consolePipe := utils.GrpcClientPipe[
				api.ConnectConsoleRequest, api.ConnectConsoleResponse]{
				MakeRequest: func(data []byte) *api.ConnectConsoleRequest {
					return &api.ConnectConsoleRequest{
						Payload: &api.ConnectConsoleRequest_Data{
							Data: data,
						},
					}
				},
				Stream: stream,
			}
			clientPipe := utils.ReadWriterPipe{
				PipeName: "client connection",
				RW:       c,
				Buf:      make([]byte, os.Getpagesize()),
			}
			proxyLog := th.log.WithField("component", "eve-console-proxy")
			utils.RunPipeProxy(th.ctx, proxyLog, "EVE console", consolePipe, clientPipe)
		}(conn)
	}
}

// GetSDNStatus returns the overall SDN (network emulator) status.
func (th *TestHarness) GetSDNStatus(
	ctx context.Context, req *api.SDNRequest) (*api.SDNStatusResponse, error) {
	if th.sdnClient == nil {
		return nil, errors.New("SDN client is not initialized")
	}
	return th.sdnClient.GetStatus(ctx, req)
}

// GetSDNNetworkModel returns the SDN's abstract model of the network.
func (th *TestHarness) GetSDNNetworkModel(
	ctx context.Context, req *api.SDNRequest) (*api.SDNGetNetworkModelResponse, error) {
	if th.sdnClient == nil {
		return nil, errors.New("SDN client is not initialized")
	}
	return th.sdnClient.GetNetworkModel(ctx, req)
}

// GetSDNConfigGraph returns the SDN configuration visualized as a Graphviz
// dot-formatted graph.
func (th *TestHarness) GetSDNConfigGraph(
	ctx context.Context, req *api.SDNRequest) (*api.SDNConfigGraphResponse, error) {
	if th.sdnClient == nil {
		return nil, errors.New("SDN client is not initialized")
	}
	return th.sdnClient.GetConfigGraph(ctx, req)
}

// StreamSDNLogs streams logs from the SDN VM to the gRPC client.
// This method acts as a simple forwarder: it subscribes to the SDN log stream
// and relays all received log messages to the caller over the gRPC stream.
// If the SDN client is not initialized, an error is returned.
// The stream will terminate when either the SDN closes the stream
// (EOF) or the context is canceled.
func (th *TestHarness) StreamSDNLogs(
	req *api.SDNRequest, stream api.Evetest_StreamSDNLogsServer) error {
	if th.sdnClient == nil {
		return errors.New("SDN client is not initialized")
	}

	// Start SDN log stream.
	sdnStream, err := th.sdnClient.StreamLogs(stream.Context(), req)
	if err != nil {
		return fmt.Errorf("failed to start SDN log stream: %v", err)
	}

	// Forward each SDN log message to the gRPC client.
	for {
		m, recvErr := sdnStream.Recv()
		if recvErr == io.EOF {
			// SDN stream closed cleanly.
			return nil
		}
		if recvErr != nil {
			return fmt.Errorf("error receiving SDN log: %w", recvErr)
		}

		if sendErr := stream.Send(m); sendErr != nil {
			return fmt.Errorf("error sending SDN log over gRPC: %w", sendErr)
		}
	}
}

// ConnectSSHToSDN establishes TCP proxy for a SSH session into the SDN.
func (th *TestHarness) ConnectSSHToSDN(
	ctx context.Context, req *api.SDNRequest) (*api.TCPProxyResponse, error) {
	// Start listener on docker interface IP and a random unused port.
	ln, err := net.Listen(
		"tcp", net.JoinHostPort(th.dockerIntfIPv4.String(), "0"))
	if err != nil {
		return nil, fmt.Errorf(
			"failed to start SDN SSH proxy listener on %s: %w",
			th.dockerIntfIPv4.String(), err,
		)
	}

	// Extract the dynamically assigned port.
	addr, ok := ln.Addr().(*net.TCPAddr)
	if !ok {
		_ = ln.Close()
		return nil, fmt.Errorf("unexpected listener address type: %T", ln.Addr())
	}
	th.log.Infof("Started SDN SSH proxy on %s:%d",
		th.dockerIntfIPv4.String(), addr.Port)

	th.wg.Add(1)
	go th.runSSHProxyForSDN(ln)

	th.tcpProxiesM.Lock()
	th.tcpProxies[addr.Port] = &tcpProxy{listener: ln}
	th.tcpProxiesM.Unlock()

	return &api.TCPProxyResponse{
		ProxyIpAddress: th.dockerIntfIPv4.String(),
		ProxyPort:      uint32(addr.Port),
	}, nil
}

func (th *TestHarness) runSSHProxyForSDN(listener net.Listener) {
	defer th.wg.Done()
	for {
		conn, err := listener.Accept()
		if err != nil {
			// Detect listener shutdown
			if errors.Is(err, net.ErrClosed) ||
				strings.Contains(err.Error(), "use of closed network connection") {
				th.log.Debugf("SDN SSH proxy listener closed, stopping accept loop")
				return
			}

			th.log.Errorf("SDN SSH proxy accept error: %v", err)
			continue
		}
		th.log.Infof("Client connected to SDN SSH proxy from %s", conn.RemoteAddr())

		th.wg.Add(1)
		go func(c net.Conn) {
			defer th.wg.Done()
			defer c.Close()

			// Connect to ssh daemon running inside SDN through the SDN tunnel.
			dialer := net.Dialer{}
			sdnConn, err := dialer.DialContext(
				th.ctx, "tcp", net.JoinHostPort(sdnTunVMIPv4.String(), "22"))
			if err != nil {
				th.log.Errorf("Failed to connect to SDN SSH endpoint %s:22: %v",
					sdnTunVMIPv4, err)
				return
			}
			defer sdnConn.Close()

			// Run TCP proxy.
			sdnPipe := utils.ReadWriterPipe{
				PipeName: "SDN SSH connection",
				RW:       sdnConn,
				Buf:      make([]byte, os.Getpagesize()),
			}
			clientPipe := utils.ReadWriterPipe{
				PipeName: "client connection",
				RW:       c,
				Buf:      make([]byte, os.Getpagesize()),
			}
			proxyLog := th.log.WithField("component", "sdn-ssh-proxy")
			utils.RunPipeProxy(th.ctx, proxyLog, "SDN SSH", sdnPipe, clientPipe)
		}(conn)
	}
}

// CloseTCPProxy closes an established TCP proxy.
// Should be used for EVE SSH, EVE Console and SDN SSH proxy once
// it is no longer needed.
func (th *TestHarness) CloseTCPProxy(ctx context.Context,
	req *api.CloseTCPProxyRequest) (*api.CloseTCPProxyResponse, error) {
	port := int(req.ProxyPort)

	th.tcpProxiesM.Lock()
	proxy, found := th.tcpProxies[port]
	if found {
		delete(th.tcpProxies, port)
	}
	th.tcpProxiesM.Unlock()

	if !found {
		return nil, fmt.Errorf("TCP proxy on port %d not found", port)
	}
	if err := proxy.listener.Close(); err != nil {
		return nil, fmt.Errorf("failed to close TCP proxy on port %d: %w", port, err)
	}
	if proxy.forConsole && proxy.devName != "" {
		th.devicesM.Lock()
		th.devices[proxy.devName].consoleInUse = false
		th.devicesM.Unlock()
	}

	th.log.Infof("Closed TCP proxy on %s:%d", req.ProxyIpAddress, port)
	return &api.CloseTCPProxyResponse{}, nil
}
