package main

import (
	"bufio"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	"os"
	"sync"
	"time"
)

const (
	metadataSrvAddr          = "http://169.254.169.254"
	netMetricsEndpoint       = "/eve/v1/networks/metrics.json"
	nodeRawMetricsEndpoint   = "/eve/v1/noderawmetrics/metrics.json"
	historyFilePath          = "/var/lib/node-health/history.json" // adjust path as you like
	metricsHistoryMaxSamples = 360                                 //   360 samples ≈ 1 hour of history
)

type MetricsHistory struct {
	mu      sync.RWMutex
	samples []NodeRawMetrics
	max     int
}

func NewMetricsHistory(max int) *MetricsHistory {
	return &MetricsHistory{max: max}
}

var metricsHistory = NewMetricsHistory(metricsHistoryMaxSamples)

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

// =========================== HELPERS =============================

func appendSampleToFile(path string, sample NodeRawMetrics) error {
	f, err := os.OpenFile(path, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o644)
	if err != nil {
		return err
	}
	defer f.Close()

	enc := json.NewEncoder(f)
	return enc.Encode(sample) // writes JSON + newline
}

func startMetricsCollector(interval time.Duration) {
	ticker := time.NewTicker(interval)
	go func() {
		defer ticker.Stop()
		for range ticker.C {
			raw, err := fetchNodeRawMetrics()
			if err != nil {
				log.Printf("metrics collector: failed to fetch metrics: %v", err)
				continue
			}

			// Optional: also push into in-memory history if you kept it
			metricsHistory.Add(raw)

			// Persist to file
			if err := appendSampleToFile(historyFilePath, raw); err != nil {
				log.Printf("metrics collector: failed to write history file: %v", err)
			}
		}
	}()
}

func loadHistoryFromFile(path string, max int) ([]NodeRawMetrics, error) {
	f, err := os.Open(path)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil // no history yet is not an error
		}
		return nil, err
	}
	defer f.Close()

	var history []NodeRawMetrics

	reader := bufio.NewReader(f)
	dec := json.NewDecoder(reader)

	for {
		var s NodeRawMetrics
		if err := dec.Decode(&s); err != nil {
			if err == io.EOF {
				break
			}
			return history, err
		}
		history = append(history, s)
		if max > 0 && len(history) > max {
			// keep only the last `max` elements
			history = history[len(history)-max:]
		}
	}
	return history, nil
}

func (h *MetricsHistory) Add(sample NodeRawMetrics) {
	h.mu.Lock()
	defer h.mu.Unlock()

	h.samples = append(h.samples, sample)

	// Trim to maximum allowed history
	if len(h.samples) > h.max {
		h.samples = h.samples[len(h.samples)-h.max:]
	}
}

func (h *MetricsHistory) Snapshot() []NodeRawMetrics {
	h.mu.RLock()
	defer h.mu.RUnlock()

	out := make([]NodeRawMetrics, len(h.samples))
	copy(out, h.samples)
	return out
}

func (h *MetricsHistory) Last() (NodeRawMetrics, bool) {
	h.mu.RLock()
	defer h.mu.RUnlock()

	if len(h.samples) == 0 {
		return NodeRawMetrics{}, false
	}
	return h.samples[len(h.samples)-1], true
}

// =========================== SIMPLE HEURISTICS =============================

func AnalyzeNode(raw NodeRawMetrics, history []NodeRawMetrics) NodeHealthReport {
	now := time.Now().UTC()

	report := NodeHealthReport{
		NodeID:        raw.NodeID,
		GeneratedAt:   now,
		OverallStatus: "ok", // will be updated
	}

	// Start from 100 and subtract deltas from each component.
	healthScore := 100

	var allMainIssues []MainIssue
	var allReplacement []ReplacementPriority

	// ---- Per-component analyzers ----

	memComp, memIssues, memRepl, memDelta := analyzeMemory(raw, history)
	healthScore -= memDelta
	allMainIssues = append(allMainIssues, memIssues...)
	allReplacement = append(allReplacement, memRepl...)

	storageComp, storageIssues, diskRepl, storageDelta := analyzeStorage(raw, history)
	healthScore -= storageDelta
	allMainIssues = append(allMainIssues, storageIssues...)
	allReplacement = append(allReplacement, diskRepl...)

	cpuComp, cpuIssues, cpuDelta := analyzeCPU(raw, history)
	healthScore -= cpuDelta
	allMainIssues = append(allMainIssues, cpuIssues...)

	thermalComp, thermalIssues, thermalDelta := analyzeThermal(raw, history)
	healthScore -= thermalDelta
	allMainIssues = append(allMainIssues, thermalIssues...)

	netComp, netIssues, netDelta := analyzeNetwork(raw, history)
	healthScore -= netDelta
	allMainIssues = append(allMainIssues, netIssues...)

	psuComp, psuIssues, psuDelta := analyzePSU(raw, history)
	healthScore -= psuDelta
	allMainIssues = append(allMainIssues, psuIssues...)

	// Clamp score
	if healthScore < 0 {
		healthScore = 0
	}
	if healthScore > 100 {
		healthScore = 100
	}

	// Overall status from score (still heuristic; could be ML later).
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
		MainIssues:  allMainIssues,
	}

	// Components
	report.Components = Components{
		CPU:     cpuComp,
		Memory:  memComp,
		Storage: storageComp,
		PSU:     psuComp,
		Thermal: thermalComp,
		Network: netComp,
	}

	// Apps + placement recommendations
	appsToMigrate, appsSafe := analyzeApps(raw, cpuComp, memComp, storageComp)
	report.AppsToMigrate = appsToMigrate
	report.AppsSafeToStay = appsSafe

	// Safe to deploy new app
	report.SafeToDeployNewApp = evaluateSafeToDeploy(cpuComp, memComp, storageComp)

	// Hardware replacement recommendations (mem + disks for now)
	report.HardwareReplacementRecommendations = buildReplacementPlan(allReplacement)

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

