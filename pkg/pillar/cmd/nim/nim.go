// Copyright (c) 2022 Zededa, Inc.
// SPDX-License-Identifier: Apache-2.0

package nim

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"io/ioutil"
	"os"
	"path"
	"strings"
	"time"

	"github.com/lf-edge/eve/pkg/pillar/agentlog"
	"github.com/lf-edge/eve/pkg/pillar/base"
	"github.com/lf-edge/eve/pkg/pillar/cipher"
	"github.com/lf-edge/eve/pkg/pillar/conntester"
	"github.com/lf-edge/eve/pkg/pillar/dpcmanager"
	"github.com/lf-edge/eve/pkg/pillar/dpcreconciler"
	"github.com/lf-edge/eve/pkg/pillar/flextimer"
	"github.com/lf-edge/eve/pkg/pillar/iptables"
	"github.com/lf-edge/eve/pkg/pillar/netmonitor"
	"github.com/lf-edge/eve/pkg/pillar/pidfile"
	"github.com/lf-edge/eve/pkg/pillar/pubsub"
	"github.com/lf-edge/eve/pkg/pillar/types"
	fileutils "github.com/lf-edge/eve/pkg/pillar/utils/file"
	"github.com/lf-edge/eve/pkg/pillar/zedcloud"
	uuid "github.com/satori/go.uuid"
	"github.com/sirupsen/logrus"
)

const (
	agentName = "nim"
	// Time limits for event loop handlers; shorter for nim than other agents
	errorTime    = 60 * time.Second
	warningTime  = 40 * time.Second
	stillRunTime = 25 * time.Second
)

const (
	configDevicePortConfigDir = types.IdentityDirname + "/DevicePortConfig"
	runDevicePortConfigDir    = "/run/global/DevicePortConfig"
	maxReadSize               = 16384       // Punt on too large files
	dpcAvailableTimeLimit     = time.Minute // TODO: make configurable?
)

// Really a constant
var nilUUID uuid.UUID

// Version is set from the Makefile.
var Version = "No version specified"

// NIM - Network Interface Manager.
// Manage (physical) network interfaces of the device based on configuration from
// various sources (controller, override, last-resort, persisted config).
// Verifies new configuration changes before fully applying them.
// Maintains old configuration with a lower-priority, but always tries to move
// to the most recent aka highest priority configuration.
type nim struct {
	Log    *base.LogObject
	Logger *logrus.Logger
	PubSub *pubsub.PubSub

	// CLI args
	debug         bool
	debugOverride bool // from command line arg
	useStdout     bool
	version       bool

	// NIM components
	connTester     *conntester.ZedcloudConnectivityTester
	dpcManager     *dpcmanager.DpcManager
	dpcReconciler  dpcreconciler.DpcReconciler
	networkMonitor netmonitor.NetworkMonitor

	// Subscriptions
	subGlobalConfig       pubsub.Subscription
	subControllerCert     pubsub.Subscription
	subCipherContext      pubsub.Subscription
	subEdgeNodeCert       pubsub.Subscription
	subDevicePortConfigA  pubsub.Subscription
	subDevicePortConfigO  pubsub.Subscription
	subDevicePortConfigS  pubsub.Subscription
	subZedAgentStatus     pubsub.Subscription
	subAssignableAdapters pubsub.Subscription

	// Publications
	pubDummyDevicePortConfig pubsub.Publication // For logging
	pubDevicePortConfig      pubsub.Publication
	pubDevicePortConfigList  pubsub.Publication
	pubCipherBlockStatus     pubsub.Publication
	pubDeviceNetworkStatus   pubsub.Publication
	pubZedcloudMetrics       pubsub.Publication
	pubCipherMetrics         pubsub.Publication
	pubWwanStatus            pubsub.Publication
	pubWwanMetrics           pubsub.Publication
	pubWwanLocationInfo      pubsub.Publication

	// Metrics
	zedcloudMetrics *zedcloud.AgentMetrics
	cipherMetrics   *cipher.AgentMetrics

	// Configuration
	globalConfig       types.ConfigItemValueMap
	gcInitialized      bool // Received initial GlobalConfig
	assignableAdapters types.AssignableAdapters
	enabledLastResort  bool
	forceLastResort    bool
	lastResort         *types.DevicePortConfig
}

