package evetest

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net"
	"os"
	"regexp"
	"strings"
	"time"

	eveconfig "github.com/lf-edge/eve-api/go/config"
	eveflowlog "github.com/lf-edge/eve-api/go/flowlog"
	eveinfo "github.com/lf-edge/eve-api/go/info"
	evelogs "github.com/lf-edge/eve-api/go/logs"
	evemetrics "github.com/lf-edge/eve-api/go/metrics"
	api "github.com/lf-edge/eve/evetest/grpcapi/go"
	"github.com/lf-edge/eve/evetest/logger"
	"github.com/lf-edge/eve/pkg/pillar/utils/generics"
	uuid "github.com/satori/go.uuid"
	"google.golang.org/protobuf/types/known/timestamppb"
)

// EdgeDevice represents a single onboarded EVE device and provides
// operations to manage its lifecycle, configuration, applications,
// and runtime state.
type EdgeDevice struct {
	th      *TestHarness
	devName string
}

const (
	// Timeout for SSH commands executed on the EVE device that are expected
	// to finish quickly.
	quickSSHCommandTimeout = 5 * time.Second

	// Timeout for file transfers from the EVE device initiated by tests
	// (see EdgeDevice.ReadFile).
	// These transfers are expected to involve reasonably sized files, not
	// extremely large datasets, but may still take longer than quick SSH
	// commands due to network latency or device load.
	fileTransferTimeout = time.Minute

	// If download progress does not advance for this long, WaitUntilAppIsRunning fails.
	downloadStalledTimeout = time.Minute
)

// LogMsg represents a single log message emitted by the device or an application.
type LogMsg struct {
	Severity  string
	Source    string
	Filename  string
	Message   string
	Timestamp time.Time
}

// LogMsgMatch defines filtering criteria for matching log messages.
type LogMsgMatch struct {
	Severity         string
	Source           string
	Filename         string
	MsgHasSubstring  string
	MsgMatchesRegexp regexp.Regexp
	NotBefore        time.Time
	NotAfter         time.Time
}

// FlowLogMatch defines filtering criteria for matching application flow logs.
type FlowLogMatch struct {
	Flow              eveflowlog.IpFlow // match every non-zero value from the 5-tuple
	Inbound           bool
	VirtualNetAdapter string // logical label
	NetworkInstance   uuid.UUID
	// NotBefore and NotAfter relates to FlowRecord.startTime
	NotBefore time.Time
	NotAfter  time.Time
}

// DNSLogMatch defines filtering criteria for matching application DNS logs.
type DNSLogMatch struct {
	VirtualNetAdapter string // logical label
	NetworkInstance   uuid.UUID
	// NotBefore and NotAfter relates to DnsRequest.requestTime
	NotBefore time.Time
	NotAfter  time.Time
}

// AuthMethod is a marker interface for application authentication methods.
type AuthMethod interface {
	isAuthMethod()
}

// UsernamePasswordAuth represents username/password authentication.
type UsernamePasswordAuth struct {
	Username string
	Password string
}

func (UsernamePasswordAuth) isAuthMethod() {}

// ClientCertAuth represents client certificate–based authentication.
type ClientCertAuth struct {
	KeyPEM string
}

func (ClientCertAuth) isAuthMethod() {}

// GetState returns the current lifecycle state of the device.
func (d *EdgeDevice) GetState() api.EVEDeviceState {
	d.th.devicesM.Lock()
	defer d.th.devicesM.Unlock()
	devState, found := d.th.devices[d.devName]
	if !found {
		return api.EVEDeviceState_EVE_DEVICE_STATE_UNDEFINED
	}
	return devState.state
}

// ApplyConfig applies a device configuration and optionally waits until
// it is fully applied.
func (d *EdgeDevice) ApplyConfig(config *EdgeDeviceConfig, waitUntilApplied bool) {
	if d.devName != config.DeviceName {
		d.th.t.Fatalf("Device name mismatch: "+
			"EdgeDevice handle is for %q but config is for %q",
			d.devName, config.DeviceName)
	}

	// Get previous config.
	d.th.devicesM.Lock()
	devState, found := d.th.devices[d.devName]
	if !found {
		d.th.t.Fatalf("Unknown device %q", d.devName)
	}
	devUUID := devState.ID
	prevConfig := devState.config
	d.th.devicesM.Unlock()

	// Set config ID.
	configVer := d.th.nextConfigVersion(prevConfig)
	newConfig := config.clone()
	newConfig.Id = &eveconfig.UUIDandVersion{
		Uuid:    devUUID.String(),
		Version: configVer,
	}

	// Set timestamp.
	newConfig.ConfigTimestamp = timestamppb.New(time.Now())

	// Set default global configuration properties.
	newConfig.setDefaultConfigProperties()

	// Preserve device reboot counter and per-app restart/purge counters from
	// the previous config when the new config does not set them explicitly.
	// This prevents a subsequent RequestReboot or Reboot/PurgeApplication
	// call from re-issuing a command the device has already processed.
	if prevConfig != nil {
		if newConfig.Reboot == nil {
			newConfig.Reboot = prevConfig.GetReboot()
		}
		prevApps := make(map[string]*eveconfig.AppInstanceConfig,
			len(prevConfig.GetApps()))
		for _, app := range prevConfig.GetApps() {
			prevApps[app.GetUuidandversion().GetUuid()] = app
		}
		for _, app := range newConfig.GetApps() {
			prev, ok := prevApps[app.GetUuidandversion().GetUuid()]
			if !ok {
				continue
			}
			if app.Restart == nil {
				app.Restart = prev.GetRestart()
			}
			if app.Purge == nil {
				app.Purge = prev.GetPurge()
			}
		}
	}

	// Preserve cipher contexts.
	if prevConfig != nil {
		newConfig.CipherContexts = prevConfig.GetCipherContexts()
	}

	ctx, cancel := context.WithTimeout(d.th.ctx, adamApplyConfigTimeout)
	err := d.th.adamClient.ApplyDeviceConfig(ctx, devUUID, newConfig.EdgeDevConfig)
	cancel()
	if err != nil {
		d.th.t.Fatalf("Failed to apply the new configuration "+
			"(version %s) for device %q: %v", configVer, d.devName, err)
	}

	// Save the applied config.
	d.th.devicesM.Lock()
	d.th.devices[d.devName].config = newConfig
	d.th.devicesM.Unlock()

	if waitUntilApplied {
		// TODO: Consider waiting for an info message where LastProcessedConfig >=
		//       newConfig.ConfigTimestamp instead of using the current approach.
		//       However, LastProcessedConfig only indicates that zedagent parsed
		//       the config and published pubsub messages to other microservices,
		//       so it does not guarantee that the config has actually been applied.
		d.th.log.Infof(
			"Waiting for device %q to apply the latest config (version %s)...",
			d.devName, configVer)
		ctx, cancel = context.WithTimeout(d.th.ctx, deviceApplyConfigTimeout)
		err = d.th.adamClient.WaitUntilDevRequest(ctx, devUUID, "/config")
		cancel()
		if err != nil {
			d.th.t.Fatalf(
				"Device %q failed to fetch the latest config (version %s): %v",
				d.devName, configVer, err)
		}
		d.th.log.Infof("Device %q applied the latest config (version %s)",
			d.devName, configVer)
	}
}

// GetConfig returns the current device configuration.
func (d *EdgeDevice) GetConfig() *EdgeDeviceConfig {
	return d.getConfig(true)
}

func (d *EdgeDevice) getConfig(clone bool) *EdgeDeviceConfig {
	d.th.devicesM.Lock()
	defer d.th.devicesM.Unlock()
	devState, found := d.th.devices[d.devName]
	if !found {
		d.th.t.Fatalf("Unknown device %q", d.devName)
	}
	if !clone {
		return devState.config
	}
	return devState.config.clone()
}

// GetDeviceIPAddress returns IP addresses assigned to the specified network adapter.
func (d *EdgeDevice) GetDeviceIPAddress(netAdapterLogicalLabel string) []net.IP {
	deviceInfo := d.GetDeviceInfo()
	if deviceInfo == nil {
		return nil
	}
	sysAdapter := deviceInfo.GetSystemAdapter()
	if sysAdapter == nil {
		return nil
	}
	var ips []net.IP
	for _, dps := range sysAdapter.GetStatus() {
		for _, port := range dps.GetPorts() {
			if port.GetName() != netAdapterLogicalLabel {
				continue
			}
			for _, ipStr := range port.GetIPAddrs() {
				// Strip CIDR prefix if present.
				host := ipStr
				if idx := strings.IndexByte(ipStr, '/'); idx != -1 {
					host = ipStr[:idx]
				}
				ip := net.ParseIP(host)
				if ip != nil {
					ips = append(ips, ip)
				}
			}
		}
	}
	return ips
}

