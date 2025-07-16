package evetest

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	eveinfo "github.com/lf-edge/eve-api/go/info"
	"github.com/lf-edge/eve/evetest/constants"
	api "github.com/lf-edge/eve/evetest/grpcapi/go"
	"github.com/lf-edge/eve/evetest/logger"
	"github.com/lf-edge/eve/evetest/utils"
	uuid "github.com/satori/go.uuid"
	"github.com/spf13/viper"
	"google.golang.org/grpc"
)

type infoMsgGrpcWriter[T any] struct {
	stream grpc.ServerStreamingServer[T]
	mapper func(*eveinfo.ZInfoMsg) (*T, error)
}

func (w *infoMsgGrpcWriter[T]) Write(msg *eveinfo.ZInfoMsg) error {
	resp, err := w.mapper(msg)
	if err != nil {
		return err
	}
	return w.stream.Send(resp)
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
	var eveDevices []*api.EVEDevice
	th.devicesM.Lock()
	for _, dev := range th.devices {
		eveDevices = append(eveDevices, dev.spec)
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
		if msg.GetZtype() != eveinfo.ZInfoTypes_ZiDevice {
			return false
		}
		_, ok := msg.InfoContent.(*eveinfo.ZInfoMsg_Dinfo)
		return ok
	}
	writer := &infoMsgGrpcWriter[api.EVEInfoResponse]{
		stream: stream,
		mapper: func(msg *eveinfo.ZInfoMsg) (*api.EVEInfoResponse, error) {
			return &api.EVEInfoResponse{
				DeviceInfo: msg.InfoContent.(*eveinfo.ZInfoMsg_Dinfo).Dinfo,
			}, nil
		},
	}
	return th.adamClient.WriteDeviceInfoMsgs(stream.Context(), devUUID, matcher,
		writer, req.GetFollow())
}

// GetEVEMetrics streams real-time metrics from EVE device (deviceMetric).
func (th *TestHarness) GetEVEMetrics(
	req *api.EVEDeviceStreamableRequest, stream api.Evetest_GetEVEMetricsServer) error {
	// TODO
	return errors.New("not implemented")
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
	logWriter := &logger.GrpcDeviceLogStreamer{Stream: stream}
	return th.adamClient.WriteDeviceLogs(
		stream.Context(), devUUID, nil, logWriter, req.GetFollow())
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
		appInfo, ok := msg.InfoContent.(*eveinfo.ZInfoMsg_Ainfo)
		if !ok {
			return false
		}
		return appInfo.Ainfo.GetAppID() == req.GetAppNameOrUuid() ||
			appInfo.Ainfo.GetAppName() == req.GetAppNameOrUuid()
	}
	writer := &infoMsgGrpcWriter[api.AppInfoResponse]{
		stream: stream,
		mapper: func(msg *eveinfo.ZInfoMsg) (*api.AppInfoResponse, error) {
			return &api.AppInfoResponse{
				AppInfo: msg.InfoContent.(*eveinfo.ZInfoMsg_Ainfo).Ainfo,
			}, nil
		},
	}
	return th.adamClient.WriteDeviceInfoMsgs(stream.Context(), devUUID, matcher,
		writer, req.GetFollow())
}

// GetAppMetrics streams application-level metrics from a device (appMetric).
func (th *TestHarness) GetAppMetrics(
	req *api.AppRequest, stream api.Evetest_GetAppMetricsServer) error {
	// TODO
	return errors.New("not implemented")
}

// GetAppLogs streams logs from an EVE-managed application.
func (th *TestHarness) GetAppLogs(
	req *api.AppRequest, stream api.Evetest_GetAppLogsServer) error {
	// TODO
	return errors.New("not implemented")
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
		niInfo, ok := msg.InfoContent.(*eveinfo.ZInfoMsg_Niinfo)
		if !ok {
			return false
		}
		return niInfo.Niinfo.GetNetworkID() == req.GetNiNameOrUuid() ||
			niInfo.Niinfo.GetDisplayname() == req.GetNiNameOrUuid()
	}
	writer := &infoMsgGrpcWriter[api.NIInfoResponse]{
		stream: stream,
		mapper: func(msg *eveinfo.ZInfoMsg) (*api.NIInfoResponse, error) {
			return &api.NIInfoResponse{
				NiInfo: msg.InfoContent.(*eveinfo.ZInfoMsg_Niinfo).Niinfo,
			}, nil
		},
	}
	return th.adamClient.WriteDeviceInfoMsgs(stream.Context(), devUUID, matcher,
		writer, req.GetFollow())
}

// GetNIMetrics streams metrics (ZMetricNetworkInstance) for a network instance.
func (th *TestHarness) GetNIMetrics(
	req *api.NIRequest, stream api.Evetest_GetNIMetricsServer) error {
	// TODO
	return errors.New("not implemented")
}

// GetClusterInfo streams summary information for the entire Kubernetes cluster.
func (th *TestHarness) GetClusterInfo(
	req *api.ClusterRequest, stream api.Evetest_GetClusterInfoServer) error {
	// TODO
	return errors.New("not implemented")
}

