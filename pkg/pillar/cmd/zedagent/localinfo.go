// Copyright (c) 2022 Zededa, Inc.
// SPDX-License-Identifier: Apache-2.0

package zedagent

import (
	"encoding/json"
	"fmt"
	"net/http"
	"path/filepath"
	"strings"
	"time"

	"github.com/lf-edge/eve/api/go/profile"
	"github.com/lf-edge/eve/pkg/pillar/flextimer"
	"github.com/lf-edge/eve/pkg/pillar/types"
	"github.com/lf-edge/eve/pkg/pillar/zedcloud"
	uuid "github.com/satori/go.uuid"
)

const (
	localAppInfoURLPath               = "/api/v1/appinfo"
	localAppInfoPOSTInterval          = time.Minute
	localAppInfoPOSTThrottledInterval = time.Hour
	savedLocalCommandsFile            = "localcommands"
)

var (
	throttledLocalAppInfo bool
)

//updateLocalAppInfoTicker sets ticker options to the initial value
//if throttle set, will use localAppInfoPOSTThrottledInterval as interval
func updateLocalAppInfoTicker(ctx *getconfigContext, throttle bool) {
	interval := float64(localAppInfoPOSTInterval)
	if throttle {
		interval = float64(localAppInfoPOSTThrottledInterval)
	}
	max := 1.1 * interval
	min := 0.8 * max
	throttledLocalAppInfo = throttle
	ctx.localAppInfoPOSTTicker.UpdateRangeTicker(time.Duration(min), time.Duration(max))
}

func initializeLocalAppInfo(ctx *getconfigContext) {
	max := 1.1 * float64(localAppInfoPOSTInterval)
	min := 0.8 * max
	ctx.localAppInfoPOSTTicker = flextimer.NewRangeTicker(time.Duration(min), time.Duration(max))
}

func initializeLocalCommands(ctx *getconfigContext) {
	if !loadSavedLocalCommands(ctx) {
		// Write the initial empty content.
		ctx.localCommands = &types.LocalCommands{}
		persistLocalCommands(ctx.localCommands)
	}
}

func triggerLocalAppInfoPOST(ctx *getconfigContext) {
	log.Functionf("Triggering POST for %s to local server", localAppInfoURLPath)
	if throttledLocalAppInfo {
		log.Functionln("throttledLocalAppInfo flag set")
		return
	}
	ctx.localAppInfoPOSTTicker.TickNow()
}

// Run a periodic POST request to send information message about apps to local server
// and optionally receive app commands to run in the response.
func localAppInfoPOSTTask(ctx *getconfigContext) {

	log.Functionf("localAppInfoPOSTTask: waiting for localAppInfoPOSTTicker")
	// wait for the first trigger
	<-ctx.localAppInfoPOSTTicker.C
	log.Functionln("localAppInfoPOSTTask: waiting for localAppInfoPOSTTicker done")
	// trigger again to pass into the loop
	triggerLocalAppInfoPOST(ctx)

	wdName := agentName + "-localappinfo"

	// Run a periodic timer so we always update StillRunning
	stillRunning := time.NewTicker(25 * time.Second)
	ctx.zedagentCtx.ps.StillRunning(wdName, warningTime, errorTime)
	ctx.zedagentCtx.ps.RegisterFileWatchdog(wdName)

	for {
		select {
		case <-ctx.localAppInfoPOSTTicker.C:
			start := time.Now()
			appCmds := postLocalAppInfo(ctx)
			processReceivedAppCommands(ctx, appCmds)
			ctx.zedagentCtx.ps.CheckMaxTimeTopic(wdName, "localAppInfoPOSTTask", start,
				warningTime, errorTime)
		case <-stillRunning.C:
		}
		ctx.zedagentCtx.ps.StillRunning(wdName, warningTime, errorTime)
	}
}