// UpgradeEVE upgrades the EVE OS to the specified version and optionally
// waits until the upgrade completes.
func (d *EdgeDevice) UpgradeEVE(eveVersion string, waitUntilUpgraded bool) {
	// TODO (do not forget to log progress)
	d.th.t.Fatalf("UpgradeEVE is not implemented")
}

// RequestReboot requests a device reboot via configuration and optionally
// waits until the reboot completes.
func (d *EdgeDevice) RequestReboot(waitUntilRebooted bool) {
	config := d.getConfig(true)
	reboot := config.GetReboot()
	if reboot == nil {
		config.Reboot = &eveconfig.DeviceOpsCmd{
			Counter: 1, DesiredState: true}
	} else {
		config.Reboot = &eveconfig.DeviceOpsCmd{
			Counter: reboot.GetCounter() + 1, DesiredState: true}
	}
	d.rebootAndWait(waitUntilRebooted, func() {
		d.ApplyConfig(config, false)
	})
}

// SoftReboot reboots the device from the console/SSH.
func (d *EdgeDevice) SoftReboot(waitUntilRebooted bool) {
	d.rebootAndWait(waitUntilRebooted, func() {
		ctx, cancel := context.WithTimeout(d.th.ctx, quickSSHCommandTimeout)
		err := d.th.runScriptOnEVEOverSSH(ctx, d.devName, "reboot", nil, nil, 0)
		cancel()
		if err != nil {
			d.th.t.Fatalf("SoftReboot: failed to run reboot over SSH: %v", err)
		}
	})
}

// HardReboot triggers device reboot through the broker.
func (d *EdgeDevice) HardReboot(waitUntilRebooted bool) {
	d.rebootAndWait(waitUntilRebooted, func() {
		devCtrlReq := &api.DeviceControlRequest{
			ClientId:   d.th.brokerClientID,
			DeviceName: d.devName,
		}
		rebootCtx, rebootCancel := context.WithTimeout(
			d.th.ctx, brokerRebootEVEDeviceTimeout)
		_, err := d.th.brokerClient.RebootDevice(rebootCtx, devCtrlReq)
		rebootCancel()
		if err != nil {
			d.th.t.Fatalf("HardReboot: broker failed to reboot device %q: %v",
				d.devName, err)
		}
	})
}

// rebootAndWait executes triggerFn to initiate a device reboot and, if
// wait is true, blocks until the device confirms the reboot by reporting
// a ZInfoDevice.lastRebootTime strictly after the moment triggerFn was called.
//
// The subscription is established before triggerFn is invoked to avoid
// missing the post-reboot info message. Device and evetest clocks are
// assumed to be in sync.
func (d *EdgeDevice) rebootAndWait(wait bool, triggerFn func()) {
	if !wait {
		triggerFn()
		return
	}

	devUUID := d.getDevUUID()

	// Subscribe before triggering the reboot so we cannot miss the
	// post-reboot info message that arrives after the device comes back.
	infoCh := make(chan *eveinfo.ZInfoMsg, 20)
	unsub, err := d.th.adamClient.SubscribeToDeviceInfoMsgs(devUUID,
		func(msg *eveinfo.ZInfoMsg) bool {
			return msg.GetZtype() == eveinfo.ZInfoTypes_ZiDevice
		},
		infoCh,
	)
	if err != nil {
		d.th.t.Fatalf("Failed to subscribe to info messages for device %q: %v",
			d.devName, err)
	}
	defer unsub()

	// Record local time just before issuing the reboot command.
	// lastRebootTime > rebootIssuedAt confirms the device has completed
	// the reboot triggered by this call.
	// Assumes that evetest and device clocks are in-sync.
	rebootIssuedAt := time.Now()
	triggerFn()
	d.th.log.Infof("Waiting for device %q to reboot (issued at: %s)...",
		d.devName, rebootIssuedAt)

	waitCtx, waitCancel := context.WithTimeout(d.th.ctx, deviceRebootTimeout)
	defer waitCancel()

	for {
		select {
		case msg, ok := <-infoCh:
			if !ok {
				d.th.t.Fatalf("Info subscription closed while waiting "+
					"for device %q to reboot", d.devName)
			}
			ts := msg.GetDinfo().GetLastRebootTime()
			if ts != nil && ts.AsTime().After(rebootIssuedAt) {
				d.th.log.Infof("Device %q has rebooted (last reboot time: %s)",
					d.devName, ts.AsTime())
				return
			}
		case <-waitCtx.Done():
			d.th.t.Fatalf("Timed out waiting for device %q to reboot", d.devName)
		}
	}
}

// GetLogs returns device log messages matching the provided criteria.
func (d *EdgeDevice) GetLogs(match LogMsgMatch) []LogMsg {
	devUUID := d.getDevUUID()
	collector := &logMsgCollector{match: match}
	ctx, cancel := context.WithTimeout(d.th.ctx, gatherLogsTimeout)
	err := d.th.adamClient.IterateDeviceLogs(
		ctx, devUUID, collector.toMatcher(), collector, false)
	cancel()
	if err != nil {
		d.th.t.Fatalf("Failed to retrieve logs for device %q: %v", d.devName, err)
	}
	return collector.msgs
}

// GetAppLogs returns application log messages matching the provided criteria.
func (d *EdgeDevice) GetAppLogs(appUUID uuid.UUID, match LogMsgMatch) []LogMsg {
	devUUID := d.getDevUUID()
	collector := &logMsgCollector{match: match}
	ctx, cancel := context.WithTimeout(d.th.ctx, gatherLogsTimeout)
	err := d.th.adamClient.IterateAppLogs(
		ctx, devUUID, appUUID, collector.toMatcher(), collector, false)
	cancel()
	if err != nil {
		d.th.t.Fatalf("Failed to retrieve app logs for device %q app %q: %v",
			d.devName, appUUID, err)
	}
	return collector.msgs
}

// GetAppFlowLogs returns flow records for the specified application
// matching the provided criteria.
func (d *EdgeDevice) GetAppFlowLogs(
	appUUID uuid.UUID, match FlowLogMatch) []eveflowlog.FlowRecord {
	// TODO: implement AdamClient.IterateAppFlowLogs first
	d.th.t.Fatalf("GetAppFlowLogs is not implemented")
	return nil
}

// GetAppDNSLogs returns DNS request logs for the specified application
// matching the provided criteria.
func (d *EdgeDevice) GetAppDNSLogs(
	appUUID uuid.UUID, match DNSLogMatch) []eveflowlog.DnsRequest {
	// TODO: implement AdamClient.IterateAppFlowLogs first
	d.th.t.Fatalf("GetAppDNSLogs is not implemented")
	return nil
}

// waitUntilAppState waits until the app reaches one of targetStates,
// logging every state transition along the way.
// ctx controls the deadline; callers must derive it from d.th.ctx.
// Calls t.Fatalf on timeout or error.
func (d *EdgeDevice) waitUntilAppState(
	ctx context.Context, appUUID uuid.UUID, targetStates ...eveinfo.ZSwState) {
	devUUID := d.getDevUUID()
	appUUIDStr := appUUID.String()

	var lastState = eveinfo.ZSwState_INVALID

	d.th.log.Infof("Waiting for app %q on device %q to reach state(s) %v",
		appUUID, d.devName, targetStates)
	err := d.th.adamClient.IterateDeviceInfoMsgs(ctx, devUUID,
		func(msg *eveinfo.ZInfoMsg) bool {
			if msg.GetZtype() != eveinfo.ZInfoTypes_ZiApp {
				return false
			}
			ainfo := msg.GetAinfo()
			return ainfo != nil && ainfo.GetAppID() == appUUIDStr
		},
		infoMsgIterFn(func(msg *eveinfo.ZInfoMsg) (bool, error) {
			ainfo := msg.GetAinfo()
			state := ainfo.GetState()
			if state != lastState {
				lastState = state
				d.th.log.Infof("App %q (%s) on device %q state changed to %s",
					appUUID, ainfo.GetAppName(), d.devName, state)
			}
			if generics.ContainsItem(targetStates, state) {
				return true, nil
			}
			return false, nil
		}),
		true,
	)

	if err != nil {
		d.th.t.Fatalf("Waiting for app %q on device %q to reach state(s) %v: %v",
			appUUID, d.devName, targetStates, err)
	}
}

