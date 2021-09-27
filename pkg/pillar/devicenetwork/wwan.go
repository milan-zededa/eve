// Copyright (c) 2021 Zededa, Inc.
// SPDX-License-Identifier: Apache-2.0

package devicenetwork

import (
	"crypto/md5"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"github.com/fsnotify/fsnotify"
	"io/ioutil"
	"os"
	"reflect"
	"strings"

	"github.com/lf-edge/eve/pkg/pillar/base"
	"github.com/lf-edge/eve/pkg/pillar/types"
)

const (
	runWwanDir      = "/run/wwan/"
	wwanConfigPath  = runWwanDir + "config.json"
	wwanStatusPath  = runWwanDir + "status.json"
	wwanMetricsPath = runWwanDir + "metrics.json"
)

// WwanService encapsulates data exchanged between nim and the wwan service.
type WwanService struct {
	ConfigChecksum string
	Config         types.WwanConfig
	Status         types.WwanStatus
	Metrics        types.WwanMetrics
}

// InitWwanWatcher starts to watch for state data and metrics published by the wwan service.
func InitWwanWatcher(log *base.LogObject) (*fsnotify.Watcher, error) {
	w, err := fsnotify.NewWatcher()
	if err != nil {
		err = fmt.Errorf("failed to create wwan watcher: %v", err)
		log.Error(err)
		return nil, err
	}

	if err = createWwanDir(log); err != nil {
		return nil, err
	}
	if err = w.Add(runWwanDir); err != nil {
		_ = w.Close()
		return nil, err
	}
	return w, nil
}

// ProcessWwanWatchEvent processes change in the wwan status data or metrics.
func ProcessWwanWatchEvent(ctx *DeviceNetworkContext, event fsnotify.Event) {
	switch event.Name {
	case wwanStatusPath:
		ReloadWwanStatus(ctx)
	case wwanMetricsPath:
		ReloadWwanMetrics(ctx)
	}
}

// ReloadWwanStatus loads the latest state data published by the wwan service.
func ReloadWwanStatus(ctx *DeviceNetworkContext) {
	log := ctx.Log
	statusFile, err := os.Open(wwanStatusPath)
	if err != nil {
		log.Errorf("Failed to open file %s: %v\n", wwanStatusPath, err)
		return
	}
	defer statusFile.Close()

	var status types.WwanStatus
	statusBytes, err := ioutil.ReadAll(statusFile)
	if err != nil {
		log.Errorf("Failed to read file %s: %v\n", wwanStatusPath, err)
		return
	}

	err = json.Unmarshal(statusBytes, &status)
	if err != nil {
		log.Errorf("Failed to open file %s: %v\n", wwanStatusPath, err)
		return
	}

	expectedChecksum := ctx.WwanService.ConfigChecksum
	if expectedChecksum != "" && expectedChecksum != status.ConfigChecksum {
		log.Noticef("Ignoring obsolete wwan status")
		return
	}
	if reflect.DeepEqual(status, ctx.WwanService.Status) {
		// nothing really changed
		return
	}

	var radioTurnedOn bool
	ctx.WwanService.Status = status
	if ctx.AirplaneMode.InProgress {
		var errMsgs []string
		if ctx.AirplaneMode.ConfigError != "" {
			errMsgs = append(errMsgs, ctx.AirplaneMode.ConfigError)
		}
		for _, modem := range status.Modems {
			if modem.ConfigError != "" {
				devName := modem.DeviceName
				if devName == "" {
					devName = modem.PhysAddrs.Interface
				}
				errMsgs = append(errMsgs, devName+": "+modem.ConfigError)
			}
		}
		ctx.AirplaneMode.ConfigError = strings.Join(errMsgs, "\n")
		if ctx.AirplaneMode.Enabled {
			for _, modem := range status.Modems {
				if modem.OpMode != types.WOM_RADIO_OFF {
					// Failed to turn off the radio
					ctx.AirplaneMode.Enabled = false // the actual state
					break
				}
			}
		} else {
			radioTurnedOn = true
		}
		ctx.AirplaneMode.InProgress = false
	}

	if radioTurnedOn {
		// verification was disabled while the radio was turned OFF
		RestartVerify(ctx, "ReloadWwanStatus")
	} else if !ctx.Pending.Inprogress {
		newDNS := MakeDeviceNetworkStatus(ctx, *ctx.DevicePortConfig, *ctx.DeviceNetworkStatus)
		ctx.DeviceNetworkStatus = &newDNS
		if ctx.PubDeviceNetworkStatus != nil {
			log.Functionf("PublishDeviceNetworkStatus: %+v\n",
				ctx.DeviceNetworkStatus)
			ctx.PubDeviceNetworkStatus.Publish("global",
				*ctx.DeviceNetworkStatus)
		}
	}
}

// ReloadWwanMetrics loads the latest metrics published by the wwan service.
func ReloadWwanMetrics(ctx *DeviceNetworkContext) {
	log := ctx.Log
	metricsFile, err := os.Open(wwanMetricsPath)
	if err != nil {
		log.Errorf("Failed to open file %s: %v\n", wwanMetricsPath, err)
		return
	}
	defer metricsFile.Close()

	var metrics types.WwanMetrics
	metricsBytes, err := ioutil.ReadAll(metricsFile)
	if err != nil {
		log.Errorf("Failed to read file %s: %v\n", wwanMetricsPath, err)
		return
	}

	err = json.Unmarshal(metricsBytes, &metrics)
	if err != nil {
		log.Errorf("Failed to open file %s: %v\n", wwanMetricsPath, err)
		return
	}

	if reflect.DeepEqual(metrics, ctx.WwanService.Metrics) {
		// nothing really changed
		return
	}

	ctx.WwanService.Metrics = metrics
	if ctx.PubWwanMetrics != nil {
		log.Functionf("PubWwanMetrics: %+v\n", metrics)
		ctx.PubWwanMetrics.Publish("global", metrics)
	}
}

