// Copyright (c) 2021 Zededa, Inc.
// SPDX-License-Identifier: Apache-2.0

package dpcmanager

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"strings"
	"time"

	"github.com/fsnotify/fsnotify"
	"github.com/lf-edge/eve/pkg/pillar/base"
	"github.com/lf-edge/eve/pkg/pillar/devicenetwork"
	"github.com/lf-edge/eve/pkg/pillar/types"
)

type wwanWatcher struct {
	Log *base.LogObject
}

func (w *wwanWatcher) Watch(ctx context.Context) (<-chan WwanEvent, error) {
	fsw, err := fsnotify.NewWatcher()
	if err != nil {
		err = fmt.Errorf("failed to create fsnotify watcher for wwan dir: %w", err)
		w.Log.Error(err)
		return nil, err
	}
	if err = w.createWwanDir(); err != nil {
		_ = fsw.Close()
		return nil, err
	}
	if err = fsw.Add(devicenetwork.RunWwanDir); err != nil {
		_ = fsw.Close()
		return nil, err
	}
	// Buffered to ensure that runWatcher will not miss fsnotify
	// notification due to a delayed send.
	sub := make(chan WwanEvent, 10)
	go w.runWatcher(ctx, fsw, sub)
	return sub, nil
}

func (w *wwanWatcher) runWatcher(ctx context.Context, fsw *fsnotify.Watcher,
	sub chan WwanEvent) {
	for {
		select {
		case event, ok := <-fsw.Events:
			if !ok {
				w.Log.Warnf("fsnotify watcher for wwan directory stopped")
				return
			}
			switch event.Name {
			case devicenetwork.WwanStatusPath:
				sub <- WwanEventNewStatus
			case devicenetwork.WwanMetricsPath:
				sub <- WwanEventNewMetrics
			case devicenetwork.WwanLocationPath:
				sub <- WwanEventNewLocationInfo
			}
		case <-ctx.Done():
			return
		}
	}
}

func (w *wwanWatcher) LoadStatus() (status types.WwanStatus, err error) {
	statusFile, err := os.Open(devicenetwork.WwanStatusPath)
	if err != nil {
		w.Log.Errorf("Failed to open file %s: %v", devicenetwork.WwanStatusPath, err)
		return status, err
	}
	defer statusFile.Close()
	statusBytes, err := io.ReadAll(statusFile)
	if err != nil {
		w.Log.Errorf("Failed to read file %s: %v", devicenetwork.WwanStatusPath, err)
		return status, err
	}
	err = json.Unmarshal(statusBytes, &status)
	if err != nil {
		w.Log.Errorf("Failed to unmarshall wwan status: %v", err)
		return status, err
	}
	return status, nil
}

func (w *wwanWatcher) LoadMetrics() (metrics types.WwanMetrics, err error) {
	metricsFile, err := os.Open(devicenetwork.WwanMetricsPath)
	if err != nil {
		w.Log.Errorf("Failed to open file %s: %v", devicenetwork.WwanMetricsPath, err)
		return metrics, err
	}
	defer metricsFile.Close()
	metricsBytes, err := io.ReadAll(metricsFile)
	if err != nil {
		w.Log.Errorf("Failed to read file %s: %v", devicenetwork.WwanMetricsPath, err)
		return metrics, err
	}
	err = json.Unmarshal(metricsBytes, &metrics)
	if err != nil {
		w.Log.Errorf("Failed to unmarshall wwan metrics: %v", err)
		return metrics, err
	}
	return metrics, nil
}

func (w *wwanWatcher) LoadLocationInfo() (locInfo types.WwanLocationInfo, err error) {
	filepath := devicenetwork.WwanLocationPath
	locFile, err := os.Open(filepath)
	if err != nil {
		w.Log.Errorf("Failed to open file %s: %v", filepath, err)
		return locInfo, err
	}
	defer locFile.Close()
	locBytes, err := io.ReadAll(locFile)
	if err != nil {
		w.Log.Errorf("Failed to read file %s: %v", filepath, err)
		return locInfo, err
	}
	err = json.Unmarshal(locBytes, &locInfo)
	if err != nil {
		w.Log.Errorf("Failed to unmarshall wwan location data: %v", err)
		return locInfo, err
	}
	return locInfo, nil
}

func (w *wwanWatcher) createWwanDir() error {
	if _, err := os.Stat(devicenetwork.RunWwanDir); err != nil {
		if err = os.MkdirAll(devicenetwork.RunWwanDir, 0700); err != nil {
			err = fmt.Errorf("failed to create directory %s: %w",
				devicenetwork.RunWwanDir, err)
			w.Log.Error(err)
			return err
		}
	}
	return nil
}