// WaitUntilAppIsRunning waits until the specified application reaches
// the running state or fails.
//
// timeoutExcludingDownload is the maximum time to wait excluding any
// period spent actively downloading (i.e. in DOWNLOAD_STARTED state with
// advancing progress). If a download stalls for downloadStalledTimeout the
// function fails immediately regardless of this timeout.
func (d *EdgeDevice) WaitUntilAppIsRunning(
	appUUID uuid.UUID, timeoutExcludingDownload time.Duration) {
	devUUID := d.getDevUUID()
	appUUIDStr := appUUID.String()
	d.th.log.Infof("Waiting for app %q on device %q to reach RUNNING state...",
		appUUID, d.devName)

	var (
		lastState          = eveinfo.ZSwState_INVALID
		lastDownloadPct    uint32
		nonDownloadStart   = time.Now()
		nonDownloadElapsed time.Duration
		inDownload         bool
		appName            string
		volumeRefs         []string
		// Keyed by volume UUID; accumulates the latest ZInfoVolume for each volume.
		volumes = make(map[string]*eveinfo.ZInfoVolume)
	)

	// ctx is canceled either by the timer below (timeout) or by d.th.ctx (test end).
	ctx, cancel := context.WithCancel(d.th.ctx)
	defer cancel()

	// The timer drives timeouts when no info messages arrive:
	//   - non-download phase: fires after the remaining non-download budget
	//   - download phase: fires after downloadStalledTimeout with no progress
	// iterCb resets it on each relevant transition or progress update.
	timer := time.NewTimer(timeoutExcludingDownload)
	defer timer.Stop()

	// Cancel the context when the timer fires so IterateDeviceInfoMsgs unblocks.
	go func() {
		select {
		case <-timer.C:
			cancel()
		case <-ctx.Done():
		}
	}()

	// Accept ZiApp messages for this app and all ZiVolume messages.
	// Volume messages are further filtered in the iterator once the app's
	// VolumeRefs are known.
	filter := func(msg *eveinfo.ZInfoMsg) bool {
		switch msg.GetZtype() {
		case eveinfo.ZInfoTypes_ZiApp:
			ainfo := msg.GetAinfo()
			return ainfo != nil && ainfo.GetAppID() == appUUIDStr
		case eveinfo.ZInfoTypes_ZiVolume:
			return true
		}
		return false
	}

	iterCb := func(msg *eveinfo.ZInfoMsg) (bool, error) {
		// Handle volume updates: store the latest state for each volume
		// and re-evaluate download progress if the app is currently downloading.
		if msg.GetZtype() == eveinfo.ZInfoTypes_ZiVolume {
			vinfo := msg.GetVinfo()
			if vinfo == nil {
				return false, nil
			}
			volumes[vinfo.GetUuid()] = vinfo
			// If the app is in DOWNLOAD_STARTED state, a volume update may
			// change the reported progress -- check and log.
			if inDownload {
				pct := appDownloadProgress(volumeRefs, volumes)
				if pct != lastDownloadPct {
					lastDownloadPct = pct
					timer.Reset(downloadStalledTimeout)
					d.th.log.Infof("App %q (%s) on device %q state changed to %s (%d%%)",
						appUUID, appName, d.devName, lastState, pct)
				}
			}
			return false, nil
		}

		ainfo := msg.GetAinfo()
		state := ainfo.GetState()
		appName = ainfo.GetAppName()

		// Update volume refs from the latest app info.
		volumeRefs = ainfo.GetVolumeRefs()

		// Maintain non-download elapsed time and update the timer when
		// transitioning between download and non-download phases.
		nowInDownload := state == eveinfo.ZSwState_DOWNLOAD_STARTED
		if inDownload && !nowInDownload {
			// Leaving download: resume non-download clock and set timer to
			// the remaining non-download budget.
			nonDownloadStart = time.Now()
			remaining := timeoutExcludingDownload - nonDownloadElapsed
			if remaining <= 0 {
				return true, fmt.Errorf(
					"timed out after %s (excluding download) waiting for app %q (%s) "+
						"on device %q to reach RUNNING state (last state: %s)",
					timeoutExcludingDownload, appUUID, appName, d.devName, state)
			}
			timer.Reset(remaining)
		} else if !inDownload && nowInDownload {
			// Entering download: freeze non-download clock and arm stall timer.
			nonDownloadElapsed += time.Since(nonDownloadStart)
			timer.Reset(downloadStalledTimeout)
		}
		inDownload = nowInDownload

		// Log every state change and every download-progress change.
		if state != lastState {
			lastState = state
			if state == eveinfo.ZSwState_DOWNLOAD_STARTED {
				pct := appDownloadProgress(volumeRefs, volumes)
				lastDownloadPct = pct
				d.th.log.Infof("App %q (%s) on device %q state changed to %s (%d%%)",
					appUUID, appName, d.devName, state, pct)
			} else {
				d.th.log.Infof("App %q (%s) on device %q state changed to %s",
					appUUID, appName, d.devName, state)
			}
		} else if state == eveinfo.ZSwState_DOWNLOAD_STARTED {
			pct := appDownloadProgress(volumeRefs, volumes)
			if pct != lastDownloadPct {
				lastDownloadPct = pct
				timer.Reset(downloadStalledTimeout)
				d.th.log.Infof("App %q (%s) on device %q state changed to %s (%d%%)",
					appUUID, appName, d.devName, state, pct)
			}
		}

		// Fail immediately on unrecoverable error.
		if state == eveinfo.ZSwState_ERROR {
			var errDescs []string
			for _, e := range ainfo.GetAppErr() {
				if desc := e.GetDescription(); desc != "" {
					errDescs = append(errDescs, desc)
				}
			}
			if len(errDescs) > 0 {
				return true, fmt.Errorf(
					"app %q (%s) on device %q entered ERROR state: %s",
					appUUID, appName, d.devName, strings.Join(errDescs, "; "))
			}
			return true, fmt.Errorf(
				"app %q (%s) on device %q entered ERROR state",
				appUUID, appName, d.devName)
		}

		// Success.
		if state == eveinfo.ZSwState_RUNNING {
			d.th.log.Infof("App %q (%s) on device %q is RUNNING",
				appUUID, appName, d.devName)
			return true, nil
		}

		return false, nil
	}

	err := d.th.adamClient.IterateDeviceInfoMsgs(ctx, devUUID, filter,
		infoMsgIterFn(iterCb), true)

	if err != nil {
		// If the test framework context was canceled, propagate the error.
		if d.th.ctx.Err() != nil {
			d.th.t.Fatalf("%v", err)
		}

		// If our context was not canceled, the error came from iterCb
		// (e.g. ZSwState_ERROR or explicit failure).
		if ctx.Err() == nil {
			d.th.t.Fatalf("%v", err)
		}

		// Otherwise our timer fired — determine which timeout occurred.
		if inDownload {
			d.th.t.Fatalf(
				"app %q (%s) on device %q download stalled at %d%% for more than %s",
				appUUID, appName, d.devName, lastDownloadPct, downloadStalledTimeout)
		}

		nonDownloadTotal := nonDownloadElapsed + time.Since(nonDownloadStart)
		d.th.t.Fatalf(
			"timed out after %s (excluding download) waiting for app %q (%s) "+
				"on device %q to reach RUNNING state (last state: %s)",
			nonDownloadTotal, appUUID, appName, d.devName, lastState)
	}
}

// RebootApplication requests a reboot of the specified application instance.
func (d *EdgeDevice) RebootApplication(appUUID uuid.UUID, waitUntilRebooted bool,
	timeout time.Duration) {
	config := d.getConfig(true)
	appUUIDStr := appUUID.String()

	// Locate the application in the config and increment the restart counter.
	found := false
	for _, app := range config.GetApps() {
		if app.GetUuidandversion().GetUuid() == appUUIDStr {
			restart := app.GetRestart()
			if restart == nil {
				app.Restart = &eveconfig.InstanceOpsCmd{Counter: 1}
			} else {
				app.Restart = &eveconfig.InstanceOpsCmd{Counter: restart.GetCounter() + 1}
			}
			found = true
			break
		}
	}
	if !found {
		d.th.t.Fatalf("App %q not found in device %q config", appUUID, d.devName)
	}
	d.ApplyConfig(config, false)
	if waitUntilRebooted {
		ctx, cancel := context.WithTimeout(d.th.ctx, timeout)
		defer cancel()
		d.waitUntilAppState(ctx, appUUID,
			eveinfo.ZSwState_RESTARTING, eveinfo.ZSwState_HALTING)
		d.waitUntilAppState(ctx, appUUID, eveinfo.ZSwState_RUNNING)
	}
}

// PurgeApplication purges the specified application instance and its state.
func (d *EdgeDevice) PurgeApplication(appUUID uuid.UUID, waitUntilPurged bool,
	timeout time.Duration) {
	config := d.getConfig(true)
	appUUIDStr := appUUID.String()

	// Locate the application in the config and increment the purge counter.
	found := false
	for _, app := range config.GetApps() {
		if app.GetUuidandversion().GetUuid() == appUUIDStr {
			purge := app.GetPurge()
			if purge == nil {
				app.Purge = &eveconfig.InstanceOpsCmd{Counter: 1}
			} else {
				app.Purge = &eveconfig.InstanceOpsCmd{Counter: purge.GetCounter() + 1}
			}
			found = true
			break
		}
	}
	if !found {
		d.th.t.Fatalf("App %q not found in device %q config", appUUID, d.devName)
	}
	d.ApplyConfig(config, false)
	if waitUntilPurged {
		ctx, cancel := context.WithTimeout(d.th.ctx, timeout)
		defer cancel()
		d.waitUntilAppState(ctx, appUUID,
			eveinfo.ZSwState_PURGING, eveinfo.ZSwState_HALTING)
		d.waitUntilAppState(ctx, appUUID, eveinfo.ZSwState_RUNNING)
	}
}