// Run - Main function - invoked from zedbox.go
func Run(ps *pubsub.PubSub, logger *logrus.Logger, log *base.LogObject) int {
	nim := &nim{
		Log:    log,
		PubSub: ps,
		Logger: logger,
	}
	if err := nim.init(); err != nil {
		log.Fatal(err)
	}
	if err := nim.run(context.Background()); err != nil {
		log.Fatal(err)
	}
	return 0
}

func (n *nim) init() (err error) {
	n.processArgs()
	if n.version {
		fmt.Printf("%s: %s\n", os.Args[0], Version)
		return nil
	}
	if err = iptables.Init(n.Log); err != nil {
		return err
	}

	n.cipherMetrics = cipher.NewAgentMetrics(agentName)
	n.zedcloudMetrics = zedcloud.NewAgentMetrics()

	if err = n.initPublications(); err != nil {
		return err
	}
	if err = n.initSubscriptions(); err != nil {
		return err
	}

	// Initialize NIM components (for Linux network stack).
	linuxNetMonitor := &netmonitor.LinuxNetworkMonitor{
		Log: n.Log,
	}
	n.networkMonitor = linuxNetMonitor
	n.connTester = &conntester.ZedcloudConnectivityTester{
		Log:       n.Log,
		AgentName: agentName,
		Metrics:   n.zedcloudMetrics,
	}
	n.dpcReconciler = &dpcreconciler.LinuxDpcReconciler{
		Log:                  n.Log,
		ExportCurrentState:   true, // XXX make configurable
		ExportIntendedState:  true, // XXX make configurable
		AgentName:            agentName,
		NetworkMonitor:       linuxNetMonitor,
		SubControllerCert:    n.subControllerCert,
		SubCipherContext:     n.subCipherContext,
		SubEdgeNodeCert:      n.subEdgeNodeCert,
		PubCipherBlockStatus: n.pubCipherBlockStatus,
		CipherMetrics:        n.cipherMetrics,
	}
	n.dpcManager = &dpcmanager.DpcManager{
		Log:                      n.Log,
		Watchdog:                 n.PubSub,
		AgentName:                agentName,
		NetworkMonitor:           n.networkMonitor,
		DpcReconciler:            n.dpcReconciler,
		ConnTester:               n.connTester,
		PubDummyDevicePortConfig: n.pubDummyDevicePortConfig,
		PubDevicePortConfigList:  n.pubDevicePortConfigList,
		PubDeviceNetworkStatus:   n.pubDeviceNetworkStatus,
		PubWwanStatus:            n.pubWwanStatus,
		PubWwanMetrics:           n.pubWwanMetrics,
		PubWwanLocationInfo:      n.pubWwanLocationInfo,
		ZedcloudMetrics:          n.zedcloudMetrics,
	}
	return nil
}