func updateWwanConfig(ctx *DeviceNetworkContext, portCfg *types.DevicePortConfig) (err error) {
	log := ctx.Log
	log.Functionf("updateWwanConfig: portCfg.Ports=%v\n", portCfg.Ports)

	newConfig := makeWwanConfig(ctx, portCfg)
	if !ctx.WwanService.Config.Equivalent(newConfig) {
		ctx.WwanService.Config = newConfig
		ctx.WwanService.ConfigChecksum, err = installWwanConfig(ctx.Log, ctx.WwanService.Config)
		return
	}
	return nil
}

func makeWwanConfig(ctx *DeviceNetworkContext, portCfg *types.DevicePortConfig) types.WwanConfig {
	log := ctx.Log
	config := types.WwanConfig{AirplaneMode: ctx.AirplaneMode.IsEnabled()}

	for _, port := range portCfg.Ports {
		if port.WirelessCfg.WType != types.WirelessTypeCellular || len(port.WirelessCfg.Cellular) == 0 {
			continue
		}
		ioBundle := ctx.AssignableAdapters.LookupIoBundleLogicallabel(port.Logicallabel)
		if ioBundle == nil {
			log.Warnf("Failed to find adapter with logical label '%s'\n", port.Logicallabel)
			continue
		}
		modem := types.WwanModemConfig{
			DeviceName: port.Logicallabel,
			PhysAddrs: types.WwanPhysAddrs{
				Interface: ioBundle.Ifname,
				USB:       ioBundle.UsbAddr,
				PCI:       ioBundle.PciLong,
			},
			// XXX Limited to a single APN for now
			Apns:      []string{port.WirelessCfg.Cellular[0].APN},
			ProbeAddr: port.WirelessCfg.Cellular[0].ProbeAddr,
		}
		config.Modems = append(config.Modems, modem)
	}
	return config
}

func createWwanDir(log *base.LogObject) error {
	if _, err := os.Stat(runWwanDir); err != nil {
		if err = os.MkdirAll(runWwanDir, 0700); err != nil {
			err = fmt.Errorf("Failed to create directory %s: %v\n", runWwanDir, err)
			log.Error(err)
			return err
		}
	}
	return nil
}

// Write cellular config into /run/wwan/config.json
func installWwanConfig(log *base.LogObject, config types.WwanConfig) (checksum string, err error) {
	if err = createWwanDir(log); err != nil {
		return "", err
	}

	log.Noticef("installWwanConfig: write file %s with config %+v", wwanConfigPath, config)
	file, err := os.Create(wwanConfigPath)
	if err != nil {
		err = fmt.Errorf("Failed to create file %s: %v\n", wwanConfigPath, err)
		log.Error(err)
		return "", err
	}
	defer file.Close()
	b, err := json.MarshalIndent(config, "", "    ")
	if err != nil {
		err = fmt.Errorf("failed to serialize wwan config: %v\n", err)
		log.Error(err)
		return "", err
	}
	if r, err := file.Write(b); err != nil || r != len(b) {
		err = fmt.Errorf("failed to write %d bytes to file %s: %v\n", len(b), file.Name(), err)
		log.Error(err)
		return "", err
	}

	hash := md5.Sum(b)
	return hex.EncodeToString(hash[:]), nil
}

// react to changed airplane mode configuration
func updateAirplaneMode(ctx *DeviceNetworkContext, airplaneMode types.AirplaneMode) {
	var (
		err     error
		errMsgs []string
		out     []byte
	)
	log := ctx.Log
	ctx.AirplaneMode = airplaneMode

	// (asynchronously) update RF state for wwan
	ctx.WwanService.Config.AirplaneMode = airplaneMode.IsEnabled()
	ctx.WwanService.ConfigChecksum, err = installWwanConfig(ctx.Log, ctx.WwanService.Config)
	if err != nil {
		errMsgs = append(errMsgs, fmt.Sprintf("Failed to install wwan config: %v", err))
		if ctx.AirplaneMode.Enabled {
			// failed to enable (can't even install config for wwan service)
			ctx.AirplaneMode.Enabled = false
		}
	} else {
		ctx.AirplaneMode.InProgress = true
	}

	// (synchronously) update rf state for wlan
	if !airplaneMode.PermanentlyEnabled {
		op := "block"
		if !airplaneMode.Enabled {
			op = "un" + op
		}
		args := []string{op, "wlan"}
		out, err = base.Exec(ctx.Log, "rfkill", args...).CombinedOutput()
		if err != nil {
			if ctx.AirplaneMode.Enabled {
				// failed to enable for wlan
				ctx.AirplaneMode.Enabled = false
			}
			errMsgs = append(errMsgs,
				fmt.Sprintf("'rfkill %s' command failed with err=%v, output=%s",
					strings.Join(args, " "), err, out))
		}
	}

	ctx.AirplaneMode.ConfigError = strings.Join(errMsgs, "\n")
	if !ctx.Pending.Inprogress {
		ctx.DeviceNetworkStatus.AirplaneMode = ctx.AirplaneMode
		if ctx.PubDeviceNetworkStatus != nil {
			log.Functionf("PublishDeviceNetworkStatus: %+v\n",
				ctx.DeviceNetworkStatus)
			ctx.PubDeviceNetworkStatus.Publish("global",
				*ctx.DeviceNetworkStatus)
		}
	}
}