// ActivateApplication activates the specified application instance.
func (d *EdgeDevice) ActivateApplication(appUUID uuid.UUID, waitUntilActivated bool,
	timeout time.Duration) {
	config := d.getConfig(true)
	appUUIDStr := appUUID.String()

	// Locate the application in the config and mark it as activated.
	found := false
	for _, app := range config.GetApps() {
		if app.GetUuidandversion().GetUuid() == appUUIDStr {
			app.Activate = true
			found = true
			break
		}
	}
	if !found {
		d.th.t.Fatalf("App %q not found in device %q config", appUUID, d.devName)
	}

	d.ApplyConfig(config, false)
	if waitUntilActivated {
		d.WaitUntilAppIsRunning(appUUID, timeout)
	}
}

// DeactivateApplication deactivates the specified application instance.
func (d *EdgeDevice) DeactivateApplication(appUUID uuid.UUID, waitUntilDeactivated bool,
	timeout time.Duration) {
	config := d.getConfig(true)
	appUUIDStr := appUUID.String()

	// Locate the application in the config and mark it as deactivated.
	found := false
	for _, app := range config.GetApps() {
		if app.GetUuidandversion().GetUuid() == appUUIDStr {
			app.Activate = false
			found = true
			break
		}
	}
	if !found {
		d.th.t.Fatalf("App %q not found in device %q config", appUUID, d.devName)
	}

	d.ApplyConfig(config, false)
	if waitUntilDeactivated {
		ctx, cancel := context.WithTimeout(d.th.ctx, timeout)
		defer cancel()
		d.waitUntilAppState(ctx, appUUID, eveinfo.ZSwState_HALTED)
	}
}

// RunShellScript executes the provided shell script on the device over SSH
// and returns its standard output and standard error as strings.
//
// If timeout is non-zero, execution is bounded by the given duration and
// will be canceled if the timeout expires. If timeout is zero, no explicit
// deadline is applied.
//
// If stdoutWatchdogTimeout is non-zero, the script will be terminated if
// it produces no output on stdout for longer than the specified duration.
// This acts as a "watchdog" to detect stalled scripts.
func (d *EdgeDevice) RunShellScript(script string, timeout time.Duration,
	stdoutWatchdogTimeout time.Duration) (stdout, stderr string, err error) {
	ctx := d.th.ctx
	if timeout != 0 {
		var cancel context.CancelFunc
		ctx, cancel = context.WithTimeout(d.th.ctx, timeout)
		defer cancel()
	}
	var stdoutBuf, stderrBuf bytes.Buffer
	err = d.th.runScriptOnEVEOverSSH(
		ctx, d.devName, script, &stdoutBuf, &stderrBuf, stdoutWatchdogTimeout)
	return stdoutBuf.String(), stderrBuf.String(), err
}

// FileExists checks whether a file exists on the device.
func (d *EdgeDevice) FileExists(fileName string) bool {
	stdout, _, err := d.RunShellScript(
		"test -f "+shellEscape(fileName)+" && echo EXISTS",
		quickSSHCommandTimeout, 0)
	if err != nil {
		d.th.t.Fatalf("FileExists: SSH command failed: %v", err)
	}
	return strings.Contains(stdout, "EXISTS")
}

// ReadFile reads the contents of a file from the device.
func (d *EdgeDevice) ReadFile(fileName string) []byte {
	ctx, cancel := context.WithTimeout(d.th.ctx, fileTransferTimeout)
	defer cancel()

	tmpFile, err := os.CreateTemp("", "eve-file-*")
	if err != nil {
		d.th.t.Fatalf("ReadFile: failed to create temp file: %v", err)
	}
	tmpPath := tmpFile.Name()
	tmpFile.Close()
	defer os.Remove(tmpPath)

	err = d.th.scpFromEVE(ctx, d.devName, fileName, tmpPath)
	if err != nil {
		d.th.t.Fatalf("ReadFile: failed to copy %q from device %q: %v",
			fileName, d.devName, err)
	}

	data, err := os.ReadFile(tmpPath)
	if err != nil {
		d.th.t.Fatalf("ReadFile: failed to read temp file: %v", err)
	}
	return data
}

// DeleteFile removes a file from the device.
func (d *EdgeDevice) DeleteFile(fileName string) {
	_, _, err := d.RunShellScript(
		"rm -f "+shellEscape(fileName), quickSSHCommandTimeout, 0)
	if err != nil {
		d.th.t.Fatalf("DeleteFile: SSH command failed: %v", err)
	}
}

// GetDeviceInfo returns the last recorded device information,
// or nil if no info message has been received yet.
func (d *EdgeDevice) GetDeviceInfo() *eveinfo.ZInfoDevice {
	devUUID := d.getDevUUID()
	var result *eveinfo.ZInfoDevice
	d.iterateInfoMsgs(devUUID,
		func(msg *eveinfo.ZInfoMsg) bool {
			return msg.GetZtype() == eveinfo.ZInfoTypes_ZiDevice
		},
		func(msg *eveinfo.ZInfoMsg) {
			result = msg.GetDinfo()
		},
	)
	return result
}

// WatchDeviceInfo subscribes to device info updates and returns a buffered
// channel that receives each new ZInfoDevice as it arrives.
// Call the returned close function to stop watching and close the channel.
func (d *EdgeDevice) WatchDeviceInfo() (updates <-chan *eveinfo.ZInfoDevice, stop func()) {
	devUUID := d.getDevUUID()
	rawCh := make(chan *eveinfo.ZInfoMsg, 10)
	unsub, err := d.th.adamClient.SubscribeToDeviceInfoMsgs(devUUID,
		func(msg *eveinfo.ZInfoMsg) bool {
			return msg.GetZtype() == eveinfo.ZInfoTypes_ZiDevice
		},
		rawCh,
	)
	if err != nil {
		d.th.t.Fatalf("WatchDeviceInfo: failed to subscribe to info messages "+
			"for device %q: %v", d.devName, err)
	}
	ch := make(chan *eveinfo.ZInfoDevice, 10)
	go func() {
		defer close(ch)
		for msg := range rawCh {
			ch <- msg.GetDinfo()
		}
	}()
	return ch, unsub
}

// GetAppInfo returns the last recorded runtime information for the specified
// application, or nil if no info message for that app has been received yet.
func (d *EdgeDevice) GetAppInfo(appUUID uuid.UUID) *eveinfo.ZInfoApp {
	devUUID := d.getDevUUID()
	appUUIDStr := appUUID.String()
	var result *eveinfo.ZInfoApp
	d.iterateInfoMsgs(devUUID,
		func(msg *eveinfo.ZInfoMsg) bool {
			if msg.GetZtype() != eveinfo.ZInfoTypes_ZiApp {
				return false
			}
			return msg.GetAinfo().GetAppID() == appUUIDStr
		},
		func(msg *eveinfo.ZInfoMsg) {
			result = msg.GetAinfo()
		},
	)
	return result
}

// WatchAppInfo subscribes to info updates for the specified application and
// returns a buffered channel that receives each new ZInfoApp as it arrives.
// Call the returned close function to stop watching and close the channel.
func (d *EdgeDevice) WatchAppInfo(
	appUUID uuid.UUID) (updates <-chan *eveinfo.ZInfoApp, stop func()) {
	devUUID := d.getDevUUID()
	appUUIDStr := appUUID.String()
	rawCh := make(chan *eveinfo.ZInfoMsg, 10)
	unsub, err := d.th.adamClient.SubscribeToDeviceInfoMsgs(devUUID,
		func(msg *eveinfo.ZInfoMsg) bool {
			return msg.GetZtype() == eveinfo.ZInfoTypes_ZiApp &&
				msg.GetAinfo().GetAppID() == appUUIDStr
		},
		rawCh,
	)
	if err != nil {
		d.th.t.Fatalf("WatchAppInfo: failed to subscribe to info messages "+
			"for device %q: %v", d.devName, err)
	}
	ch := make(chan *eveinfo.ZInfoApp, 10)
	go func() {
		defer close(ch)
		for msg := range rawCh {
			ch <- msg.GetAinfo()
		}
	}()
	return ch, unsub
}