func (n *nim) run(ctx context.Context) (err error) {
	if err = pidfile.CheckAndCreatePidfile(n.Log, agentName); err != nil {
		return err
	}
	n.Log.Noticef("Starting %s", agentName)

	// Start DPC Manager.
	if err = n.dpcManager.Init(ctx); err != nil {
		return err
	}
	if err = n.dpcManager.Run(ctx); err != nil {
		return err
	}

	// Wait for initial GlobalConfig.
	if err = n.subGlobalConfig.Activate(); err != nil {
		return err
	}
	for !n.gcInitialized {
		n.Log.Noticef("Waiting for GCInitialized")
		select {
		case change := <-n.subGlobalConfig.MsgChan():
			n.subGlobalConfig.ProcessChange(change)
		}
	}
	n.Log.Noticef("Processed GlobalConfig")

	// Check if we have a /config/DevicePortConfig/*.json which we need to
	// take into account by copying it to /run/global/DevicePortConfig/
	// We tag it with a OriginFile so that the file in /config/DevicePortConfig/
	// will be deleted once we have published its content in
	// the DevicePortConfigList.
	// This avoids repeated application of this startup file.
	n.ingestDevicePortConfig()

	// Activate some subscriptions.
	// Not all yet though, first we wait for last-resort and AA to initialize.
	if err = n.subControllerCert.Activate(); err != nil {
		return err
	}
	if err = n.subEdgeNodeCert.Activate(); err != nil {
		return err
	}
	if err = n.subCipherContext.Activate(); err != nil {
		return err
	}
	if err = n.subDevicePortConfigS.Activate(); err != nil {
		return err
	}

	// Run a periodic timer so we always update StillRunning
	stillRunning := time.NewTicker(stillRunTime)
	n.PubSub.StillRunning(agentName, warningTime, errorTime)

	// Publish metrics for zedagent every 10 seconds
	interval := 10 * time.Second
	max := float64(interval)
	min := max * 0.3
	publishTimer := flextimer.NewRangeTicker(time.Duration(min), time.Duration(max))

	// Watch for interface changes to update last resort DPC.
	done := make(chan struct{})
	defer close(done)
	netEvents := n.networkMonitor.WatchEvents(ctx, agentName)

	// Time limit to obtain some network config.
	// If it runs out and we still do not have any config,
	// lastresort will be enabled unconditionally.
	dpcAvailTimer := time.After(dpcAvailableTimeLimit)

	waitForLastResort := n.enabledLastResort
	waitForAA := true

	lastResortIsReady := func() error {
		if err = n.subDevicePortConfigO.Activate(); err != nil {
			return err
		}
		if err = n.subZedAgentStatus.Activate(); err != nil {
			return err
		}
		err = n.subAssignableAdapters.Activate()
		return err
	}
	if !waitForLastResort {
		if err = lastResortIsReady(); err != nil {
			return err
		}
	} else {
		n.Log.Notice("Waiting for last-resort DPC...")
	}

	for {
		select {
		case change := <-n.subControllerCert.MsgChan():
			n.subControllerCert.ProcessChange(change)

		case change := <-n.subEdgeNodeCert.MsgChan():
			n.subEdgeNodeCert.ProcessChange(change)

		case change := <-n.subCipherContext.MsgChan():
			n.subCipherContext.ProcessChange(change)

		case change := <-n.subGlobalConfig.MsgChan():
			n.subGlobalConfig.ProcessChange(change)
			if waitForLastResort && !n.enabledLastResort {
				waitForLastResort = false
				n.Log.Notice("last-resort DPC is not enabled")
				if err = lastResortIsReady(); err != nil {
					return err
				}
			}

		case change := <-n.subDevicePortConfigA.MsgChan():
			n.subDevicePortConfigA.ProcessChange(change)

		case change := <-n.subDevicePortConfigO.MsgChan():
			n.subDevicePortConfigO.ProcessChange(change)

		case change := <-n.subDevicePortConfigS.MsgChan():
			n.subDevicePortConfigS.ProcessChange(change)
			if waitForLastResort && n.lastResort != nil {
				waitForLastResort = false
				n.Log.Notice("last-resort DPC is ready")
				if err = lastResortIsReady(); err != nil {
					return err
				}
			}

		case change := <-n.subZedAgentStatus.MsgChan():
			n.subZedAgentStatus.ProcessChange(change)

		case change := <-n.subAssignableAdapters.MsgChan():
			n.subAssignableAdapters.ProcessChange(change)
			if waitForAA && n.assignableAdapters.Initialized {
				n.Log.Noticef("Assignable Adapters are initialized")
				if err = n.subDevicePortConfigA.Activate(); err != nil {
					return err
				}
				go n.queryControllerDNS()
				waitForAA = false
			}

		case event := <-netEvents:
			ifChange, isIfChange := event.(netmonitor.IfChange)
			if isIfChange {
				n.processInterfaceChange(ifChange)
			}

		case <-publishTimer.C:
			start := time.Now()
			err = n.cipherMetrics.Publish(n.Log, n.pubCipherMetrics, "global")
			if err != nil {
				n.Log.Error(err)
			}
			err = n.zedcloudMetrics.Publish(n.Log, n.pubZedcloudMetrics, "global")
			if err != nil {
				n.Log.Error(err)
			}
			n.PubSub.CheckMaxTimeTopic(agentName, "publishTimer", start,
				warningTime, errorTime)

		case <-dpcAvailTimer:
			obj, err := n.pubDevicePortConfigList.Get("global")
			if err != nil {
				n.Log.Errorf("Failed to get published DPCL: %v", err)
				continue
			}
			dpcl := obj.(types.DevicePortConfigList)
			if len(dpcl.PortConfigList) == 0 {
				n.Log.Noticef("DPC Manager has no network config to work with "+
					"even after %v, enabling lastresort unconditionally", dpcAvailableTimeLimit)
				n.forceLastResort = true
				n.enabledLastResort = true
				n.updateLastResortDPC("lastresort forcefully enabled")
			}

		case <-ctx.Done():
			return nil

		case <-stillRunning.C:
		}
		n.PubSub.StillRunning(agentName, warningTime, errorTime)
	}
}

