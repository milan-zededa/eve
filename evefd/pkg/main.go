package main

import (
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"time"
)

const (
	metadataSrvAddr        = "http://169.254.169.254"
	netMetricsEndpoint     = "/eve/v1/networks/metrics.json"
	nodeRawMetricsEndpoint = "/eve/v1/noderawmetrics/metrics.json"
)

type NetworkMetrics struct {
	IfName              string `json:"IfName"`
	Up                  bool   `json:"Up"`
	TxBytes             uint64 `json:"TxBytes"`
	RxBytes             uint64 `json:"RxBytes"`
	TxDrops             uint64 `json:"TxDrops"`
	RxDrops             uint64 `json:"RxDrops"`
	TxPkts              uint64 `json:"TxPkts"`
	RxPkts              uint64 `json:"RxPkts"`
	TxErrors            uint64 `json:"TxErrors"`
	RxErrors            uint64 `json:"RxErrors"`
	TxACLDrops          uint64 `json:"TxACLDrops"`
	RxACLDrops          uint64 `json:"RxACLDrops"`
	TxACLRateLimitDrops uint64 `json:"TxACLRateLimitDrops"`
	RxACLRateLimitDrops uint64 `json:"RxACLRateLimitDrops"`
}

// ======================= INPUT TYPES (YOUR METRICS) ========================

type ECCModule struct {
	Controller string `json:"controller"` // e.g. mc0
	Label      string `json:"label"`      // DIMM label if available
	Slot       string `json:"slot"`       // slot/csrow index, best-effort
	CECount    uint64 `json:"ce_count"`
	UECount    uint64 `json:"ue_count"`
}

type SmartIndicators struct {
	Device              string `json:"device"` // e.g. /dev/sda
	ReallocatedSectors  *int64 `json:"reallocated_sectors,omitempty"`
	PendingSectors      *int64 `json:"pending_sectors,omitempty"`
	CRCErrors           *int64 `json:"crc_errors,omitempty"`
	WearLevelPercent    *int64 `json:"wear_level_percent,omitempty"`
	RawAttributesSource any    `json:"raw_attributes,omitempty"` // optional, for debugging
}

type TemperatureSensor struct {
	Name         string  `json:"name"`
	Location     string  `json:"location"`
	TemperatureC float64 `json:"temperature_c"`
}

type CPUThrottling struct {
	CoreThrottleCount    uint64 `json:"core_throttle_count"`
	PackageThrottleCount uint64 `json:"package_throttle_count"`
}

type PSUSensor struct {
	Name         string  `json:"name"`
	Metric       string  `json:"metric"`
	Value        float64 `json:"value"`
	Unit         string  `json:"unit"`
	OriginalChip string  `json:"original_chip,omitempty"`
}

type AppUsage struct {
	AppID      string  `json:"app_id"`
	AppName    string  `json:"app_name"`
	CPUPercent float64 `json:"cpu_percent"`
	MemoryGB   float64 `json:"memory_gb"`
	IOProfile  string  `json:"io_profile,omitempty"` // e.g. high_read, high_write, mixed, low
}

type NodeRawMetrics struct {
	NodeID        string              `json:"node_id"`
	CollectedAt   time.Time           `json:"collected_at"`
	ECCModules    []ECCModule         `json:"ecc_modules,omitempty"`
	Smart         []SmartIndicators   `json:"smart,omitempty"`
	Temperatures  []TemperatureSensor `json:"temperatures,omitempty"`
	CPUThrottling CPUThrottling       `json:"cpu_throttling"`
	PSUSensors    []PSUSensor         `json:"psu_sensors,omitempty"`
	Apps          []AppUsage          `json:"apps,omitempty"`
	Networks      []NetworkMetrics    `json:"networks,omitempty"`
}

// ======================= OUTPUT TYPES (HEALTH REPORT) ======================

type MainIssue struct {
	Title            string   `json:"title"`
	Detail           string   `json:"detail"`
	Severity         string   `json:"severity"` // critical, warning, medium, low
	ComponentType    string   `json:"component_type"`
	ComponentIDs     []string `json:"component_ids,omitempty"`
	SuggestedActions []string `json:"suggested_actions,omitempty"`
}

type Summary struct {
	HealthScore int         `json:"health_score"`
	StatusLabel string      `json:"status_label"`
	MainIssues  []MainIssue `json:"main_issues,omitempty"`
}

type CPUIssue struct {
	Type             string   `json:"type"`
	Severity         string   `json:"severity"`
	Detail           string   `json:"detail"`
	SuggestedActions []string `json:"suggested_actions,omitempty"`
}

