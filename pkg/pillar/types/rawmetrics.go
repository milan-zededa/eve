package types

import "time"

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
	// You can add timestamps or deltas if you sample periodically.
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
	// Apps: you will usually fill this from your orchestrator,
	// not from this collector. Left here for completeness.
	Apps []AppUsage `json:"apps,omitempty"`
}
