package main

import (
	"fmt"
	"time"
)

// eccErrorRatePerHour computes the corrected ECC error rate per hour
// by comparing the current ECCModule against the most recent older
// snapshot in history.
func eccErrorRatePerHour(current ECCModule, currentTs time.Time, history []NodeRawMetrics) float64 {
	if len(history) == 0 || currentTs.IsZero() {
		return 0
	}

	id := labelOrSlot(current)

	// Walk history backwards to find the latest earlier snapshot for this DIMM.
	for i := len(history) - 1; i >= 0; i-- {
		prev := history[i]

		// Ignore snapshots without timestamps or newer timestamps.
		if prev.CollectedAt.IsZero() || !prev.CollectedAt.Before(currentTs) {
			continue
		}

		// Find matching ECC module in that snapshot.
		for _, prevM := range prev.ECCModules {
			if labelOrSlot(prevM) == id {
				// Counter overflow or reset: no valid rate
				if current.CECount <= prevM.CECount {
					return 0
				}
				delta := float64(current.CECount - prevM.CECount)
				dtSec := currentTs.Sub(prev.CollectedAt).Seconds()
				if dtSec <= 0 {
					return 0
				}
				return delta / dtSec * 3600.0 // convert to errors/hour
			}
		}
	}

	return 0
}

func analyzeMemory(raw NodeRawMetrics, history []NodeRawMetrics) (MemoryComponent, []MainIssue, []ReplacementPriority, int) {
	memComp := MemoryComponent{Status: "ok"}
	var memIssues []MemoryIssue
	var mainIssues []MainIssue
	var memReplacement []ReplacementPriority
	healthDelta := 0

	for _, m := range raw.ECCModules {
		highCorrected := m.CECount > 100
		hasUE := m.UECount > 0

		if highCorrected || hasUE {
			severity := "medium"
			if hasUE {
				severity = "high"
			}

			ratePerHour := eccErrorRatePerHour(m, raw.CollectedAt, history)

			issue := MemoryIssue{
				Type:                      "ecc_errors",
				Severity:                  severity,
				CorrectedErrorRatePerHour: ratePerHour,
				UncorrectedErrorsLast24h:  m.UECount,
				Detail:                    fmt.Sprintf("ECC error count on %s is above the safety threshold.", labelOrSlot(m)),
				AffectedModules: []MemoryAffectedModule{
					{
						Label:             m.Label,
						Slot:              m.Slot,
						RecommendedAction: "Replace this DIMM as soon as possible.",
					},
				},
				SuggestedActions: []string{
					fmt.Sprintf("Schedule replacement of DIMM %s in the next maintenance window.", labelOrSlot(m)),
					"Avoid deploying new memory-critical workloads before replacement.",
				},
			}
			memIssues = append(memIssues, issue)
			memComp.Status = "critical"

			mainIssues = append(mainIssues, MainIssue{
				Title:         fmt.Sprintf("High ECC error rate on memory %s", labelOrSlot(m)),
				Detail:        "ECC error rate is above the safety threshold. Uncorrected errors significantly increase risk of data corruption.",
				Severity:      "critical",
				ComponentType: "memory",
				ComponentIDs:  []string{labelOrSlot(m)},
				SuggestedActions: []string{
					fmt.Sprintf("Schedule replacement of DIMM %s in the next maintenance window.", labelOrSlot(m)),
					"Avoid deploying new memory-intensive workloads on this node until the DIMM is replaced.",
					"Monitor ECC error counters more frequently until replacement is completed.",
				},
			})

			memReplacement = append(memReplacement, ReplacementPriority{
				ComponentType: "memory_dimm",
				ID:            labelOrSlot(m),
				Priority:      "1",
				Reason:        "ECC error count above threshold - high risk for data corruption.",
			})

			healthDelta += 30
		}
	}

	memComp.Issues = memIssues
	return memComp, mainIssues, memReplacement, healthDelta
}