// GetNetworkInstanceInfo returns the last recorded information about the
// specified network instance, or nil if no info message for it has been
// received yet.
func (d *EdgeDevice) GetNetworkInstanceInfo(niUUID uuid.UUID) *eveinfo.ZInfoNetworkInstance {
	devUUID := d.getDevUUID()
	niUUIDStr := niUUID.String()
	var result *eveinfo.ZInfoNetworkInstance
	d.iterateInfoMsgs(devUUID,
		func(msg *eveinfo.ZInfoMsg) bool {
			if msg.GetZtype() != eveinfo.ZInfoTypes_ZiNetworkInstance {
				return false
			}
			return msg.GetNiinfo().GetNetworkID() == niUUIDStr
		},
		func(msg *eveinfo.ZInfoMsg) {
			result = msg.GetNiinfo()
		},
	)
	return result
}

// WatchNetworkInstanceInfo subscribes to info updates for the specified network
// instance and returns a buffered channel that receives each new
// ZInfoNetworkInstance as it arrives.
// Call the returned close function to stop watching and close the channel.
func (d *EdgeDevice) WatchNetworkInstanceInfo(
	niUUID uuid.UUID) (updates <-chan *eveinfo.ZInfoNetworkInstance, stop func()) {
	devUUID := d.getDevUUID()
	niUUIDStr := niUUID.String()
	rawCh := make(chan *eveinfo.ZInfoMsg, 10)
	unsub, err := d.th.adamClient.SubscribeToDeviceInfoMsgs(devUUID,
		func(msg *eveinfo.ZInfoMsg) bool {
			return msg.GetZtype() == eveinfo.ZInfoTypes_ZiNetworkInstance &&
				msg.GetNiinfo().GetNetworkID() == niUUIDStr
		},
		rawCh,
	)
	if err != nil {
		d.th.t.Fatalf("WatchNetworkInstanceInfo: failed to subscribe to info messages "+
			"for device %q: %v", d.devName, err)
	}
	ch := make(chan *eveinfo.ZInfoNetworkInstance, 10)
	go func() {
		defer close(ch)
		for msg := range rawCh {
			ch <- msg.GetNiinfo()
		}
	}()
	return ch, unsub
}

// GetVolumeInfo returns the last recorded information about the specified
// storage volume, or nil if no info message for it has been received yet.
func (d *EdgeDevice) GetVolumeInfo(volumeUUID uuid.UUID) *eveinfo.ZInfoVolume {
	devUUID := d.getDevUUID()
	volUUIDStr := volumeUUID.String()
	var result *eveinfo.ZInfoVolume
	d.iterateInfoMsgs(devUUID,
		func(msg *eveinfo.ZInfoMsg) bool {
			if msg.GetZtype() != eveinfo.ZInfoTypes_ZiVolume {
				return false
			}
			return msg.GetVinfo().GetUuid() == volUUIDStr
		},
		func(msg *eveinfo.ZInfoMsg) {
			result = msg.GetVinfo()
		},
	)
	return result
}

// WatchVolumeInfo subscribes to info updates for the specified storage volume
// and returns a buffered channel that receives each new ZInfoVolume as it arrives.
// Call the returned close function to stop watching and close the channel.
func (d *EdgeDevice) WatchVolumeInfo(volumeUUID uuid.UUID) (
	updates <-chan *eveinfo.ZInfoVolume, stop func()) {

	devUUID := d.getDevUUID()
	volUUIDStr := volumeUUID.String()
	rawCh := make(chan *eveinfo.ZInfoMsg, 10)
	unsub, err := d.th.adamClient.SubscribeToDeviceInfoMsgs(devUUID,
		func(msg *eveinfo.ZInfoMsg) bool {
			return msg.GetZtype() == eveinfo.ZInfoTypes_ZiVolume &&
				msg.GetVinfo().GetUuid() == volUUIDStr
		},
		rawCh,
	)
	if err != nil {
		d.th.t.Fatalf("WatchVolumeInfo: failed to subscribe to info messages "+
			"for device %q: %v", d.devName, err)
	}
	ch := make(chan *eveinfo.ZInfoVolume, 10)
	go func() {
		defer close(ch)
		for msg := range rawCh {
			ch <- msg.GetVinfo()
		}
	}()
	return ch, unsub
}

// GetContentTreeInfo returns the last recorded information about the specified
// content tree, or nil if no info message for it has been received yet.
func (d *EdgeDevice) GetContentTreeInfo(ctUUID uuid.UUID) *eveinfo.ZInfoContentTree {
	devUUID := d.getDevUUID()
	ctUUIDStr := ctUUID.String()
	var result *eveinfo.ZInfoContentTree
	d.iterateInfoMsgs(devUUID,
		func(msg *eveinfo.ZInfoMsg) bool {
			if msg.GetZtype() != eveinfo.ZInfoTypes_ZiContentTree {
				return false
			}
			return msg.GetCinfo().GetUuid() == ctUUIDStr
		},
		func(msg *eveinfo.ZInfoMsg) {
			result = msg.GetCinfo()
		},
	)
	return result
}

// WatchContentTreeInfo subscribes to info updates for the specified content tree
// and returns a buffered channel that receives each new ZInfoContentTree as it arrives.
// Call the returned close function to stop watching and close the channel.
func (d *EdgeDevice) WatchContentTreeInfo(
	ctUUID uuid.UUID) (updates <-chan *eveinfo.ZInfoContentTree, stop func()) {
	devUUID := d.getDevUUID()
	ctUUIDStr := ctUUID.String()
	rawCh := make(chan *eveinfo.ZInfoMsg, 10)
	unsub, err := d.th.adamClient.SubscribeToDeviceInfoMsgs(devUUID,
		func(msg *eveinfo.ZInfoMsg) bool {
			return msg.GetZtype() == eveinfo.ZInfoTypes_ZiContentTree &&
				msg.GetCinfo().GetUuid() == ctUUIDStr
		},
		rawCh,
	)
	if err != nil {
		d.th.t.Fatalf("WatchContentTreeInfo: failed to subscribe to info messages "+
			"for device %q: %v", d.devName, err)
	}
	ch := make(chan *eveinfo.ZInfoContentTree, 10)
	go func() {
		defer close(ch)
		for msg := range rawCh {
			ch <- msg.GetCinfo()
		}
	}()
	return ch, unsub
}

// GetBlobInfo returns the last recorded information about stored blobs on the
// device, or nil if no blob info message has been received yet.
func (d *EdgeDevice) GetBlobInfo() *eveinfo.ZInfoBlobList {
	devUUID := d.getDevUUID()
	var result *eveinfo.ZInfoBlobList
	d.iterateInfoMsgs(devUUID,
		func(msg *eveinfo.ZInfoMsg) bool {
			return msg.GetZtype() == eveinfo.ZInfoTypes_ZiBlobList
		},
		func(msg *eveinfo.ZInfoMsg) {
			result = msg.GetBinfo()
		},
	)
	return result
}

// WatchBlobInfo subscribes to blob info updates and returns a buffered channel
// that receives each new ZInfoBlobList as it arrives.
// Call the returned close function to stop watching and close the channel.
func (d *EdgeDevice) WatchBlobInfo() (updates <-chan *eveinfo.ZInfoBlobList, stop func()) {
	devUUID := d.getDevUUID()
	rawCh := make(chan *eveinfo.ZInfoMsg, 10)
	unsub, err := d.th.adamClient.SubscribeToDeviceInfoMsgs(devUUID,
		func(msg *eveinfo.ZInfoMsg) bool {
			return msg.GetZtype() == eveinfo.ZInfoTypes_ZiBlobList
		},
		rawCh,
	)
	if err != nil {
		d.th.t.Fatalf("WatchBlobInfo: failed to subscribe to info messages "+
			"for device %q: %v", d.devName, err)
	}
	ch := make(chan *eveinfo.ZInfoBlobList, 10)
	go func() {
		defer close(ch)
		for msg := range rawCh {
			ch <- msg.GetBinfo()
		}
	}()
	return ch, unsub
}

// GetAppMetadata returns the last recorded metadata associated with the
// specified application instance, or nil if none has been received yet.
func (d *EdgeDevice) GetAppMetadata(appUUID uuid.UUID) *eveinfo.ZInfoAppInstMetaData {
	devUUID := d.getDevUUID()
	appUUIDStr := appUUID.String()
	var result *eveinfo.ZInfoAppInstMetaData
	d.iterateInfoMsgs(devUUID,
		func(msg *eveinfo.ZInfoMsg) bool {
			if msg.GetZtype() != eveinfo.ZInfoTypes_ZiAppInstMetaData {
				return false
			}
			return msg.GetAmdinfo().GetUuid() == appUUIDStr
		},
		func(msg *eveinfo.ZInfoMsg) {
			result = msg.GetAmdinfo()
		},
	)
	return result
}