func (n *nim) processArgs() {
	versionPtr := flag.Bool("v", false, "Print Version of the agent.")
	debugPtr := flag.Bool("d", false, "Set Debug level")
	stdoutPtr := flag.Bool("s", false, "Use stdout")
	flag.Parse()

	n.debug = *debugPtr
	n.debugOverride = n.debug
	n.useStdout = *stdoutPtr
	if n.debugOverride {
		logrus.SetLevel(logrus.TraceLevel)
	} else {
		logrus.SetLevel(logrus.InfoLevel)
	}
	n.version = *versionPtr
}

func (n *nim) initPublications() (err error) {
	n.pubDeviceNetworkStatus, err = n.PubSub.NewPublication(
		pubsub.PublicationOptions{
			AgentName: agentName,
			TopicType: types.DeviceNetworkStatus{},
		})
	if err != nil {
		return err
	}
	if err = n.pubDeviceNetworkStatus.ClearRestarted(); err != nil {
		return err
	}

	n.pubZedcloudMetrics, err = n.PubSub.NewPublication(
		pubsub.PublicationOptions{
			AgentName: agentName,
			TopicType: types.MetricsMap{},
		})
	if err != nil {
		return err
	}

	n.pubDevicePortConfig, err = n.PubSub.NewPublication(
		pubsub.PublicationOptions{
			AgentName: agentName,
			TopicType: types.DevicePortConfig{},
		})
	if err != nil {
		return err
	}
	if err = n.pubDevicePortConfig.ClearRestarted(); err != nil {
		return err
	}

	// Publication to get logs
	n.pubDummyDevicePortConfig, err = n.PubSub.NewPublication(
		pubsub.PublicationOptions{
			AgentName:  agentName,
			AgentScope: "dummy",
			TopicType:  types.DevicePortConfig{},
		})
	if err != nil {
		return err
	}
	if err = n.pubDummyDevicePortConfig.ClearRestarted(); err != nil {
		return err
	}

	n.pubDevicePortConfigList, err = n.PubSub.NewPublication(
		pubsub.PublicationOptions{
			AgentName:  agentName,
			Persistent: true,
			TopicType:  types.DevicePortConfigList{},
		})
	if err != nil {
		return err
	}
	if err = n.pubDevicePortConfigList.ClearRestarted(); err != nil {
		return err
	}

	n.pubCipherBlockStatus, err = n.PubSub.NewPublication(
		pubsub.PublicationOptions{
			AgentName: agentName,
			TopicType: types.CipherBlockStatus{},
		})
	if err != nil {
		return err
	}

	n.pubCipherMetrics, err = n.PubSub.NewPublication(pubsub.PublicationOptions{
		AgentName: agentName,
		TopicType: types.CipherMetrics{},
	})
	if err != nil {
		return err
	}

	n.pubWwanStatus, err = n.PubSub.NewPublication(
		pubsub.PublicationOptions{
			AgentName: agentName,
			TopicType: types.WwanStatus{},
		})
	if err != nil {
		return err
	}

	n.pubWwanMetrics, err = n.PubSub.NewPublication(
		pubsub.PublicationOptions{
			AgentName: agentName,
			TopicType: types.WwanMetrics{},
		})
	if err != nil {
		return err
	}

	n.pubWwanLocationInfo, err = n.PubSub.NewPublication(
		pubsub.PublicationOptions{
			AgentName: agentName,
			TopicType: types.WwanLocationInfo{},
		})
	if err != nil {
		return err
	}
	return nil
}