// Post the current state of locally running application instances to the local server
// and optionally receive a set of app commands to run in the response.
func postLocalAppInfo(ctx *getconfigContext) *profile.LocalAppCmdList {
	localProfileServer := ctx.localProfileServer
	if localProfileServer == "" {
		return nil
	}
	localServerURL, err := makeLocalServerBaseURL(localProfileServer)
	if err != nil {
		log.Errorf("sendLocalAppInfo: makeLocalServerBaseURL: %v", err)
		return nil
	}
	if !ctx.localServerMap.upToDate {
		err := updateLocalServerMap(ctx, localServerURL)
		if err != nil {
			log.Errorf("sendLocalAppInfo: updateLocalServerMap: %v", err)
			return nil
		}
	}
	srvMap := ctx.localServerMap.servers
	if len(srvMap) == 0 {
		log.Functionf("sendLocalAppInfo: cannot find any configured apps for localServerURL: %s",
			localServerURL)
		return nil
	}

	localInfo := prepareLocalInfo(ctx)
	var errList []string
	for bridgeName, servers := range srvMap {
		for _, srv := range servers {
			fullURL := srv.localServerAddr + localAppInfoURLPath
			appCmds := &profile.LocalAppCmdList{}
			resp, err := zedcloud.SendLocalProto(
				zedcloudCtx, fullURL, bridgeName, srv.bridgeIP, localInfo, appCmds)
			if err != nil {
				errList = append(errList, fmt.Sprintf("SendLocalProto: %v", err))
				continue
			}
			switch resp.StatusCode {
			case http.StatusNotFound:
				// Throttle sending to be about once per hour.
				updateLocalAppInfoTicker(ctx, true)
				return nil
			case http.StatusOK:
				if len(appCmds.AppCommands) != 0 {
					if appCmds.GetServerToken() != ctx.profileServerToken {
						errList = append(errList,
							fmt.Sprintf("invalid token submitted by local server (%s)", appCmds.GetServerToken()))
						continue
					}
					updateLocalAppInfoTicker(ctx, false)
					return appCmds
				}
				// No content in the response.
				fallthrough
			case http.StatusNoContent:
				log.Functionf("Local server %s does not require additional app commands to execute",
					localServerURL)
				updateLocalAppInfoTicker(ctx, false)
				return nil
			default:
				errList = append(errList, fmt.Sprintf("SendLocal: wrong response status code: %d",
					resp.StatusCode))
				continue
			}
		}
	}
	log.Errorf("sendLocalAppInfo: all attempts failed: %s", strings.Join(errList, ";"))
	return nil
}

// TODO: move this logic to zedmanager
func processReceivedAppCommands(ctx *getconfigContext, cmdList *profile.LocalAppCmdList) {
	ctx.localCommands.Lock()
	defer ctx.localCommands.Unlock()
	if cmdList == nil {
		// Nothing requested by local server, just refresh the persisted config.
		if !ctx.localCommands.Empty() {
			touchLocalCommands()
		}
		return
	}

	var (
		cmdChanges    bool
		publishVols   []types.VolumeConfig
		unpublishVols []string // keys
	)
	publishApps := make(map[string]types.AppInstanceConfig)
	for _, appCmdReq := range cmdList.AppCommands {
		var err error
		appUUID := nilUUID
		if appCmdReq.Id != "" {
			appUUID, err = uuid.FromString(appCmdReq.Id)
			if err != nil {
				log.Warnf("Failed to parse UUID from app command request: %v", err)
				continue
			}
		}
		displayName := appCmdReq.Displayname
		if appUUID == nilUUID && displayName == "" {
			log.Warnf("App command request is missing both UUID and display name: %+v",
				appCmdReq)
			continue
		}
		// Try to find the application instance.
		appInst := findAppInstance(ctx, appUUID, displayName)
		if appInst == nil {
			log.Warnf("Failed to find app instance with UUID=%s, displayName=%s",
				appUUID, displayName)
			continue
		}
		appUUID = appInst.UUIDandVersion.UUID
		if _, duplicate := publishApps[appUUID.String()]; duplicate {
			log.Warnf("Multiple commands requested for app instance with UUID=%s",
				appUUID)
			continue
		}

		command := types.AppCommand(appCmdReq.Command)
		appCmd, hasLocalCmd := ctx.localCommands.AppCommands[appUUID.String()]
		if !hasLocalCmd {
			appCmd = &types.LocalAppCommand{}
			if ctx.localCommands.AppCommands == nil {
				ctx.localCommands.AppCommands = make(map[string]*types.LocalAppCommand)
			}
			ctx.localCommands.AppCommands[appUUID.String()] = appCmd
		}
		if appCmd.Command == command &&
			appCmd.LocalServerTimestamp == appCmdReq.Timestamp {
			// already accepted
			continue
		}
		appCmd.Command = command
		appCmd.LocalServerTimestamp = appCmdReq.Timestamp
		appCmd.DeviceTimestamp = time.Now()
		appCmd.Completed = false

		appCounters, hasCounters := ctx.localCommands.AppCounters[appUUID.String()]
		if !hasCounters {
			appCounters = &types.LocalAppCounters{}
			if ctx.localCommands.AppCounters == nil {
				ctx.localCommands.AppCounters = make(map[string]*types.LocalAppCounters)
			}
			ctx.localCommands.AppCounters[appUUID.String()] = appCounters
		}

		// Update configuration to trigger the operation.
		switch appCmd.Command {
		case types.AppCommandRestart:
			appCounters.RestartCmd.Counter++
			appCounters.RestartCmd.ApplyTime = appCmd.DeviceTimestamp.String()
			appInst.LocalRestartCmd = appCounters.RestartCmd
			publishApps[appUUID.String()] = *appInst

		case types.AppCommandPurge:
			appCounters.PurgeCmd.Counter++
			appCounters.PurgeCmd.ApplyTime = appCmd.DeviceTimestamp.String()
			appInst.LocalPurgeCmd = appCounters.PurgeCmd
			// Trigger purge of all volumes used by the application.
			// XXX Currently the assumption is that every volume instance is used
			//     by at most one application.
			if ctx.localCommands.VolumeGenCounters == nil {
				ctx.localCommands.VolumeGenCounters = make(map[string]int64)
			}
			for i := range appInst.VolumeRefConfigList {
				vr := &appInst.VolumeRefConfigList[i]
				uuid := vr.VolumeID.String()
				remoteGenCounter := vr.GenerationCounter
				localGenCounter := ctx.localCommands.VolumeGenCounters[uuid]
				// Un-publish volume with the current counters.
				volKey := volumeKey(uuid, remoteGenCounter, localGenCounter)
				volObj, _ := ctx.pubVolumeConfig.Get(volKey)
				if volObj == nil {
					log.Warnf("Failed to find volume %s referenced by app instance "+
						"with UUID=%s - not purging this volume", volKey, appUUID)
					continue
				}
				volume := volObj.(types.VolumeConfig)
				unpublishVols = append(unpublishVols, volKey)
				// Publish volume with an increased local generation counter.
				localGenCounter++
				ctx.localCommands.VolumeGenCounters[uuid] = localGenCounter
				vr.LocalGenerationCounter = localGenCounter
				volume.LocalGenerationCounter = localGenCounter
				publishVols = append(publishVols, volume)
			}
			publishApps[appUUID.String()] = *appInst
		}
		cmdChanges = true
	}

	// Persist application commands and counters before publishing
	// updated configuration.
	if cmdChanges {
		persistLocalCommands(ctx.localCommands)
	} else {
		// No actual configuration change to apply, just refresh the persisted config.
		touchLocalCommands()
	}

	// Publish updated configuration.
	for _, volKey := range unpublishVols {
		unpublishVolumeConfig(ctx, volKey)
	}
	for _, volume := range publishVols {
		publishVolumeConfig(ctx, volume)
	}
	for _, appInst := range publishApps {
		checkAndPublishAppInstanceConfig(ctx, appInst)
	}
	if len(publishVols) > 0 || len(unpublishVols) > 0 {
		signalVolumeConfigRestarted(ctx)
	}
}