// reloadWwanStatus loads the latest state data published by the wwan service.
func (m *DpcManager) reloadWwanStatus(ctx context.Context) {
	status, err := m.WwanWatcher.LoadStatus()
	if err != nil {
		// Already logged.
		return
	}
	expectedChecksum := m.reconcileStatus.RS.WwanConfigChecksum
	if expectedChecksum != "" && expectedChecksum != status.ConfigChecksum {
		m.Log.Noticef("Ignoring obsolete wwan status")
		return
	}

	netName := func(modem types.WwanNetworkStatus) string {
		netName := modem.LogicalLabel
		if netName == "" {
			netName = modem.PhysAddrs.Interface
		}
		return netName
	}

	status.DoSanitize()
	changed := !m.wwanStatus.Equal(status)
	if changed {
		m.Log.Functionf("Have new wwan status: %v", m.wwanStatus)
	}
	wasInProgress := m.rsStatus.ChangeInProgress
	m.wwanStatus = status

	if m.rsStatus.ChangeInProgress {
		var errMsgs []string
		if m.rsStatus.ConfigError != "" {
			errMsgs = append(errMsgs, m.rsStatus.ConfigError)
		}
		for _, network := range status.Networks {
			if network.ConfigError != "" {
				errMsgs = append(errMsgs, netName(network)+": "+network.ConfigError)
			}
		}
		if m.rsStatus.Imposed {
			for _, network := range status.Networks {
				if network.Module.OpMode != types.WwanOpModeRadioOff {
					// Failed to turn off the radio
					m.Log.Warnf("Modem %s (network: %s) is not in the radio-off operational state",
						network.Module.Name, netName(network))
					m.rsStatus.Imposed = false // the actual state
					if network.ConfigError == "" {
						errMsgs = append(errMsgs,
							fmt.Sprintf("%s: modem %s is not in the radio-off operational state",
								netName(network), network.Module.Name))
					}
				}
			}
		}
		m.rsStatus.ConfigError = strings.Join(errMsgs, "\n")
		m.rsStatus.ChangeInProgress = false
		m.Log.Noticeln("Radio-silence state changing operation has finalized (as seen by nim)")
	}

	if changed || wasInProgress {
		if m.currentDPC() != nil {
			changedDPC := m.setDiscoveredWwanIfNames(m.currentDPC())
			if changedDPC {
				m.publishDPCL()
			}
		}
		m.restartVerify(ctx, "wwan status changed")
		m.updateDNS()
	}
	if changed && m.PubWwanStatus != nil {
		// WWAN status is published to zedrouter, which then exposes it to apps
		// via meta-data server.
		m.Log.Functionf("PubWwanStatus: %+v", status)
		if err = m.PubWwanStatus.Publish("global", status); err != nil {
			m.Log.Errorf("Failed to publish wwan status: %v", err)
		}
	}
}

// reloadWwanMetrics loads the latest metrics published by the wwan service.
func (m *DpcManager) reloadWwanMetrics() {
	metrics, err := m.WwanWatcher.LoadMetrics()
	if err != nil {
		// Already logged.
		return
	}
	if m.wwanMetrics.Equal(metrics) {
		// nothing really changed
		return
	}

	m.wwanMetrics = metrics
	if m.PubWwanMetrics != nil {
		m.Log.Functionf("PubWwanMetrics: %+v", metrics)
		if err = m.PubWwanMetrics.Publish("global", metrics); err != nil {
			m.Log.Errorf("Failed to publish wwan metrics: %v", err)
		}
	}
}

// reloadWwanLocationInfo loads the latest location info published by the wwan service.
func (m *DpcManager) reloadWwanLocationInfo() {
	if !m.hasGlobalCfg {
		// Do not publish location info until we learn the publish interval
		// from the global config.
		return
	}
	locInfo, err := m.WwanWatcher.LoadLocationInfo()
	if err != nil {
		// Already logged.
		return
	}

	// Filter out location updates with invalid (aka unknown) coordinates or timestamps.
	if locInfo.Latitude < -90 || locInfo.Latitude > 90 ||
		locInfo.Longitude < -180 || locInfo.Longitude > 180 ||
		locInfo.UTCTimestamp == 0 {
		// Ignore the update.
		return
	}

	// We may receive location information from the GNSS receiver quite often.
	// In fact, qmicli (as used by the wwan microservice) hard-codes the interval between
	// location reports to 1 second, see:
	// https://github.com/freedesktop/libqmi/blob/qmi-1-30/src/qmicli/qmicli-loc.c#L1426
	// (value in the code is in milliseconds).
	// Here we rate-limit location updates (by dropping some) to decrease the volume
	// of pubsub messages.
	publishCloudInterval := time.Second *
		time.Duration(m.globalCfg.GlobalValueInt(types.LocationCloudInterval))
	publishAppInterval := time.Second *
		time.Duration(m.globalCfg.GlobalValueInt(types.LocationAppInterval))
	// Publish 2x more often (at most) than zedagent publishes to applications/controller.
	publishInterval := publishAppInterval
	if publishCloudInterval < publishInterval {
		// This is quite unlikely config.
		publishInterval = publishCloudInterval
	}
	maxRate := publishInterval >> 1

	// Do not drop if this is a very first location info.
	if m.lastPublishedLocInfo.UTCTimestamp != 0 {
		// More accurate location estimation than the previous one?
		var moreAccurate bool
		newUncertainty := locInfo.HorizontalUncertainty
		prevUncertainty := m.lastPublishedLocInfo.HorizontalUncertainty
		if newUncertainty >= 0 {
			if prevUncertainty < 0 || newUncertainty < prevUncertainty {
				moreAccurate = true
			}
		}
		// How much time elapsed between the last published location update
		// and this one (as measured by the modem).
		elapsed := time.Millisecond *
			time.Duration(locInfo.UTCTimestamp-m.lastPublishedLocInfo.UTCTimestamp)
		if !moreAccurate && elapsed < maxRate {
			// Drop the location update if it came too fast and wasn't more accurate
			// than the previous one.
			return
		}
	}

	if m.PubWwanLocationInfo != nil {
		m.Log.Functionf("PubWwanLocationInfo: %+v", locInfo)
		if err = m.PubWwanLocationInfo.Publish("global", locInfo); err != nil {
			m.Log.Errorf("Failed to publish wwan location info: %v", err)
		}
		m.lastPublishedLocInfo = locInfo
	}
}