// WatchAppMetadata subscribes to metadata updates for the specified application
// instance and returns a buffered channel that receives each new
// ZInfoAppInstMetaData as it arrives.
// Call the returned close function to stop watching and close the channel.
func (d *EdgeDevice) WatchAppMetadata(
	appUUID uuid.UUID) (updates <-chan *eveinfo.ZInfoAppInstMetaData, stop func()) {
	devUUID := d.getDevUUID()
	appUUIDStr := appUUID.String()
	rawCh := make(chan *eveinfo.ZInfoMsg, 10)
	unsub, err := d.th.adamClient.SubscribeToDeviceInfoMsgs(devUUID,
		func(msg *eveinfo.ZInfoMsg) bool {
			return msg.GetZtype() == eveinfo.ZInfoTypes_ZiAppInstMetaData &&
				msg.GetAmdinfo().GetUuid() == appUUIDStr
		},
		rawCh,
	)
	if err != nil {
		d.th.t.Fatalf("WatchAppMetadata: failed to subscribe to info messages "+
			"for device %q: %v", d.devName, err)
	}
	ch := make(chan *eveinfo.ZInfoAppInstMetaData, 10)
	go func() {
		defer close(ch)
		for msg := range rawCh {
			ch <- msg.GetAmdinfo()
		}
	}()
	return ch, unsub
}

// GetHardwareInfo returns the last recorded hardware inventory information,
// or nil if no hardware info message has been received yet.
func (d *EdgeDevice) GetHardwareInfo() *eveinfo.ZInfoHardware {
	devUUID := d.getDevUUID()
	var result *eveinfo.ZInfoHardware
	d.iterateInfoMsgs(devUUID,
		func(msg *eveinfo.ZInfoMsg) bool {
			return msg.GetZtype() == eveinfo.ZInfoTypes_ZiHardware
		},
		func(msg *eveinfo.ZInfoMsg) {
			result = msg.GetHwinfo()
		},
	)
	return result
}

// WatchHardwareInfo subscribes to hardware info updates and returns a buffered
// channel that receives each new ZInfoHardware as it arrives.
// Call the returned close function to stop watching and close the channel.
func (d *EdgeDevice) WatchHardwareInfo() (
	updates <-chan *eveinfo.ZInfoHardware, stop func()) {
	devUUID := d.getDevUUID()
	rawCh := make(chan *eveinfo.ZInfoMsg, 10)
	unsub, err := d.th.adamClient.SubscribeToDeviceInfoMsgs(devUUID,
		func(msg *eveinfo.ZInfoMsg) bool {
			return msg.GetZtype() == eveinfo.ZInfoTypes_ZiHardware
		},
		rawCh,
	)
	if err != nil {
		d.th.t.Fatalf("WatchHardwareInfo: failed to subscribe to info messages "+
			"for device %q: %v", d.devName, err)
	}
	ch := make(chan *eveinfo.ZInfoHardware, 10)
	go func() {
		defer close(ch)
		for msg := range rawCh {
			ch <- msg.GetHwinfo()
		}
	}()
	return ch, unsub
}

// GetLocationInfo returns the last recorded device location information,
// or nil if no location info message has been received yet.
func (d *EdgeDevice) GetLocationInfo() *eveinfo.ZInfoLocation {
	devUUID := d.getDevUUID()
	var result *eveinfo.ZInfoLocation
	d.iterateInfoMsgs(devUUID,
		func(msg *eveinfo.ZInfoMsg) bool {
			return msg.GetZtype() == eveinfo.ZInfoTypes_ZiLocation
		},
		func(msg *eveinfo.ZInfoMsg) {
			result = msg.GetLocinfo()
		},
	)
	return result
}

// WatchLocationInfo subscribes to location info updates and returns a buffered
// channel that receives each new ZInfoLocation as it arrives.
// Call the returned close function to stop watching and close the channel.
func (d *EdgeDevice) WatchLocationInfo() (
	updates <-chan *eveinfo.ZInfoLocation, stop func()) {
	devUUID := d.getDevUUID()
	rawCh := make(chan *eveinfo.ZInfoMsg, 10)
	unsub, err := d.th.adamClient.SubscribeToDeviceInfoMsgs(devUUID,
		func(msg *eveinfo.ZInfoMsg) bool {
			return msg.GetZtype() == eveinfo.ZInfoTypes_ZiLocation
		},
		rawCh,
	)
	if err != nil {
		d.th.t.Fatalf("WatchLocationInfo: failed to subscribe to info messages "+
			"for device %q: %v", d.devName, err)
	}
	ch := make(chan *eveinfo.ZInfoLocation, 10)
	go func() {
		defer close(ch)
		for msg := range rawCh {
			ch <- msg.GetLocinfo()
		}
	}()
	return ch, unsub
}

// GetNTPSources returns the last recorded NTP sources configured on the device,
// or nil if no NTP sources info message has been received yet.
func (d *EdgeDevice) GetNTPSources() *eveinfo.ZInfoNTPSources {
	devUUID := d.getDevUUID()
	var result *eveinfo.ZInfoNTPSources
	d.iterateInfoMsgs(devUUID,
		func(msg *eveinfo.ZInfoMsg) bool {
			return msg.GetZtype() == eveinfo.ZInfoTypes_ZiNTPSources
		},
		func(msg *eveinfo.ZInfoMsg) {
			result = msg.GetNtpSources()
		},
	)
	return result
}

// WatchNTPSources subscribes to NTP sources updates and returns a buffered
// channel that receives each new ZInfoNTPSources as it arrives.
// Call the returned close function to stop watching and close the channel.
func (d *EdgeDevice) WatchNTPSources() (
	updates <-chan *eveinfo.ZInfoNTPSources, stop func()) {
	devUUID := d.getDevUUID()
	rawCh := make(chan *eveinfo.ZInfoMsg, 10)
	unsub, err := d.th.adamClient.SubscribeToDeviceInfoMsgs(devUUID,
		func(msg *eveinfo.ZInfoMsg) bool {
			return msg.GetZtype() == eveinfo.ZInfoTypes_ZiNTPSources
		},
		rawCh,
	)
	if err != nil {
		d.th.t.Fatalf("WatchNTPSources: failed to subscribe to info messages "+
			"for device %q: %v", d.devName, err)
	}
	ch := make(chan *eveinfo.ZInfoNTPSources, 10)
	go func() {
		defer close(ch)
		for msg := range rawCh {
			ch <- msg.GetNtpSources()
		}
	}()
	return ch, unsub
}

// GetClusterNodeInfo returns the last recorded information about the device as
// a cluster node, or nil if no such info message has been received yet.
func (d *EdgeDevice) GetClusterNodeInfo() *eveinfo.ZInfoClusterNode {
	devUUID := d.getDevUUID()
	var result *eveinfo.ZInfoClusterNode
	d.iterateInfoMsgs(devUUID,
		func(msg *eveinfo.ZInfoMsg) bool {
			return msg.GetZtype() == eveinfo.ZInfoTypes_ZiKubeCluster &&
				msg.GetClusterNode() != nil
		},
		func(msg *eveinfo.ZInfoMsg) {
			result = msg.GetClusterNode()
		},
	)
	return result
}

// WatchClusterNodeInfo subscribes to cluster node info updates and returns a
// buffered channel that receives each new ZInfoClusterNode as it arrives.
// Call the returned close function to stop watching and close the channel.
func (d *EdgeDevice) WatchClusterNodeInfo() (
	updates <-chan *eveinfo.ZInfoClusterNode, stop func()) {
	devUUID := d.getDevUUID()
	rawCh := make(chan *eveinfo.ZInfoMsg, 10)
	unsub, err := d.th.adamClient.SubscribeToDeviceInfoMsgs(devUUID,
		func(msg *eveinfo.ZInfoMsg) bool {
			return msg.GetZtype() == eveinfo.ZInfoTypes_ZiKubeCluster &&
				msg.GetClusterNode() != nil
		},
		rawCh,
	)
	if err != nil {
		d.th.t.Fatalf("WatchClusterNodeInfo: failed to subscribe to info messages "+
			"for device %q: %v", d.devName, err)
	}
	ch := make(chan *eveinfo.ZInfoClusterNode, 10)
	go func() {
		defer close(ch)
		for msg := range rawCh {
			ch <- msg.GetClusterNode()
		}
	}()
	return ch, unsub
}

// GetClusterInfo returns the last recorded information about the Kubernetes
// cluster, or nil if no such info message has been received yet.
func (d *EdgeDevice) GetClusterInfo() *eveinfo.ZInfoKubeCluster {
	devUUID := d.getDevUUID()
	var result *eveinfo.ZInfoKubeCluster
	d.iterateInfoMsgs(devUUID,
		func(msg *eveinfo.ZInfoMsg) bool {
			return msg.GetZtype() == eveinfo.ZInfoTypes_ZiKubeCluster &&
				msg.GetClusterInfo() != nil
		},
		func(msg *eveinfo.ZInfoMsg) {
			result = msg.GetClusterInfo()
		},
	)
	return result
}