func (n *nim) initSubscriptions() (err error) {
	// Look for global config such as log levels.
	n.subGlobalConfig, err = n.PubSub.NewSubscription(pubsub.SubscriptionOptions{
		AgentName:     "zedagent",
		MyAgentName:   agentName,
		TopicImpl:     types.ConfigItemValueMap{},
		Persistent:    true,
		Activate:      false,
		CreateHandler: n.handleGlobalConfigCreate,
		ModifyHandler: n.handleGlobalConfigModify,
		DeleteHandler: n.handleGlobalConfigDelete,
		SyncHandler:   n.handleGlobalConfigSynchronized,
		WarningTime:   warningTime,
		ErrorTime:     errorTime,
	})
	if err != nil {
		return err
	}

	// Look for controller certs which will be used for decryption.
	n.subControllerCert, err = n.PubSub.NewSubscription(pubsub.SubscriptionOptions{
		AgentName:   "zedagent",
		MyAgentName: agentName,
		TopicImpl:   types.ControllerCert{},
		Activate:    false,
		WarningTime: warningTime,
		ErrorTime:   errorTime,
		Persistent:  true,
	})
	if err != nil {
		return err
	}

	// Look for edge node certs which will be used for decryption
	n.subEdgeNodeCert, err = n.PubSub.NewSubscription(pubsub.SubscriptionOptions{
		AgentName:   "tpmmgr",
		MyAgentName: agentName,
		TopicImpl:   types.EdgeNodeCert{},
		Activate:    false,
		Persistent:  true,
		WarningTime: warningTime,
		ErrorTime:   errorTime,
	})
	if err != nil {
		return err
	}

	// Look for cipher context which will be used for decryption
	n.subCipherContext, err = n.PubSub.NewSubscription(pubsub.SubscriptionOptions{
		AgentName:   "zedagent",
		MyAgentName: agentName,
		TopicImpl:   types.CipherContext{},
		Activate:    false,
		WarningTime: warningTime,
		ErrorTime:   errorTime,
		Persistent:  true,
	})
	if err != nil {
		return err
	}

	// We get DevicePortConfig from three sources in this priority:
	// 1. zedagent publishing DevicePortConfig
	// 2. override file in /run/global/DevicePortConfig/*.json
	// 3. "lastresort" derived from the set of network interfaces
	n.subDevicePortConfigA, err = n.PubSub.NewSubscription(pubsub.SubscriptionOptions{
		AgentName:     "zedagent",
		MyAgentName:   agentName,
		TopicImpl:     types.DevicePortConfig{},
		Activate:      false,
		CreateHandler: n.handleDPCCreate,
		ModifyHandler: n.handleDPCModify,
		DeleteHandler: n.handleDPCDelete,
		WarningTime:   warningTime,
		ErrorTime:     errorTime,
	})
	if err != nil {
		return err
	}

	n.subDevicePortConfigO, err = n.PubSub.NewSubscription(pubsub.SubscriptionOptions{
		AgentName:     "",
		MyAgentName:   agentName,
		TopicImpl:     types.DevicePortConfig{},
		Activate:      false,
		CreateHandler: n.handleDPCCreate,
		ModifyHandler: n.handleDPCModify,
		DeleteHandler: n.handleDPCDelete,
		WarningTime:   warningTime,
		ErrorTime:     errorTime,
	})
	if err != nil {
		return err
	}

	n.subDevicePortConfigS, err = n.PubSub.NewSubscription(pubsub.SubscriptionOptions{
		AgentName:     agentName,
		MyAgentName:   agentName,
		TopicImpl:     types.DevicePortConfig{},
		Activate:      false,
		CreateHandler: n.handleDPCCreate,
		ModifyHandler: n.handleDPCModify,
		DeleteHandler: n.handleDPCDelete,
		WarningTime:   warningTime,
		ErrorTime:     errorTime,
	})
	if err != nil {
		return err
	}

	// To read radio silence configuration.
	n.subZedAgentStatus, err = n.PubSub.NewSubscription(pubsub.SubscriptionOptions{
		AgentName:     "zedagent",
		MyAgentName:   agentName,
		TopicImpl:     types.ZedAgentStatus{},
		Activate:      false,
		CreateHandler: n.handleZedAgentStatusCreate,
		ModifyHandler: n.handleZedAgentStatusModify,
		WarningTime:   warningTime,
		ErrorTime:     errorTime,
	})
	if err != nil {
		return err
	}

	// To determine which ports are in PCIBack.
	n.subAssignableAdapters, err = n.PubSub.NewSubscription(pubsub.SubscriptionOptions{
		AgentName:     "domainmgr",
		MyAgentName:   agentName,
		TopicImpl:     types.AssignableAdapters{},
		Activate:      false,
		CreateHandler: n.handleAssignableAdaptersCreate,
		ModifyHandler: n.handleAssignableAdaptersModify,
		DeleteHandler: n.handleAssignableAdaptersDelete,
		WarningTime:   warningTime,
		ErrorTime:     errorTime,
	})
	if err != nil {
		return err
	}

	return nil
}

