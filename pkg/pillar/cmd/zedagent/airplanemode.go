// Copyright (c) 2021 Zededa, Inc.
// SPDX-License-Identifier: Apache-2.0

package zedagent

import (
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/golang/protobuf/proto"
	"github.com/lf-edge/eve/api/go/info"
	"github.com/lf-edge/eve/api/go/profile"
	"github.com/lf-edge/eve/pkg/pillar/flextimer"
	"github.com/lf-edge/eve/pkg/pillar/types"
	"github.com/lf-edge/eve/pkg/pillar/zedcloud"
)

const (
	radioUrlPath         = "/api/v1/radio"
	savedRadioConfigFile = "last-radio-config"
	radioPOSTInterval    = 5 * time.Second
)

func initializeAirplaneMode(ctx *getconfigContext) {
	ctx.triggerRadioPOST = make(chan Notify, 1)
	kernelArgs, err := os.ReadFile(kernelCmdArgs)
	if err != nil {
		log.Fatalf("Failed to read %s: %v\n", kernelCmdArgs, err)
	}
	for _, kernelArg := range strings.Split(string(kernelArgs), " ") {
		if kernelArg == permanentRFKillArg {
			ctx.airplaneMode.PermanentlyEnabled = true
		}
	}
	if !ctx.airplaneMode.PermanentlyEnabled {
		processSavedRadioConfig(ctx)
	}
	ctx.airplaneMode.RequestedAt = time.Now()
	// apply requested RF status immediately
	publishZedAgentStatus(ctx)
}

func triggerRadioPOST(ctx *getconfigContext) {
	log.Functionf("Triggering POST for %s from local server\n", radioUrlPath)
	select {
	case ctx.triggerRadioPOST <- struct{}{}:
		// Do nothing more
	default:
		log.Warnf("Failed to trigger Radio fetch operation")
	}
}

// Run a periodic POST request to fetch the intended state of radio devices from localServer.
func radioPOSTTask(ctx *getconfigContext) {
	max := float64(radioPOSTInterval)
	min := max * 0.3
	ticker := flextimer.NewRangeTicker(time.Duration(min), time.Duration(max))

	log.Functionf("radioPOSTTask: waiting for triggerRadioPOST")
	// wait for the first trigger
	<-ctx.triggerRadioPOST
	log.Functionf("radioPOSTTask: waiting for triggerRadioPOST done")
	//trigger again to pass into loop
	triggerRadioPOST(ctx)

	wdName := agentName + "-radio"

	// Run a periodic timer so we always update StillRunning
	stillRunning := time.NewTicker(25 * time.Second)
	ctx.zedagentCtx.ps.StillRunning(wdName, warningTime, errorTime)
	ctx.zedagentCtx.ps.RegisterFileWatchdog(wdName)

	task := func() {
		start := time.Now()
		status := getRadioStatus(ctx)
		if status == nil {
			log.Noticeln("Radio status is not yet available")
			return
		}
		config := getRadioConfig(ctx, status)
		if config != nil {
			if ctx.airplaneMode.PermanentlyEnabled {
				log.Functionf("Ignoring Radio Config, airplane mode is permanently enabled")
			} else if config.AirplaneMode != ctx.airplaneMode.Enabled {
				ctx.airplaneMode.Enabled = config.AirplaneMode
				ctx.airplaneMode.InProgress = true
				ctx.airplaneMode.RequestedAt = time.Now()
				publishZedAgentStatus(ctx)
			}
		}
		ctx.zedagentCtx.ps.CheckMaxTimeTopic(wdName, "radioPOSTTask", start,
			warningTime, errorTime)
	}
	for {
		select {
		case <-ctx.triggerRadioPOST:
			task()
		case <-ticker.C:
			task()
		case <-stillRunning.C:
		}
		ctx.zedagentCtx.ps.StillRunning(wdName, warningTime, errorTime)
	}
}

func getRadioStatus(ctx *getconfigContext) *profile.RadioStatus {
	obj, err := ctx.zedagentCtx.subDeviceNetworkStatus.Get("global")
	if err != nil {
		log.Error(err)
		return nil
	}
	dns := obj.(types.DeviceNetworkStatus)
	if !dns.AirplaneMode.RequestedAt.Equal(ctx.airplaneMode.RequestedAt) {
		log.Noticeln("Up-to-date airplane mode status is not available")
		return nil
	}
	if dns.AirplaneMode.InProgress {
		log.Noticeln("Skipping radio POST request - airplane-mode operation is still in progress")
		return nil
	}
	var cellularStatus []*info.CellularStatus
	for _, port := range dns.Ports {
		if port.WirelessStatus.WType != types.WirelessTypeCellular {
			continue
		}
		cellularStatus = append(cellularStatus,
			encodeCellularStatus(port.Logicallabel, port.WirelessStatus.Cellular))
	}
	return &profile.RadioStatus{
		AirplaneMode:   dns.AirplaneMode.IsEnabled(),
		ConfigError:    dns.AirplaneMode.ConfigError,
		CellularStatus: cellularStatus,
	}
}