func analyzeStorage(raw NodeRawMetrics, history []NodeRawMetrics) (StorageComponent, []MainIssue, []ReplacementPriority, int) {
	storageComp := StorageComponent{Status: "ok"}
	var storageDevices []StorageDeviceIssue
	var mainIssues []MainIssue
	var diskReplacement []ReplacementPriority
	healthDelta := 0

	for _, s := range raw.Smart {
		severity := "ok"
		needsIssue := false

		realloc := ptrInt64(s.ReallocatedSectors)
		pending := ptrInt64(s.PendingSectors)
		crc := ptrInt64(s.CRCErrors)
		wear := ptrInt64(s.WearLevelPercent)

		if realloc > 0 || pending > 0 || crc > 0 || wear >= 80 {
			needsIssue = true
			severity = "medium"
		}

		if needsIssue {
			storageComp.Status = "warning"

			smartMap := map[string]interface{}{
				"reallocated_sectors": realloc,
				"pending_sectors":     pending,
				"crc_errors":          crc,
				"wear_level_percent":  wear,
			}

			issue := StorageSmartIssue{
				Type:              "smart_health",
				Severity:          severity,
				SmartIndicators:   smartMap,
				Detail:            "SMART attributes indicate early degradation.",
				RecommendedAction: fmt.Sprintf("Plan disk %s replacement during the next maintenance window.", s.Device),
			}

			deviceIssue := StorageDeviceIssue{
				DeviceID: s.Device,
				Role:     "system_storage", // TODO: refine based on topology
				Status:   "warning",
				Issues:   []StorageSmartIssue{issue},
			}

			storageDevices = append(storageDevices, deviceIssue)

			mainIssues = append(mainIssues, MainIssue{
				Title:         fmt.Sprintf("SMART degradation on disk %s", s.Device),
				Detail:        "Disk shows reallocated and/or pending sectors, indicating early media degradation.",
				Severity:      "warning",
				ComponentType: "storage",
				ComponentIDs:  []string{s.Device},
				SuggestedActions: []string{
					fmt.Sprintf("Plan disk %s replacement during the next maintenance window.", s.Device),
					"Ensure recent backups or replicas exist for any critical data on this disk.",
					"Avoid placing new write-heavy workloads on this node until the disk is replaced.",
				},
			})

			diskReplacement = append(diskReplacement, ReplacementPriority{
				ComponentType: "disk",
				ID:            s.Device,
				Priority:      "2",
				Reason:        "SMART values indicate early disk degradation - schedule replacement.",
			})

			healthDelta += 20
		}
	}

	storageComp.Devices = storageDevices
	return storageComp, mainIssues, diskReplacement, healthDelta
}

func analyzeCPU(raw NodeRawMetrics, history []NodeRawMetrics) (CPUComponent, []MainIssue, int) {
	cpuComp := CPUComponent{Status: "ok"}
	var cpuIssues []CPUIssue
	var mainIssues []MainIssue
	healthDelta := 0

	totalThrottle := raw.CPUThrottling.CoreThrottleCount + raw.CPUThrottling.PackageThrottleCount
	if totalThrottle > 0 {
		cpuComp.Status = "warning"
		issue := CPUIssue{
			Type:     "thermal_throttling",
			Severity: "medium",
			Detail:   "CPU frequency has been capped due to high temperature.",
			SuggestedActions: []string{
				"Check cooling (fans, airflow, dust).",
				"Reduce CPU load by migrating at least one high-CPU app.",
				"Avoid onboarding new CPU-heavy apps on this node.",
			},
		}
		cpuIssues = append(cpuIssues, issue)

		mainIssues = append(mainIssues, MainIssue{
			Title:         "CPU thermal throttling under current workload",
			Detail:        "CPU frequency has been capped due to high temperature.",
			Severity:      "medium",
			ComponentType: "cpu",
			ComponentIDs:  []string{"cpu_package_0"},
			SuggestedActions: []string{
				"Inspect cooling (fans, airflow, dust) and verify that all fans operate correctly.",
				"Consider migrating at least one high-CPU application to another node.",
				"Review power/thermal limits if throttling persists after cooling checks.",
			},
		})

		healthDelta += 15
	}

	cpuComp.Issues = cpuIssues
	return cpuComp, mainIssues, healthDelta
}