// ====================== HTTP SERVER EXTENSION =========================

func fetchNodeRawMetrics() (NodeRawMetrics, error) {
	client := http.Client{
		Timeout: 5 * time.Second,
	}

	// Fetch network metrics
	netResp, err := client.Get(metadataSrvAddr + netMetricsEndpoint)
	if err != nil {
		return NodeRawMetrics{}, fmt.Errorf("fetch network metrics: %w", err)
	}
	defer netResp.Body.Close()

	var networks []NetworkMetrics
	if err := json.NewDecoder(netResp.Body).Decode(&networks); err != nil {
		return NodeRawMetrics{}, fmt.Errorf("decode network metrics: %w", err)
	}

	// Fetch node raw metrics (ECC, Smart, Thermal, CPU throttling...)
	nodeResp, err := client.Get(metadataSrvAddr + nodeRawMetricsEndpoint)
	if err != nil {
		return NodeRawMetrics{}, fmt.Errorf("fetch node raw metrics: %w", err)
	}
	defer nodeResp.Body.Close()

	var raw NodeRawMetrics
	if err := json.NewDecoder(nodeResp.Body).Decode(&raw); err != nil {
		return NodeRawMetrics{}, fmt.Errorf("decode node raw metrics: %w", err)
	}

	// Merge NIC results (EVE exposes them separately)
	raw.Networks = networks

	return raw, nil
}

// GET /health/analyze  — fetch from metadata + analyze
func analyzeLiveHandler(w http.ResponseWriter, r *http.Request) {
	history, err := loadHistoryFromFile(historyFilePath, metricsHistoryMaxSamples)
	if err != nil {
		log.Printf("analyzeLiveHandler: failed to load history: %v", err)
		http.Error(w, "failed to load metrics history", http.StatusInternalServerError)
		return
	}
	if len(history) == 0 {
		http.Error(w, "no metrics history available yet", http.StatusServiceUnavailable)
		return
	}

	raw := history[len(history)-1] // latest snapshot
	report := AnalyzeNode(raw, history)

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(report)
}

// POST /health/analyze — analyze provided JSON input
func analyzeInputHandler(w http.ResponseWriter, r *http.Request) {
	var raw NodeRawMetrics
	if err := json.NewDecoder(r.Body).Decode(&raw); err != nil {
		http.Error(w, "invalid JSON payload", http.StatusBadRequest)
		return
	}
	if raw.CollectedAt.IsZero() {
		raw.CollectedAt = time.Now().UTC()
	}

	// Load historical samples from file
	history, err := loadHistoryFromFile(historyFilePath, metricsHistoryMaxSamples)
	if err != nil {
		log.Printf("analyzeInputHandler: failed to load history: %v", err)
		// Fallback: analyze without history
		history = nil
	}

	report := AnalyzeNode(raw, history)

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(report)
}

func main() {
	mux := http.NewServeMux()

	mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		switch r.Method {
		case http.MethodGet:
			analyzeLiveHandler(w, r)
		case http.MethodPost:
			analyzeInputHandler(w, r)
		default:
			http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		}
	})

	// Start background collector that samples every 10 seconds
	startMetricsCollector(10 * time.Second)

	server := &http.Server{
		Addr:         ":8080",
		Handler:      mux,
		ReadTimeout:  5 * time.Second,
		WriteTimeout: 10 * time.Second,
		IdleTimeout:  30 * time.Second,
	}

	log.Println("Node Health HTTP API started on :8080")
	if err := server.ListenAndServe(); err != nil {
		log.Fatalf("server failed: %v", err)
	}
}