type CPUComponent struct {
	Status string     `json:"status"`
	Issues []CPUIssue `json:"issues,omitempty"`
}

type MemoryAffectedModule struct {
	Label             string `json:"label"`
	Slot              string `json:"slot"`
	RecommendedAction string `json:"recommended_action"`
}

type MemoryIssue struct {
	Type                      string                 `json:"type"`
	Severity                  string                 `json:"severity"`
	CorrectedErrorRatePerHour float64                `json:"corrected_error_rate_per_hour,omitempty"`
	UncorrectedErrorsLast24h  uint64                 `json:"uncorrected_errors_last_24h,omitempty"`
	Detail                    string                 `json:"detail"`
	AffectedModules           []MemoryAffectedModule `json:"affected_modules,omitempty"`
	SuggestedActions          []string               `json:"suggested_actions,omitempty"`
}

type MemoryComponent struct {
	Status string        `json:"status"`
	Issues []MemoryIssue `json:"issues,omitempty"`
}

type StorageSmartIssue struct {
	Type              string                 `json:"type"`
	Severity          string                 `json:"severity"`
	SmartIndicators   map[string]interface{} `json:"smart_indicators,omitempty"`
	Detail            string                 `json:"detail"`
	RecommendedAction string                 `json:"recommended_action"`
}

type StorageDeviceIssue struct {
	DeviceID string              `json:"device_id"`
	Role     string              `json:"role"`
	Status   string              `json:"status"`
	Issues   []StorageSmartIssue `json:"issues,omitempty"`
}

type StorageComponent struct {
	Status  string               `json:"status"`
	Devices []StorageDeviceIssue `json:"devices,omitempty"`
}

type NetworkIssue struct {
	Type             string   `json:"type"`
	Severity         string   `json:"severity"`
	Interface        string   `json:"interface"`
	Detail           string   `json:"detail"`
	SuggestedActions []string `json:"suggested_actions,omitempty"`
}

type NetworkComponent struct {
	Status string         `json:"status"`
	Issues []NetworkIssue `json:"issues,omitempty"`
}

type PSUComponent struct {
	Status string `json:"status"`
	Detail string `json:"detail"`
}

type ThermalComponent struct {
	Status           string   `json:"status"`
	Detail           string   `json:"detail"`
	SuggestedActions []string `json:"suggested_actions,omitempty"`
}

type Components struct {
	CPU     CPUComponent     `json:"cpu"`
	Memory  MemoryComponent  `json:"memory"`
	Storage StorageComponent `json:"storage"`
	PSU     PSUComponent     `json:"psu"`
	Thermal ThermalComponent `json:"thermal"`
	Network NetworkComponent `json:"network"`
}

type SafeToDeployNewApp struct {
	Status string `json:"status"` // yes / no / maybe
	Reason string `json:"reason"`
}

type AppPlacementCurrentUsage struct {
	CPUPercent float64 `json:"cpu_percent"`
	MemoryGB   float64 `json:"memory_gb"`
	IOProfile  string  `json:"io_profile,omitempty"`
}

type AppPlacementTargetReq struct {
	MinCPUCores  int     `json:"min_cpu_cores,omitempty"`
	MinMemoryGB  float64 `json:"min_memory_gb,omitempty"`
	PreferredGPU bool    `json:"preferred_gpu,omitempty"`
	StorageType  string  `json:"storage_type,omitempty"`
}

type AppToMigrate struct {
	AppID                  string                   `json:"app_id"`
	AppName                string                   `json:"app_name"`
	CurrentUsage           AppPlacementCurrentUsage `json:"current_usage"`
	Reasons                []string                 `json:"reasons"`
	TargetNodeRequirements AppPlacementTargetReq    `json:"target_node_requirements"`
	Priority               string                   `json:"priority"`
}

type AppSafeToStay struct {
	AppID   string `json:"app_id"`
	AppName string `json:"app_name"`
	Reason  string `json:"reason"`
}

type ReplacementPriority struct {
	ComponentType string `json:"component_type"`
	ID            string `json:"id"`
	Priority      string `json:"priority"`
	Reason        string `json:"reason"`
}

type HardwareReplacementRecommendations struct {
	PriorityOrder  []ReplacementPriority `json:"priority_order"`
	OverallComment string                `json:"overall_comment"`
}