func (n *nim) handleGlobalConfigCreate(_ interface{}, key string, _ interface{}) {
	n.handleGlobalConfigImpl(key)
}

func (n *nim) handleGlobalConfigModify(_ interface{}, key string, _, _ interface{}) {
	n.handleGlobalConfigImpl(key)
}

func (n *nim) handleGlobalConfigImpl(key string) {
	if key != "global" {
		n.Log.Functionf("handleGlobalConfigImpl: ignoring %s", key)
		return
	}
	var gcp *types.ConfigItemValueMap
	n.debug, gcp = agentlog.HandleGlobalConfig(n.Log, n.subGlobalConfig, agentName,
		n.debugOverride, n.Logger)
	n.applyGlobalConfig(gcp)
}

func (n *nim) handleGlobalConfigDelete(_ interface{}, key string, _ interface{}) {
	if key != "global" {
		n.Log.Functionf("handleGlobalConfigDelete: ignoring %s", key)
		return
	}
	n.debug, _ = agentlog.HandleGlobalConfig(n.Log, n.subGlobalConfig, agentName,
		n.debugOverride, n.Logger)
	n.applyGlobalConfig(types.DefaultConfigItemValueMap())
}

// In case there is no GlobalConfig.json this will move us forward.
func (n *nim) handleGlobalConfigSynchronized(_ interface{}, done bool) {
	n.Log.Functionf("handleGlobalConfigSynchronized(%v)", done)
	if done && !n.gcInitialized {
		n.applyGlobalConfig(types.DefaultConfigItemValueMap())
	}
}

func (n *nim) applyGlobalConfig(gcp *types.ConfigItemValueMap) {
	if gcp == nil {
		return
	}
	n.globalConfig = *gcp
	n.dpcManager.UpdateGCP(n.globalConfig)
	timeout := gcp.GlobalValueInt(types.NetworkTestTimeout)
	n.connTester.TestTimeout = time.Second * time.Duration(timeout)
	fallbackAnyEth := gcp.GlobalValueTriState(types.NetworkFallbackAnyEth)
	enableLastResort := fallbackAnyEth == types.TS_ENABLED
	enableLastResort = enableLastResort || n.forceLastResort
	if n.enabledLastResort != enableLastResort {
		if enableLastResort {
			n.updateLastResortDPC("lastresort enabled by global config")
			n.enabledLastResort = true
		} else {
			n.removeLastResortDPC()
			n.enabledLastResort = false
		}
	}
	n.gcInitialized = true
}

// handleDPCCreate handles three different sources in this priority order:
// 1. zedagent with any key
// 2. "usb" key from build or USB stick file
// 3. "lastresort" derived from the set of network interfaces
// We determine the priority from TimePriority in the config.
func (n *nim) handleDPCCreate(_ interface{}, key string, configArg interface{}) {
	n.handleDPCImpl(key, configArg)
}

// handleDPCModify handles three different sources as above
func (n *nim) handleDPCModify(_ interface{}, key string, configArg, _ interface{}) {
	n.handleDPCImpl(key, configArg)
}

func (n *nim) handleDPCImpl(key string, configArg interface{}) {
	dpc := configArg.(types.DevicePortConfig)
	dpc.DoSanitize(n.Log, true, true, key, true, true)
	n.dpcManager.AddDPC(dpc)
}

func (n *nim) handleDPCDelete(_ interface{}, key string, configArg interface{}) {
	dpc := configArg.(types.DevicePortConfig)
	dpc.DoSanitize(n.Log, false, true, key, true, true)
	n.dpcManager.DelDPC(dpc)
}