// WatchClusterInfo subscribes to Kubernetes cluster info updates and returns a
// buffered channel that receives each new ZInfoKubeCluster as it arrives.
// Call the returned close function to stop watching and close the channel.
func (d *EdgeDevice) WatchClusterInfo() (
	updates <-chan *eveinfo.ZInfoKubeCluster, stop func()) {
	devUUID := d.getDevUUID()
	rawCh := make(chan *eveinfo.ZInfoMsg, 10)
	unsub, err := d.th.adamClient.SubscribeToDeviceInfoMsgs(devUUID,
		func(msg *eveinfo.ZInfoMsg) bool {
			return msg.GetZtype() == eveinfo.ZInfoTypes_ZiKubeCluster &&
				msg.GetClusterInfo() != nil
		},
		rawCh,
	)
	if err != nil {
		d.th.t.Fatalf("WatchClusterInfo: failed to subscribe to info messages "+
			"for device %q: %v", d.devName, err)
	}
	ch := make(chan *eveinfo.ZInfoKubeCluster, 10)
	go func() {
		defer close(ch)
		for msg := range rawCh {
			ch <- msg.GetClusterInfo()
		}
	}()
	return ch, unsub
}

// GetDeviceMetrics returns the last recorded device-level metrics,
// or nil if no metrics message has been received yet.
func (d *EdgeDevice) GetDeviceMetrics() *evemetrics.DeviceMetric {
	devUUID := d.getDevUUID()
	var result *evemetrics.DeviceMetric
	d.iterateMetricMsgs(devUUID,
		func(msg *evemetrics.ZMetricMsg) {
			if msg.GetDm() != nil {
				result = msg.GetDm()
			}
		},
	)
	return result
}

// WatchDeviceMetrics subscribes to device-level metrics updates and returns a
// buffered channel that receives each new DeviceMetric as it arrives.
// Call the returned close function to stop watching and close the channel.
func (d *EdgeDevice) WatchDeviceMetrics() (
	updates <-chan *evemetrics.DeviceMetric, stop func()) {
	devUUID := d.getDevUUID()
	rawCh := make(chan *evemetrics.ZMetricMsg, 10)
	unsub, err := d.th.adamClient.SubscribeToDeviceMetrics(devUUID, rawCh)
	if err != nil {
		d.th.t.Fatalf("WatchDeviceMetrics: failed to subscribe to metrics "+
			"for device %q: %v", d.devName, err)
	}
	ch := make(chan *evemetrics.DeviceMetric, 10)
	go func() {
		defer close(ch)
		for msg := range rawCh {
			if dm := msg.GetDm(); dm != nil {
				ch <- dm
			}
		}
	}()
	return ch, unsub
}

// GetAppMetrics returns the last recorded metrics for the specified application,
// or nil if no metrics message for that app has been received yet.
func (d *EdgeDevice) GetAppMetrics(appUUID uuid.UUID) *evemetrics.AppMetric {
	devUUID := d.getDevUUID()
	appUUIDStr := appUUID.String()
	var result *evemetrics.AppMetric
	d.iterateMetricMsgs(devUUID,
		func(msg *evemetrics.ZMetricMsg) {
			for _, am := range msg.GetAm() {
				if am.GetAppID() == appUUIDStr {
					result = am
				}
			}
		},
	)
	return result
}

// WatchAppMetrics subscribes to metrics updates for the specified application
// and returns a buffered channel that receives each new AppMetric as it arrives.
// Call the returned close function to stop watching and close the channel.
func (d *EdgeDevice) WatchAppMetrics(
	appUUID uuid.UUID) (updates <-chan *evemetrics.AppMetric, stop func()) {
	devUUID := d.getDevUUID()
	appUUIDStr := appUUID.String()
	rawCh := make(chan *evemetrics.ZMetricMsg, 10)
	unsub, err := d.th.adamClient.SubscribeToDeviceMetrics(devUUID, rawCh)
	if err != nil {
		d.th.t.Fatalf("WatchAppMetrics: failed to subscribe to metrics "+
			"for device %q: %v", d.devName, err)
	}
	ch := make(chan *evemetrics.AppMetric, 10)
	go func() {
		defer close(ch)
		for msg := range rawCh {
			for _, am := range msg.GetAm() {
				if am.GetAppID() == appUUIDStr {
					ch <- am
				}
			}
		}
	}()
	return ch, unsub
}

// GetNetworkInstanceMetrics returns the last recorded metrics for the specified
// network instance, or nil if no metrics message for it has been received yet.
func (d *EdgeDevice) GetNetworkInstanceMetrics(
	niUUID uuid.UUID) *evemetrics.ZMetricNetworkInstance {
	devUUID := d.getDevUUID()
	niUUIDStr := niUUID.String()
	var result *evemetrics.ZMetricNetworkInstance
	d.iterateMetricMsgs(devUUID,
		func(msg *evemetrics.ZMetricMsg) {
			for _, nm := range msg.GetNm() {
				if nm.GetNetworkID() == niUUIDStr {
					result = nm
				}
			}
		},
	)
	return result
}

// WatchNetworkInstanceMetrics subscribes to metrics updates for the specified
// network instance and returns a buffered channel that receives each new
// ZMetricNetworkInstance as it arrives.
// Call the returned close function to stop watching and close the channel.
func (d *EdgeDevice) WatchNetworkInstanceMetrics(
	niUUID uuid.UUID) (updates <-chan *evemetrics.ZMetricNetworkInstance, stop func()) {
	devUUID := d.getDevUUID()
	niUUIDStr := niUUID.String()
	rawCh := make(chan *evemetrics.ZMetricMsg, 10)
	unsub, err := d.th.adamClient.SubscribeToDeviceMetrics(devUUID, rawCh)
	if err != nil {
		d.th.t.Fatalf("WatchNetworkInstanceMetrics: failed to subscribe to metrics "+
			"for device %q: %v", d.devName, err)
	}
	ch := make(chan *evemetrics.ZMetricNetworkInstance, 10)
	go func() {
		defer close(ch)
		for msg := range rawCh {
			for _, nm := range msg.GetNm() {
				if nm.GetNetworkID() == niUUIDStr {
					ch <- nm
				}
			}
		}
	}()
	return ch, unsub
}

// GetVolumeMetrics returns the last recorded metrics for the specified storage
// volume, or nil if no metrics message for it has been received yet.
func (d *EdgeDevice) GetVolumeMetrics(volumeUUID uuid.UUID) *evemetrics.ZMetricVolume {
	devUUID := d.getDevUUID()
	volUUIDStr := volumeUUID.String()
	var result *evemetrics.ZMetricVolume
	d.iterateMetricMsgs(devUUID,
		func(msg *evemetrics.ZMetricMsg) {
			for _, vm := range msg.GetVm() {
				if vm.GetUuid() == volUUIDStr {
					result = vm
				}
			}
		},
	)
	return result
}

// WatchVolumeMetrics subscribes to metrics updates for the specified storage
// volume and returns a buffered channel that receives each new ZMetricVolume
// as it arrives.
// Call the returned close function to stop watching and close the channel.
func (d *EdgeDevice) WatchVolumeMetrics(
	volumeUUID uuid.UUID) (updates <-chan *evemetrics.ZMetricVolume, stop func()) {
	devUUID := d.getDevUUID()
	volUUIDStr := volumeUUID.String()
	rawCh := make(chan *evemetrics.ZMetricMsg, 10)
	unsub, err := d.th.adamClient.SubscribeToDeviceMetrics(devUUID, rawCh)
	if err != nil {
		d.th.t.Fatalf("WatchVolumeMetrics: failed to subscribe to metrics "+
			"for device %q: %v", d.devName, err)
	}
	ch := make(chan *evemetrics.ZMetricVolume, 10)
	go func() {
		defer close(ch)
		for msg := range rawCh {
			for _, vm := range msg.GetVm() {
				if vm.GetUuid() == volUUIDStr {
					ch <- vm
				}
			}
		}
	}()
	return ch, unsub
}

// GetClusterMetrics returns the last recorded metrics for the Kubernetes cluster,
// or nil if no cluster metrics message has been received yet.
func (d *EdgeDevice) GetClusterMetrics() *evemetrics.KubeClusterMetrics {
	devUUID := d.getDevUUID()
	var result *evemetrics.KubeClusterMetrics
	d.iterateMetricMsgs(devUUID,
		func(msg *evemetrics.ZMetricMsg) {
			if msg.GetCm() != nil {
				result = msg.GetCm()
			}
		},
	)
	return result
}

// WatchClusterMetrics subscribes to Kubernetes cluster metrics updates and
// returns a buffered channel that receives each new KubeClusterMetrics as it arrives.
// Call the returned close function to stop watching and close the channel.
func (d *EdgeDevice) WatchClusterMetrics() (
	updates <-chan *evemetrics.KubeClusterMetrics, stop func()) {
	devUUID := d.getDevUUID()
	rawCh := make(chan *evemetrics.ZMetricMsg, 10)
	unsub, err := d.th.adamClient.SubscribeToDeviceMetrics(devUUID, rawCh)
	if err != nil {
		d.th.t.Fatalf("WatchClusterMetrics: failed to subscribe to metrics "+
			"for device %q: %v", d.devName, err)
	}
	ch := make(chan *evemetrics.KubeClusterMetrics, 10)
	go func() {
		defer close(ch)
		for msg := range rawCh {
			if cm := msg.GetCm(); cm != nil {
				ch <- cm
			}
		}
	}()
	return ch, unsub
}