// react to changed radio-silence configuration
func (m *DpcManager) doUpdateRadioSilence(ctx context.Context, newRS types.RadioSilence) {
	var errMsgs []string
	if !newRS.ChangeRequestedAt.After(m.rsConfig.ChangeRequestedAt) {
		return
	}

	// ChangeInProgress is enabled below if wwan config changes.
	m.rsStatus.ChangeInProgress = false
	m.rsStatus.ChangeRequestedAt = newRS.ChangeRequestedAt
	m.rsStatus.ConfigError = ""

	if newRS.ConfigError != "" {
		// Do not apply if configuration is marked as invalid by zedagent.
		// Keep RadioSilence.Imposed unchanged.
		errMsgs = append(errMsgs, newRS.ConfigError)
	} else {
		// Valid configuration, try to apply.
		wasImposed := m.rsConfig.Imposed
		m.rsConfig = newRS
		m.rsStatus.Imposed = newRS.Imposed

		// update RF state for wwan and wlan
		m.reconcileStatus = m.DpcReconciler.Reconcile(ctx, m.reconcilerArgs())
		if m.reconcileStatus.RS.ConfigError != "" {
			errMsgs = append(errMsgs, m.reconcileStatus.RS.ConfigError)
			m.rsStatus.Imposed = m.reconcileStatus.RS.Imposed // should be false
		} else if wasImposed != newRS.Imposed {
			m.rsStatus.ChangeInProgress = true // waiting for ack from wwan service
			m.Log.Noticef("Triggering radio-silence state change to: %s", m.rsConfig)
		}
	}

	m.rsStatus.ConfigError = strings.Join(errMsgs, "\n")
	m.updateDNS()
}

// Handle cellular modems referenced in the device model by USB or PCI addresses
// but without interface name included.
// Use status published by the wwan microservice to learn the name of the interface
// created by the kernel for the modem data-path.
func (m *DpcManager) setDiscoveredWwanIfNames(dpc *types.DevicePortConfig) bool {
	var changed bool
	ifNames := make(map[string]string) // interface name -> logical label
	currentDPC := m.currentDPC()
	for i := range dpc.Ports {
		port := &dpc.Ports[i]
		if port.WirelessCfg.WType != types.WirelessTypeCellular {
			continue
		}
		wwanNetStatus, found := m.wwanStatus.LookupNetworkStatus(port.Logicallabel)
		if found && wwanNetStatus.PhysAddrs.Interface != "" {
			ifNames[wwanNetStatus.PhysAddrs.Interface] = port.Logicallabel
			if port.IfName != wwanNetStatus.PhysAddrs.Interface {
				changed = true
			}
		} else if port.IfName == "" && currentDPC != nil && currentDPC != dpc {
			// Maybe we received new DPC while modem status is not yet available.
			// See if we can get interface name from the current DPC.
			currentPortConfig := currentDPC.GetPortByLogicalLabel(port.Logicallabel)
			if currentPortConfig != nil && currentPortConfig.IfName != "" &&
				currentPortConfig.USBAddr == port.USBAddr &&
				currentPortConfig.PCIAddr == port.PCIAddr {
				if _, used := ifNames[currentPortConfig.IfName]; !used {
					ifNames[currentPortConfig.IfName] = port.Logicallabel
					changed = true
				}
			}
		}
	}
	if !changed {
		return false
	}
	updatedPorts := make([]types.NetworkPortConfig, len(dpc.Ports))
	// First see if any wwan modem has changed interface name.
	for i := range dpc.Ports {
		port := &dpc.Ports[i]
		updatedPorts[i] = *port // copy
		if port.IfName != "" {
			if port2 := ifNames[port.IfName]; port2 != "" && port2 != port.Logicallabel {
				// This interface name was taken by port2.
				updatedPorts[i].IfName = ""
				m.Log.Noticef("Interface name %s was taken from port %s by port %s",
					port.IfName, port.Logicallabel, port2)
			}
		}
		for ifName, port2 := range ifNames {
			if port.Logicallabel == port2 {
				updatedPorts[i].IfName = ifName
			}
		}
	}
	dpc.Ports = updatedPorts
	return true
}