func analyzeThermal(raw NodeRawMetrics, history []NodeRawMetrics) (ThermalComponent, []MainIssue, int) {
	thermalComp := ThermalComponent{
		Status: "ok",
		Detail: "Thermals appear within normal range.",
	}
	var mainIssues []MainIssue
	healthDelta := 0

	maxTemp := 0.0
	for _, t := range raw.Temperatures {
		if t.TemperatureC > maxTemp {
			maxTemp = t.TemperatureC
		}
	}

	if maxTemp >= 80 {
		thermalComp.Status = "warning"
		thermalComp.Detail = "Chassis temperature and/or CPU package temperature are near upper limits."
		thermalComp.SuggestedActions = []string{
			"Verify rack cooling and airflow.",
			"Check for blocked vents or dust on heatsinks.",
		}
		healthDelta += 10

		mainIssues = append(mainIssues, MainIssue{
			Title:         "High chassis or CPU temperature",
			Detail:        thermalComp.Detail,
			Severity:      "warning",
			ComponentType: "thermal",
		})
	}

	return thermalComp, mainIssues, healthDelta
}

func analyzeNetwork(raw NodeRawMetrics, history []NodeRawMetrics) (NetworkComponent, []MainIssue, int) {
	netComp := NetworkComponent{Status: "ok"}
	var netIssues []NetworkIssue
	var mainIssues []MainIssue
	healthDelta := 0

	for _, nic := range raw.Networks {
		totalPkts := nic.TxPkts + nic.RxPkts
		totalErrors := nic.TxErrors + nic.RxErrors
		totalDrops := nic.TxDrops + nic.RxDrops +
			nic.TxACLDrops + nic.RxACLDrops +
			nic.TxACLRateLimitDrops + nic.RxACLRateLimitDrops

		if !nic.Up {
			issue := NetworkIssue{
				Type:      "interface_down",
				Severity:  "critical",
				Interface: nic.IfName,
				Detail:    fmt.Sprintf("Interface %s is reported as down.", nic.IfName),
				SuggestedActions: []string{
					"Check physical link (cable, SFP, switch port).",
					"Verify VLAN/switch configuration and NIC configuration.",
					"If intentionally down, mark this interface as unused in config.",
				},
			}
			netIssues = append(netIssues, issue)
			netComp.Status = "critical"

			mainIssues = append(mainIssues, MainIssue{
				Title:         fmt.Sprintf("Network interface %s is down", nic.IfName),
				Detail:        "Interface is not operational; connectivity may be impacted.",
				Severity:      "critical",
				ComponentType: "network",
				ComponentIDs:  []string{nic.IfName},
				SuggestedActions: []string{
					"Check cabling and switch ports.",
					"Verify that this interface is configured and should be up.",
				},
			})

			healthDelta += 15
			continue
		}

		var errorRatio, dropRatio float64
		if totalPkts > 0 {
			errorRatio = float64(totalErrors) / float64(totalPkts)
			dropRatio = float64(totalDrops) / float64(totalPkts)
		}

		highErrors := totalErrors > 100 || errorRatio > 0.01
		highDrops := totalDrops > 500 || dropRatio > 0.02

		if highErrors || highDrops {
			severity := "warning"
			if errorRatio > 0.05 || dropRatio > 0.05 {
				severity = "medium"
			}

			detail := fmt.Sprintf(
				"Interface %s shows elevated errors/drops (errors=%d, drops=%d, total_pkts=%d).",
				nic.IfName, totalErrors, totalDrops, totalPkts,
			)

			issue := NetworkIssue{
				Type:      "packet_errors_or_drops",
				Severity:  severity,
				Interface: nic.IfName,
				Detail:    detail,
				SuggestedActions: []string{
					"Inspect cabling and switch port for this interface.",
					"Check for duplex/speed mismatches or faulty hardware.",
					"Consider moving latency-sensitive traffic away from this NIC until stabilized.",
				},
			}
			netIssues = append(netIssues, issue)

			if netComp.Status != "critical" {
				netComp.Status = "warning"
			}

			mainIssues = append(mainIssues, MainIssue{
				Title:         fmt.Sprintf("NIC %s has high error/drop rate", nic.IfName),
				Detail:        detail,
				Severity:      severity,
				ComponentType: "network",
				ComponentIDs:  []string{nic.IfName},
				SuggestedActions: []string{
					"Inspect cabling and switch port.",
					"Review network configuration and physical connectivity.",
				},
			})

			healthDelta += 10
		}
	}

	netComp.Issues = netIssues
	return netComp, mainIssues, healthDelta
}
func analyzePSU(raw NodeRawMetrics, history []NodeRawMetrics) (PSUComponent, []MainIssue, int) {
	psuComp := PSUComponent{
		Status: "ok",
		Detail: "PSU health and fan speeds are within normal range.",
	}
	// Currently no hard rules; hook for future/ML.
	return psuComp, nil, 0
}