func processAppCommandStatus(
	ctx *getconfigContext, appStatus types.AppInstanceStatus) {
	ctx.localCommands.Lock()
	defer ctx.localCommands.Unlock()
	uuid := appStatus.UUIDandVersion.UUID.String()
	appCmd, hasLocalCmd := ctx.localCommands.AppCommands[uuid]
	if !hasLocalCmd {
		// This app received no local command requests.
		return
	}
	if appCmd.Completed {
		// Nothing to update.
		return
	}
	if appStatus.PurgeInprogress != types.NotInprogress ||
		appStatus.RestartInprogress != types.NotInprogress {
		// A command is still ongoing.
		return
	}
	var updated bool
	switch appCmd.Command {
	case types.AppCommandRestart:
		if appStatus.RestartStartedAt.After(appCmd.DeviceTimestamp) {
			appCmd.Completed = true
			appCmd.LastCompletedTimestamp = appCmd.LocalServerTimestamp
			updated = true
			log.Noticef("Local restart completed: %+v", appCmd)
		}
	case types.AppCommandPurge:
		if appStatus.PurgeStartedAt.After(appCmd.DeviceTimestamp) {
			appCmd.Completed = true
			appCmd.LastCompletedTimestamp = appCmd.LocalServerTimestamp
			updated = true
			log.Noticef("Local purge completed: %+v", appCmd)
		}
	}
	if updated {
		persistLocalCommands(ctx.localCommands)
	}
}

// Add config submitted for the application via local profile server.
// ctx.localCommands should be locked!
func addLocalAppConfig(ctx *getconfigContext, appInstance *types.AppInstanceConfig) {
	uuid := appInstance.UUIDandVersion.UUID.String()
	appCounters, hasCounters := ctx.localCommands.AppCounters[uuid]
	if hasCounters {
		appInstance.LocalRestartCmd = appCounters.RestartCmd
		appInstance.LocalPurgeCmd = appCounters.PurgeCmd
	}
	for i := range appInstance.VolumeRefConfigList {
		vr := &appInstance.VolumeRefConfigList[i]
		uuid = vr.VolumeID.String()
		vr.LocalGenerationCounter = ctx.localCommands.VolumeGenCounters[uuid]
	}
}