func (n *nim) handleAssignableAdaptersCreate(_ interface{}, key string, configArg interface{}) {
	n.handleAssignableAdaptersImpl(key, configArg)
}

func (n *nim) handleAssignableAdaptersModify(_ interface{}, key string, configArg, _ interface{}) {
	n.handleAssignableAdaptersImpl(key, configArg)
}

func (n *nim) handleAssignableAdaptersImpl(key string, configArg interface{}) {
	if key != "global" {
		n.Log.Functionf("handleAssignableAdaptersImpl: ignoring %s\n", key)
		return
	}
	assignableAdapters := configArg.(types.AssignableAdapters)
	n.assignableAdapters = assignableAdapters
	n.dpcManager.UpdateAA(n.assignableAdapters)
	if n.enabledLastResort {
		n.updateLastResortDPC("assignable adapters changed")
	}
}

func (n *nim) handleAssignableAdaptersDelete(_ interface{}, key string, _ interface{}) {
	// This usually happens only at restart - as any changes to assignable
	// adapters results in domain restart and takes affect only after
	// the restart.
	// UsbAccess can change dynamically - but it is not network device,
	// so can be ignored. Assuming there are no USB based network interfaces.
	n.Log.Functionf("handleAssignableAdaptersDelete done for %s\n", key)
}

func (n *nim) handleZedAgentStatusCreate(_ interface{}, key string, statusArg interface{}) {
	n.handleZedAgentStatusImpl(key, statusArg)
}

func (n *nim) handleZedAgentStatusModify(_ interface{}, key string, statusArg, _ interface{}) {
	n.handleZedAgentStatusImpl(key, statusArg)
}

func (n *nim) handleZedAgentStatusImpl(_ string, statusArg interface{}) {
	zedagentStatus := statusArg.(types.ZedAgentStatus)
	n.dpcManager.UpdateRadioSilence(zedagentStatus.RadioSilence)
}

// ingestPortConfig reads all json files in configDevicePortConfigDir, ensures
// they have a TimePriority, and adds a OriginFile to them and then writes to
// runDevicePortConfigDir.
// Later the OriginFile field will result in removing the original file from
// /config/DevicePortConfig/ to avoid re-application (this is done by DPC Manager).
func (n *nim) ingestDevicePortConfig() {
	locations, err := ioutil.ReadDir(configDevicePortConfigDir)
	if err != nil {
		// Directory might not exist
		return
	}
	for _, location := range locations {
		if !location.IsDir() {
			n.ingestDevicePortConfigFile(configDevicePortConfigDir,
				runDevicePortConfigDir, location.Name())
		}
	}
}

func (n *nim) ingestDevicePortConfigFile(oldDirname string, newDirname string, name string) {
	filename := path.Join(oldDirname, name)
	n.Log.Noticef("ingestDevicePortConfigFile(%s)", filename)
	b, err := fileutils.ReadWithMaxSize(n.Log, filename, maxReadSize)
	if err != nil {
		n.Log.Errorf("Failed to read file %s: %v", filename, err)
		return
	}
	if len(b) == 0 {
		n.Log.Errorf("Ignore empty file %s", filename)
		return
	}

	var dpc types.DevicePortConfig
	err = json.Unmarshal(b, &dpc)
	if err != nil {
		n.Log.Errorf("Could not parse json data in file %s: %s",
			filename, err)
		return
	}
	dpc.DoSanitize(n.Log, true, false, "", true, true)
	dpc.OriginFile = filename

	// Save New config to file.
	var data []byte
	data, err = json.Marshal(dpc)
	if err != nil {
		n.Log.Fatalf("Failed to json marshall new DevicePortConfig err %s",
			err)
	}
	filename = path.Join(newDirname, name)
	err = fileutils.WriteRename(filename, data)
	if err != nil {
		n.Log.Errorf("Failed to write new DevicePortConfig to %s: %s",
			filename, err)
	}
}