type NodeHealthReport struct {
	NodeID                             string                             `json:"node_id"`
	GeneratedAt                        time.Time                          `json:"generated_at"`
	OverallStatus                      string                             `json:"overall_status"` // ok, warning, critical
	Summary                            Summary                            `json:"summary"`
	Components                         Components                         `json:"components"`
	SafeToDeployNewApp                 SafeToDeployNewApp                 `json:"safe_to_deploy_new_app"`
	AppsToMigrate                      []AppToMigrate                     `json:"apps_to_migrate"`
	AppsSafeToStay                     []AppSafeToStay                    `json:"apps_safe_to_stay"`
	HardwareReplacementRecommendations HardwareReplacementRecommendations `json:"hardware_replacement_recommendations"`
}

// =========================== SIMPLE HEURISTICS =============================

func AnalyzeNode(raw NodeRawMetrics) NodeHealthReport {
	report := NodeHealthReport{
		NodeID:        raw.NodeID,
		GeneratedAt:   time.Now().UTC(),
		OverallStatus: "ok",
	}

	var mainIssues []MainIssue
	healthScore := 100

	// ------------------ MEMORY / ECC ---------------------------------------
	memComp := MemoryComponent{Status: "ok"}
	var memIssues []MemoryIssue
	var memReplacement []ReplacementPriority

	for _, m := range raw.ECCModules {
		// Simple thresholds (you can refine later)
		highCorrected := m.CECount > 100
		hasUE := m.UECount > 0

		if highCorrected || hasUE {
			severity := "medium"
			if hasUE {
				severity = "high"
			}
			issue := MemoryIssue{
				Type:                      "ecc_errors",
				Severity:                  severity,
				CorrectedErrorRatePerHour: 0, // we don't have rate yet, just counts
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

			healthScore -= 30
		}
	}
	if memComp.Status == "ok" {
		memComp.Status = "ok"
	}
	memComp.Issues = memIssues

	// ------------------- STORAGE / SMART ------------------------------------
	storageComp := StorageComponent{Status: "ok"}
	var storageDevices []StorageDeviceIssue
	var diskReplacement []ReplacementPriority

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
				Role:     "system_storage", // you can refine this from topology info
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

			healthScore -= 20
		}
	}
	storageComp.Devices = storageDevices

	// ------------------- CPU / THROTTLING -----------------------------------
	cpuComp := CPUComponent{Status: "ok"}
	var cpuIssues []CPUIssue

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

		healthScore -= 15
	}
	cpuComp.Issues = cpuIssues

	// ------------------- THERMALS -------------------------------------------
	thermalComp := ThermalComponent{
		Status: "ok",
		Detail: "Thermals appear within normal range.",
	}
	maxTemp := 0.0
	for _, t := range raw.Temperatures {
		if t.TemperatureC > maxTemp {
			maxTemp = t.TemperatureC
		}
	}
	if maxTemp >= 80 { // simple threshold
		thermalComp.Status = "warning"
		thermalComp.Detail = "Chassis temperature and/or CPU package temperature are near upper limits."
		thermalComp.SuggestedActions = []string{
			"Verify rack cooling and airflow.",
			"Check for blocked vents or dust on heatsinks.",
		}
		if healthScore > 0 {
			healthScore -= 10
		}
	}

	// ------------------- NETWORK / NICS -------------------------------------
	netComp := NetworkComponent{Status: "ok"}
	var netIssues []NetworkIssue

	for _, nic := range raw.Networks {
		totalPkts := nic.TxPkts + nic.RxPkts
		totalErrors := nic.TxErrors + nic.RxErrors
		totalDrops := nic.TxDrops + nic.RxDrops +
			nic.TxACLDrops + nic.RxACLDrops +
			nic.TxACLRateLimitDrops + nic.RxACLRateLimitDrops

		// 1) Interface down
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

			healthScore -= 15
			continue
		}

		// 2) Errors / drops
		var errorRatio, dropRatio float64
		if totalPkts > 0 {
			errorRatio = float64(totalErrors) / float64(totalPkts)
			dropRatio = float64(totalDrops) / float64(totalPkts)
		}

		// thresholds: tweak to taste
		highErrors := totalErrors > 100 || errorRatio > 0.01 // >1% errors
		highDrops := totalDrops > 500 || dropRatio > 0.02    // >2% drops

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

			healthScore -= 10
		}
	}
	if netComp.Status == "ok" {
		netComp.Status = "ok"
	}
	netComp.Issues = netIssues

	// ------------------- PSU -------------------------------------------------
	psuComp := PSUComponent{
		Status: "ok",
		Detail: "PSU health and fan speeds are within normal range.",
	}
	// You can add real rules based on PSUSensors here.

	// ------------------- OVERALL STATUS -------------------------------------
	if healthScore < 0 {
		healthScore = 0
	}
	if healthScore > 100 {
		healthScore = 100
	}

	statusLabel := "Healthy"
	overallStatus := "ok"
	if healthScore >= 80 {
		statusLabel = "Healthy"
		overallStatus = "ok"
	} else if healthScore >= 60 {
		statusLabel = "Degraded - action recommended"
		overallStatus = "warning"
	} else {
		statusLabel = "Degraded - action required"
		overallStatus = "critical"
	}
	report.OverallStatus = overallStatus

	report.Summary = Summary{
		HealthScore: healthScore,
		StatusLabel: statusLabel,
		MainIssues:  mainIssues,
	}

	// ------------------- COMPONENT SUMMARY ----------------------------------
	report.Components = Components{
		CPU:     cpuComp,
		Memory:  memComp,
		Storage: storageComp,
		PSU:     psuComp,
		Thermal: thermalComp,
	}

	// ------------------- SAFE TO DEPLOY NEW APPS? ---------------------------
	safe := SafeToDeployNewApp{
		Status: "yes",
		Reason: "No major hardware issues detected.",
	}
	if memComp.Status == "critical" || storageComp.Status == "warning" || cpuComp.Status == "warning" {
		safe.Status = "no"
		safe.Reason = "Memory and/or storage and/or CPU issues detected. New workloads are not recommended until hardware is fixed or load is reduced."
	}
	report.SafeToDeployNewApp = safe

	// ------------------- APP PLACEMENT RECOMMENDATIONS ----------------------
	var appsToMigrate []AppToMigrate
	var appsSafe []AppSafeToStay

	for _, app := range raw.Apps {
		heavyCPU := app.CPUPercent > 70
		heavyMem := app.MemoryGB > 8
		writeHeavy := app.IOProfile == "high_write"

		reasons := []string{}
		if heavyCPU && cpuComp.Status == "warning" {
			reasons = append(reasons, "High sustained CPU usage contributes to thermal throttling.")
		}
		if heavyMem && memComp.Status == "critical" {
			reasons = append(reasons, "High memory footprint on a node with ECC problems.")
		}
		if writeHeavy && storageComp.Status == "warning" {
			reasons = append(reasons, "Write-heavy workload on a disk with SMART degradation.")
		}

		if len(reasons) > 0 {
			priority := "medium"
			if memComp.Status == "critical" || heavyCPU {
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
	report.AppsToMigrate = appsToMigrate
	report.AppsSafeToStay = appsSafe

	// ------------------- HARDWARE REPLACEMENT PRIORITY ----------------------
	var priorityOrder []ReplacementPriority
	priorityOrder = append(priorityOrder, memReplacement...)
	priorityOrder = append(priorityOrder, diskReplacement...)

	overallComment := "No immediate hardware replacement needed."
	if len(priorityOrder) > 0 {
		overallComment = "Focus first on memory stability, then replace degraded disks. After that, reassess whether CPU throttling persists."
	}

	report.HardwareReplacementRecommendations = HardwareReplacementRecommendations{
		PriorityOrder:  priorityOrder,
		OverallComment: overallComment,
	}

	return report
}

func labelOrSlot(m ECCModule) string {
	if m.Label != "" {
		return m.Label
	}
	if m.Slot != "" {
		return m.Slot
	}
	if m.Controller != "" {
		return m.Controller
	}
	return "unknown_dimm"
}

func ptrInt64(p *int64) int64 {
	if p == nil {
		return 0
	}
	return *p
}

func main() {
	client := http.Client{Timeout: 5 * time.Second}

	// Get nework metrics
	url := metadataSrvAddr + netMetricsEndpoint
	resp, err := client.Get(url)
	if err != nil {
		log.Fatalf("Failed to fetch network metrics: %v", err)
	}
	defer resp.Body.Close()

	var netMetrics []NetworkMetrics
	if err := json.NewDecoder(resp.Body).Decode(&netMetrics); err != nil {
		log.Fatalf("Failed to decode network metrics JSON: %v", err)
	}

	// Get node raw metrics
	url = metadataSrvAddr + netMetricsEndpoint
	resp, err = client.Get(url)
	if err != nil {
		log.Fatalf("Failed to fetch network metrics: %v", err)
	}
	defer resp.Body.Close()

	nodeRawMetrics := NodeRawMetrics{}
	if err := json.NewDecoder(resp.Body).Decode(&nodeRawMetrics); err != nil {
		log.Fatalf("Failed to decode network metrics JSON: %v", err)
	}
	nodeRawMetrics.Networks = netMetrics

	fmt.Printf("Node raw metrics: %+v", nodeRawMetrics)
	report := AnalyzeNode(nodeRawMetrics)

	out, err := json.MarshalIndent(report, "", "  ")
	if err != nil {
		panic(err)
	}
	fmt.Println(string(out))
}