// ReadPublication retrieves a single message from a pub-sub topic published by
// the specified device and agent (microservice).
//
// Parameters:
//   - d: the EdgeDevice handle to read from
//   - fromAgent: the name of the agent/microservice publishing the topic
//   - key: identifies the specific message within the topic to fetch
//   - output: pointer to a value of type T to unmarshal the message into
//
// Returns an error if the topic or message does not exist, cannot be read, or
// fails to unmarshal into the provided output type.
func ReadPublication[T any](d *EdgeDevice, fromAgent string, persistent bool,
	key string, output *T) {
	fullName := fmt.Sprintf("%T", *new(T))
	typeName := fullName[strings.LastIndex(fullName, ".")+1:]
	var path string
	if persistent {
		path = fmt.Sprintf("/persistent/status/%s/%s/%s.json", fromAgent, typeName, key)
	} else {
		path = fmt.Sprintf("/run/%s/%s/%s.json", fromAgent, typeName, key)
	}
	data := d.ReadFile(path)
	if err := json.Unmarshal(data, output); err != nil {
		d.th.t.Fatalf("ReadPublication: failed to unmarshal %q from device %q: %v",
			path, d.devName, err)
	}
}

// ReadAllPublications retrieves all messages from a pub-sub topic published by
// the specified device and agent (microservice).
//
// Parameters:
//   - d: the EdgeDevice handle to read from
//   - fromAgent: the name of the agent/microservice publishing the topic
//
// Returns a slice of values of type T representing all messages from the topic,
// or an error if reading or unmarshaling fails.
func ReadAllPublications[T any](d *EdgeDevice, fromAgent string, persistent bool) []T {
	fullName := fmt.Sprintf("%T", *new(T))
	typeName := fullName[strings.LastIndex(fullName, ".")+1:]
	var dir string
	if persistent {
		dir = fmt.Sprintf("/persistent/status/%s/%s", fromAgent, typeName)
	} else {
		dir = fmt.Sprintf("/run/%s/%s", fromAgent, typeName)
	}
	// List all JSON files in the directory; suppress errors if the dir is absent.
	stdout, _, err := d.RunShellScript(
		"find "+shellEscape(dir)+" -maxdepth 1 -name '*.json' -type f 2>/dev/null || true",
		quickSSHCommandTimeout, 0)
	if err != nil {
		d.th.t.Fatalf("ReadAllPublications: failed to list %q on device %q: %v",
			dir, d.devName, err)
	}
	var results []T
	for _, file := range strings.Fields(stdout) {
		data := d.ReadFile(file)
		var item T
		if err := json.Unmarshal(data, &item); err != nil {
			d.th.t.Fatalf("ReadAllPublications: failed to unmarshal %q from device %q: %v",
				file, d.devName, err)
		}
		results = append(results, item)
	}
	return results
}

// getDevUUID returns the device UUID, calling t.Fatalf if not found/onboarded.
func (d *EdgeDevice) getDevUUID() uuid.UUID {
	d.th.devicesM.Lock()
	defer d.th.devicesM.Unlock()
	devState, found := d.th.devices[d.devName]
	if !found {
		d.th.t.Fatalf("Unknown device %q", d.devName)
	}
	if devState.ID == NilUUID {
		d.th.t.Fatalf("Device %q is not onboarded", d.devName)
	}
	return devState.ID
}

// iterateInfoMsgs fetches all info messages from Adam matching the filter,
// calling onMatch for each. It uses a short timeout and calls t.Fatalf on error.
func (d *EdgeDevice) iterateInfoMsgs(devUUID uuid.UUID,
	filter func(*eveinfo.ZInfoMsg) bool, onMatch func(*eveinfo.ZInfoMsg)) {
	ctx, cancel := context.WithTimeout(d.th.ctx, gatherInfoMsgsTimeout)
	defer cancel()
	err := d.th.adamClient.IterateDeviceInfoMsgs(ctx, devUUID, filter,
		infoMsgIterFn(func(msg *eveinfo.ZInfoMsg) (bool, error) {
			onMatch(msg)
			return false, nil
		}), false)
	if err != nil {
		d.th.t.Fatalf("Failed to retrieve info messages for device %q: %v",
			d.devName, err)
	}
}

// iterateMetricMsgs fetches all metric messages from Adam, calling onMatch for each.
// It uses a short timeout and calls t.Fatalf on error.
func (d *EdgeDevice) iterateMetricMsgs(
	devUUID uuid.UUID, onMatch func(*evemetrics.ZMetricMsg)) {
	ctx, cancel := context.WithTimeout(d.th.ctx, gatherMetricsMsgsTimeout)
	defer cancel()
	err := d.th.adamClient.IterateDeviceMetrics(ctx, devUUID,
		metricMsgIterFn(func(msg *evemetrics.ZMetricMsg) (bool, error) {
			onMatch(msg)
			return false, nil
		}), false)
	if err != nil {
		d.th.t.Fatalf("Failed to retrieve metrics for device %q: %v",
			d.devName, err)
	}
}

// infoMsgIterFn adapts a function to the controller.InfoMsgIterator interface.
type infoMsgIterFn func(*eveinfo.ZInfoMsg) (bool, error)

func (f infoMsgIterFn) Iterate(msg *eveinfo.ZInfoMsg) (bool, error) { return f(msg) }

// metricMsgIterFn adapts a function to the controller.MetricMsgIterator interface.
type metricMsgIterFn func(*evemetrics.ZMetricMsg) (bool, error)

func (f metricMsgIterFn) Iterate(msg *evemetrics.ZMetricMsg) (bool, error) { return f(msg) }

// appDownloadProgress returns the average download progress (0–100) across
// the app's volumes. For each volume UUID listed in volumeRefs the progress
// is taken from the latest ZInfoVolume in volumes:
//   - INVALID or INITIAL state → 0%
//   - DOWNLOADED or above      → 100%
//   - any other state          → ProgressPercentage as reported
//
// Returns 0 if volumeRefs is empty or no volume info has been received yet.
func appDownloadProgress(
	volumeRefs []string, volumes map[string]*eveinfo.ZInfoVolume) uint32 {
	if len(volumeRefs) == 0 {
		return 0
	}
	var total uint32
	for _, ref := range volumeRefs {
		vol, ok := volumes[ref]
		if !ok {
			// No info received yet for this volume; treat as 0%.
			continue
		}
		state := vol.GetState()
		switch {
		case state == eveinfo.ZSwState_INVALID || state == eveinfo.ZSwState_INITIAL:
			// 0 -- nothing added
		case state >= eveinfo.ZSwState_DOWNLOADED:
			total += 100
		default:
			total += vol.GetProgressPercentage()
		}
	}
	return total / uint32(len(volumeRefs))
}

// logMsgCollector accumulates log entries into []LogMsg, applying LogMsgMatch filters.
type logMsgCollector struct {
	match LogMsgMatch
	msgs  []LogMsg
}

func (c *logMsgCollector) toMatcher() logger.LogEntryMatcher {
	m := c.match
	return func(entry *evelogs.LogEntry) bool {
		ts := entry.GetTimestamp().AsTime()
		if m.Severity != "" && entry.GetSeverity() != m.Severity {
			return false
		}
		if m.Source != "" && entry.GetSource() != m.Source {
			return false
		}
		if m.Filename != "" && entry.GetFilename() != m.Filename {
			return false
		}
		if m.MsgHasSubstring != "" &&
			!strings.Contains(entry.GetContent(), m.MsgHasSubstring) {
			return false
		}
		if m.MsgMatchesRegexp.String() != "" &&
			!m.MsgMatchesRegexp.MatchString(entry.GetContent()) {
			return false
		}
		if !m.NotBefore.IsZero() && ts.Before(m.NotBefore) {
			return false
		}
		if !m.NotAfter.IsZero() && ts.After(m.NotAfter) {
			return false
		}
		return true
	}
}

func (c *logMsgCollector) Iterate(entry *evelogs.LogEntry) (bool, error) {
	c.msgs = append(c.msgs, LogMsg{
		Severity:  entry.GetSeverity(),
		Source:    entry.GetSource(),
		Filename:  entry.GetFilename(),
		Message:   entry.GetContent(),
		Timestamp: entry.GetTimestamp().AsTime(),
	})
	return false, nil
}

// shellEscape returns a single-quoted shell-safe version of s.
func shellEscape(s string) string {
	return "'" + strings.ReplaceAll(s, "'", `'\''`) + "'"
}