// Delete all local config for this application.
// ctx.localCommands should be locked!
func delLocalAppConfig(ctx *getconfigContext, appInstance types.AppInstanceConfig) {
	uuid := appInstance.UUIDandVersion.UUID.String()
	delete(ctx.localCommands.AppCommands, uuid)
	delete(ctx.localCommands.AppCounters, uuid)
	for i := range appInstance.VolumeRefConfigList {
		vr := &appInstance.VolumeRefConfigList[i]
		uuid = vr.VolumeID.String()
		delete(ctx.localCommands.VolumeGenCounters, uuid)
	}
	persistLocalCommands(ctx.localCommands)
}

// Add config submitted for the volume via local profile server.
// ctx.localCommands should be locked!
func addLocalVolumeConfig(ctx *getconfigContext, volumeConfig *types.VolumeConfig) {
	uuid := volumeConfig.VolumeID.String()
	volumeConfig.LocalGenerationCounter = ctx.localCommands.VolumeGenCounters[uuid]
}

func prepareLocalInfo(ctx *getconfigContext) *profile.LocalAppInfoList {
	msg := profile.LocalAppInfoList{}
	ctx.localCommands.Lock()
	defer ctx.localCommands.Unlock()
	addAppInstanceFunc := func(key string, value interface{}) bool {
		ais := value.(types.AppInstanceStatus)
		zinfoAppInst := new(profile.LocalAppInfo)
		zinfoAppInst.Id = ais.UUIDandVersion.UUID.String()
		zinfoAppInst.Version = ais.UUIDandVersion.Version
		zinfoAppInst.Name = ais.DisplayName
		zinfoAppInst.Err = encodeErrorInfo(ais.ErrorAndTimeWithSource.ErrorDescription)
		zinfoAppInst.State = ais.State.ZSwState()
		if appCmd, hasEntry := ctx.localCommands.AppCommands[zinfoAppInst.Id]; hasEntry {
			zinfoAppInst.LastCmdTimestamp = appCmd.LastCompletedTimestamp
		}
		msg.AppsInfo = append(msg.AppsInfo, zinfoAppInst)
		return true
	}
	ctx.subAppInstanceStatus.Iterate(addAppInstanceFunc)
	return &msg
}

func findAppInstance(
	ctx *getconfigContext, appUUID uuid.UUID, displayName string) (appInst *types.AppInstanceConfig) {
	matchApp := func(_ string, value interface{}) bool {
		ais := value.(types.AppInstanceConfig)
		if (appUUID == nilUUID || appUUID == ais.UUIDandVersion.UUID) &&
			(displayName == "" || displayName == ais.DisplayName) {
			appInst = &ais
			// stop iteration
			return false
		}
		return true
	}
	ctx.pubAppInstanceConfig.Iterate(matchApp)
	return appInst
}

func readSavedLocalCommands(ctx *getconfigContext) (*types.LocalCommands, error) {
	commands := &types.LocalCommands{}
	contents, ts, err := readSavedConfig(
		ctx.zedagentCtx.globalConfig.GlobalValueInt(types.StaleConfigTime),
		filepath.Join(checkpointDirname, savedLocalCommandsFile), false)
	if err != nil {
		return commands, err
	}
	if contents != nil {
		err := json.Unmarshal(contents, &commands)
		if err != nil {
			return commands, err
		}
		log.Noticef("Using saved local commands dated %s",
			ts.Format(time.RFC3339Nano))
		return commands, nil
	}
	return commands, nil
}

// loadSavedLocalCommands reads saved locally-issued commands and sets them.
func loadSavedLocalCommands(ctx *getconfigContext) bool {
	commands, err := readSavedLocalCommands(ctx)
	if err != nil {
		log.Errorf("loadSavedLocalCommands failed: %v", err)
		return false
	}
	for _, appCmd := range commands.AppCommands {
		log.Noticef("Loaded persisted local app command: %+v", appCmd)
	}
	ctx.localCommands = commands
	return true
}

func persistLocalCommands(localCommands *types.LocalCommands) {
	contents, err := json.Marshal(localCommands)
	if err != nil {
		log.Fatalf("persistLocalCommands: Marshalling failed: %v", err)
	}
	saveConfig(savedLocalCommandsFile, contents)
	return
}

// touchLocalCommands is used to update the modification time of the persisted
// local commands.
func touchLocalCommands() {
	touchSavedConfig(savedLocalCommandsFile)
}