// GetClusterMetrics streams metrics for the Kubernetes cluster (KubeClusterMetrics).
func (th *TestHarness) GetClusterMetrics(
	req *api.ClusterRequest, stream api.Evetest_GetClusterMetricsServer) error {
	// TODO
	return errors.New("not implemented")
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

			eveIPs, err := th.collectEVEIPs(devName)
			if err != nil {
				return
			}

			// Try connecting to EVE SSH on all discovered IPs.
			var eveConn net.Conn
			for _, eveIP := range eveIPs {
				dialer := net.Dialer{}
				eveConn, err = dialer.DialContext(
					th.ctx, "tcp", net.JoinHostPort(eveIP, "22"))
				if err != nil {
					th.log.Errorf(
						"Failed to connect to EVE device %q SSH endpoint %s:22: %v",
						devName, eveIP, err)
					continue
				}
				th.log.Debugf("Connected to EVE device %q SSH endpoint %s:22",
					devName, eveIP)
				break
			}
			if eveConn == nil {
				th.log.Errorf("Unable to establish SSH connection to EVE device %q "+
					"using detected IPs", devName)
				return
			}
			defer eveConn.Close()

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

// resolveEVEDeviceName validates and resolves the target EVE device name.
// If devName is empty and exactly one device is onboarded, that device is selected.
// If multiple devices are onboarded, devName must be provided and must match
// one of the onboarded devices.
func (th *TestHarness) resolveEVEDeviceName(devName string) (string, uuid.UUID, error) {
	th.devicesM.Lock()
	defer th.devicesM.Unlock()
	return th.resolveEVEDeviceNameLocked(devName)
}

// resolveEVEDeviceNameLocked resolves the target EVE device name assuming
// that th.devicesM is already held by the caller.
//
// It returns the resolved device name and its UUID.
// The caller MUST hold th.devicesM before invoking this method.
func (th *TestHarness) resolveEVEDeviceNameLocked(
	devName string) (string, uuid.UUID, error) {
	if devName != "" {
		if dev, found := th.devices[devName]; found {
			return devName, dev.ID, nil
		}
		return "", uuid.Nil, fmt.Errorf("unknown EVE device %q", devName)
	}

	if len(th.devices) > 1 {
		return "", uuid.Nil, fmt.Errorf(
			"multiple EVE devices are onboarded; device name must be specified",
		)
	}

	for name, dev := range th.devices {
		return name, dev.ID, nil
	}

	// No devices at all
	return "", uuid.Nil, fmt.Errorf("no EVE devices are currently onboarded")
}

// collectEVEIPs discovers IP addresses of the given EVE device by querying SDN
// using the MAC addresses of its network ports.
// Returns all successfully discovered IPs or an error if none were found.
func (th *TestHarness) collectEVEIPs(devName string) (eveIPs []string, err error) {
	th.netModelM.Lock()
	defer th.netModelM.Unlock()

	for _, port := range th.netModel.Ports {
		if port.EveDeviceName != devName {
			continue
		}

		macAddr := port.EveMacAddress
		cmd := exec.CommandContext(
			th.ctx,
			"ssh",
			"-i", "/root/.ssh/sdn_rsa",
			"-o", "StrictHostKeyChecking=no",
			"-o", "UserKnownHostsFile=/dev/null",
			"-o", "IdentitiesOnly=yes",
			"root@"+sdnTunVMIPv4.String(),
			"/bin/get-eve-ip.sh", macAddr,
		)

		out, err := cmd.Output()
		if err != nil {
			th.log.Warnf("Failed to detect EVE IP for device %q (port %q): %v",
				devName, port.LogicalLabel, err)
			continue
		}

		// get-eve-ip.sh can return multiple IPs, separated by a newline
		ips := strings.Split(string(out), "\n")

		foundIP := false
		for _, ipStr := range ips {
			ipStr = strings.TrimSpace(ipStr)
			if ipStr == "" {
				continue
			}

			ip := net.ParseIP(ipStr)
			if ip == nil {
				th.log.Warnf(
					"Ignoring invalid EVE IP %q for device %q (port %q, MAC %s)",
					ipStr, devName, port.LogicalLabel, macAddr,
				)
				continue
			}

			// Filter non-routable addresses
			if ip.IsLinkLocalUnicast() || ip.IsLoopback() || ip.IsUnspecified() {
				continue
			}

			foundIP = true
			th.log.Debugf(
				"Detected EVE IP %s for device %q (port %q, MAC %s)",
				ip, devName, port.LogicalLabel, macAddr,
			)
			eveIPs = append(eveIPs, ipStr)
		}

		if !foundIP {
			th.log.Warnf(
				"No EVE IP returned for device %q (port %q, MAC %s)",
				devName, port.LogicalLabel, macAddr,
			)
		}
	}

	if len(eveIPs) == 0 {
		err = fmt.Errorf("failed to detect any IP address for EVE device %q",
			devName)
		th.log.Error(err)
		return nil, err
	}
	return eveIPs, nil
}