func analyzeApps(raw NodeRawMetrics, cpu CPUComponent, mem MemoryComponent, storage StorageComponent) ([]AppToMigrate, []AppSafeToStay) {
	var appsToMigrate []AppToMigrate
	var appsSafe []AppSafeToStay

	for _, app := range raw.Apps {
		heavyCPU := app.CPUPercent > 70
		heavyMem := app.MemoryGB > 8
		writeHeavy := app.IOProfile == "high_write"

		reasons := []string{}
		if heavyCPU && cpu.Status == "warning" {
			reasons = append(reasons, "High sustained CPU usage contributes to thermal throttling.")
		}
		if heavyMem && mem.Status == "critical" {
			reasons = append(reasons, "High memory footprint on a node with ECC problems.")
		}
		if writeHeavy && storage.Status == "warning" {
			reasons = append(reasons, "Write-heavy workload on a disk with SMART degradation.")
		}

		if len(reasons) > 0 {
			priority := "medium"
			if mem.Status == "critical" || heavyCPU {
				priority = "high"
			}
			targetReq := AppPlacementTargetReq{}
			if heavyCPU {
				targetReq.MinCPUCores = 16
			}
			if heavyMem {
				targetReq.MinMemoryGB = 32
			}
			if writeHeavy {
				targetReq.StorageType = "healthy_ssd_or_nvme"
			}
			appsToMigrate = append(appsToMigrate, AppToMigrate{
				AppID:   app.AppID,
				AppName: app.AppName,
				CurrentUsage: AppPlacementCurrentUsage{
					CPUPercent: app.CPUPercent,
					MemoryGB:   app.MemoryGB,
					IOProfile:  app.IOProfile,
				},
				Reasons:                reasons,
				TargetNodeRequirements: targetReq,
				Priority:               priority,
			})
		} else {
			appsSafe = append(appsSafe, AppSafeToStay{
				AppID:   app.AppID,
				AppName: app.AppName,
				Reason:  "Low resource footprint; safe to keep on this node.",
			})
		}
	}

	return appsToMigrate, appsSafe
}

func evaluateSafeToDeploy(cpu CPUComponent, mem MemoryComponent, storage StorageComponent) SafeToDeployNewApp {
	safe := SafeToDeployNewApp{
		Status: "yes",
		Reason: "No major hardware issues detected.",
	}
	if mem.Status == "critical" || storage.Status == "warning" || cpu.Status == "warning" {
		safe.Status = "no"
		safe.Reason = "Memory and/or storage and/or CPU issues detected. New workloads are not recommended until hardware is fixed or load is reduced."
	}
	return safe
}
func buildReplacementPlan(all []ReplacementPriority) HardwareReplacementRecommendations {
	overallComment := "No immediate hardware replacement needed."
	if len(all) > 0 {
		overallComment = "Focus first on memory stability, then replace degraded disks. After that, reassess whether CPU throttling persists."
	}
	return HardwareReplacementRecommendations{
		PriorityOrder:  all,
		OverallComment: overallComment,
	}
}