func (n *nim) updateLastResortDPC(reason string) {
	n.Log.Functionf("updateLastResortDPC")
	dpc, err := n.makeLastResortDPC()
	if err != nil {
		n.Log.Error(err)
		return
	}
	if n.lastResort != nil && n.lastResort.MostlyEqual(&dpc) {
		return
	}
	n.Log.Noticef("Updating last-resort DPC, reason: %v", reason)
	if err := n.pubDevicePortConfig.Publish(dpcmanager.LastResortKey, dpc); err != nil {
		n.Log.Errorf("Failed to publish last-resort DPC: %v", err)
		return
	}
	n.lastResort = &dpc
}

func (n *nim) removeLastResortDPC() {
	n.Log.Noticef("removeLastResortDPC")
	if err := n.pubDevicePortConfig.Unpublish(dpcmanager.LastResortKey); err != nil {
		n.Log.Errorf("Failed to un-publish last-resort DPC: %v", err)
		return
	}
	n.lastResort = nil
}

func (n *nim) makeLastResortDPC() (types.DevicePortConfig, error) {
	config := types.DevicePortConfig{}
	config.Key = dpcmanager.LastResortKey
	config.Version = types.DPCIsMgmt
	// Set to higher than all zero but lower than the hardware model derived one above
	config.TimePriority = time.Unix(0, 0)
	ifNames, err := n.networkMonitor.ListInterfaces()
	if err != nil {
		err = fmt.Errorf("makeLastResortDPC: Failed to list interfaces: %v", err)
		return config, err
	}
	for _, ifName := range ifNames {
		ifIndex, _, err := n.networkMonitor.GetInterfaceIndex(ifName)
		if err != nil {
			n.Log.Errorf("makeLastResortDPC: failed to get interface index: %v", err)
			continue
		}
		ifAttrs, err := n.networkMonitor.GetInterfaceAttrs(ifIndex)
		if err != nil {
			n.Log.Errorf("makeLastResortDPC: failed to get interface attrs: %v", err)
			continue
		}
		if !n.includeLastResortPort(ifAttrs) {
			continue
		}
		port := types.NetworkPortConfig{
			IfName:       ifName,
			Phylabel:     ifName,
			Logicallabel: ifName,
			IsMgmt:       true,
			IsL3Port:     true,
			DhcpConfig: types.DhcpConfig{
				Dhcp: types.DT_CLIENT,
			},
		}
		dns := n.dpcManager.GetDNS()
		portStatus := dns.GetPortByIfName(ifName)
		if portStatus != nil {
			port.WirelessCfg = portStatus.WirelessCfg
		}
		config.Ports = append(config.Ports, port)
	}
	return config, nil
}

func (n *nim) includeLastResortPort(ifAttrs netmonitor.IfAttrs) bool {
	ifName := ifAttrs.IfName
	exclude := strings.HasPrefix(ifName, "vif") ||
		strings.HasPrefix(ifName, "nbu") ||
		strings.HasPrefix(ifName, "nbo") ||
		strings.HasPrefix(ifName, "keth")
	if exclude {
		return false
	}
	if n.isInterfaceAssigned(ifName) {
		return false
	}
	if ifAttrs.IsLoopback || !ifAttrs.WithBroadcast || ifAttrs.Enslaved {
		return false
	}
	if ifAttrs.IfType == "device" {
		return true
	}
	if ifAttrs.IfType == "bridge" {
		// Was this originally an ethernet interface turned into a bridge?
		_, exists, _ := n.networkMonitor.GetInterfaceIndex("k" + ifName)
		return exists
	}
	return false
}

func (n *nim) isInterfaceAssigned(ifName string) bool {
	ib := n.assignableAdapters.LookupIoBundleIfName(ifName)
	if ib == nil {
		return false
	}
	n.Log.Tracef("isAssigned(%s): pciback %t, used %s",
		ifName, ib.IsPCIBack, ib.UsedByUUID.String())
	if ib.UsedByUUID != nilUUID {
		return true
	}
	return false
}

func (n *nim) processInterfaceChange(ifChange netmonitor.IfChange) {
	if !n.enabledLastResort || n.lastResort == nil {
		return
	}
	includePort := n.includeLastResortPort(ifChange.Attrs)
	port := n.lastResort.GetPortByIfName(ifChange.Attrs.IfName)
	if port == nil && includePort {
		n.updateLastResortDPC(fmt.Sprintf("interface %s should be included",
			ifChange.Attrs.IfName))
	}
}