func getRadioConfig(ctx *getconfigContext, radioStatus *profile.RadioStatus) *profile.RadioConfig {
	localProfileServer := ctx.localProfileServer
	if localProfileServer == "" {
		return nil
	}
	localServerURL, err := makeLocalServerBaseURL(localProfileServer)
	if err != nil {
		log.Errorf("getRadioConfig: makeLocalServerBaseURL: %v\n", err)
		return nil
	}
	if !ctx.localServerMap.upToDate {
		err := updateLocalServerMap(ctx, localServerURL)
		if err != nil {
			log.Errorf("getRadioConfig: updateLocalServerMap: %v\n", err)
			return nil
		}
	}
	srvMap := ctx.localServerMap.servers
	if len(srvMap) == 0 {
		log.Functionf("getRadioConfig: cannot find any configured apps for localServerURL: %s\n",
			localServerURL)
		return nil
	}

	var errList []string
	for bridgeName, servers := range srvMap {
		for _, srv := range servers {
			fullURL := srv.localServerAddr + radioUrlPath
			radioConfig := &profile.RadioConfig{}
			resp, err := zedcloud.SendLocalProto(
				zedcloudCtx, fullURL, bridgeName, srv.bridgeIP, radioStatus, radioConfig)
			if err != nil {
				errList = append(errList, fmt.Sprintf("SendLocalProto: %v", err))
				continue
			}
			if resp.StatusCode != http.StatusOK && resp.StatusCode != http.StatusNoContent {
				errList = append(errList, fmt.Sprintf("SendLocal: wrong response status code: %d",
					resp.StatusCode))
				continue
			}
			if resp.StatusCode == http.StatusNoContent {
				log.Functionf("Local server %s does not require change in the airplane mode", localServerURL)
				return nil
			}
			if radioConfig.GetServerToken() != ctx.profileServerToken {
				errList = append(errList,
					fmt.Sprintf("invalid token submitted by local server (%s)", radioConfig.GetServerToken()))
				continue
			}
			writeOrTouchRadioConfig(ctx, radioConfig)
			return radioConfig
		}
	}
	log.Errorf("getRadioConfig: all attempts failed: %s", strings.Join(errList, ";"))
	return nil
}

// read saved radio config in case of particular reboot reason
func readSavedRadioConfig(ctx *getconfigContext) (*profile.RadioConfig, error) {
	radioConfigBytes, ts, err := readSavedProtoMessage(
		ctx.zedagentCtx.globalConfig.GlobalValueInt(types.StaleConfigTime),
		filepath.Join(checkpointDirname, savedRadioConfigFile), false)
	if err != nil {
		return nil, fmt.Errorf("readSavedRadioConfig: %v", err)
	}
	if radioConfigBytes != nil {
		radioConfig := &profile.RadioConfig{}
		err := proto.Unmarshal(radioConfigBytes, radioConfig)
		if err != nil {
			return nil, fmt.Errorf("radio config unmarshalling failed: %v", err)
		}
		log.Noticef("Using saved radio config dated %s",
			ts.Format(time.RFC3339Nano))
		return radioConfig, nil
	}
	return nil, nil
}

// processSavedRadioConfig reads saved radio config and sets it.
func processSavedRadioConfig(ctx *getconfigContext) {
	radioConfig, err := readSavedRadioConfig(ctx)
	if err != nil {
		log.Functionf("readSavedRadioConfig failed: %v", err)
		return
	}
	if radioConfig != nil {
		log.Noticef("starting with radio config: %+v", radioConfig)
		ctx.airplaneMode.Enabled = radioConfig.AirplaneMode
	}
}

// writeOrTouchRadioConfig saves received RadioConfig into the persisted partition.
// If the config has not changed only the modification time is updated.
func writeOrTouchRadioConfig(ctx *getconfigContext, radioConfig *profile.RadioConfig) {
	if ctx.airplaneMode.Enabled == radioConfig.AirplaneMode &&
		ctx.profileServerToken == radioConfig.GetServerToken() {
		touchProtoMessage(savedRadioConfigFile)
		return
	}
	contents, err := proto.Marshal(radioConfig)
	if err != nil {
		log.Errorf("writeOrTouchRadioConfig: Marshalling failed: %v", err)
		return
	}
	writeProtoMessage(savedRadioConfigFile, contents)
	return
}
