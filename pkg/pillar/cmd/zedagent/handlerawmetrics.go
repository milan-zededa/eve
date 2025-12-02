package zedagent

import (
	"bytes"
	"encoding/json"
	"io/fs"
	"io/ioutil"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/lf-edge/eve/pkg/pillar/types"
)

func publishNodeRawMetrics(ctx *zedagentContext) {
	nodeID := hostnameOrDefault()
	ecc := collectECC()
	smart := []types.SmartIndicators{
		// Add more devices if needed, or detect via /sys/block
		collectSMART("/dev/sda"),
	}
	temps := collectThermalZones()
	throttling := collectCPUThrottling()
	psu := collectPSUSensors()
	metrics := types.NodeRawMetrics{
		NodeID:        nodeID,
		CollectedAt:   time.Now().UTC(),
		ECCModules:    ecc,
		Smart:         smart,
		Temperatures:  temps,
		CPUThrottling: throttling,
		PSUSensors:    psu,
		Apps:          nil, // filled externally
	}
	ctx.pubNodeRawMetrics.Publish("global", metrics)
}

func hostnameOrDefault() string {
	h, err := os.Hostname()
	if err != nil || h == "" {
		return "unknown-node"
	}
	return h
}

func collectECC() []types.ECCModule {
	base := "/sys/devices/system/edac/mc"
	var result []types.ECCModule
	mcDirs, err := ioutil.ReadDir(base)
	if err != nil {
		// EDAC might not be enabled
		return result
	}
	for _, mc := range mcDirs {
		if !mc.IsDir() || !strings.HasPrefix(mc.Name(), "mc") {
			continue
		}
		mcPath := filepath.Join(base, mc.Name())
		// Many drivers expose per-csrow / per-dimm info in subdirs
		filepath.WalkDir(mcPath, func(path string, d fs.DirEntry, walkErr error) error {
			if walkErr != nil {
				return nil
			}
			if !d.IsDir() {
				return nil
			}
			// look for ce_count/ue_count in this dir
			cePath := filepath.Join(path, "ce_count")
			uePath := filepath.Join(path, "ue_count")
			if !exists(cePath) && !exists(uePath) {
				return nil
			}
			ce := readUint(cePath)
			ue := readUint(uePath)
			label := readTrimmed(filepath.Join(path, "dimm_label"))
			if label == "" {
				label = readTrimmed(filepath.Join(path, "label"))
			}
			// slot: last path element if it looks like csrowX or dimmY
			_, slot := filepath.Split(path)
			mod := types.ECCModule{
				Controller: mc.Name(),
				Label:      label,
				Slot:       slot,
				CECount:    ce,
				UECount:    ue,
			}
			result = append(result, mod)
			return nil
		})
	}
	return result
}

func collectSMART(dev string) types.SmartIndicators {
	// Requires: smartmontools (smartctl)
	cmd := exec.Command("smartctl", "-A", "-j", dev)
	var out bytes.Buffer
	cmd.Stdout = &out
	cmd.Stderr = &out
	err := cmd.Run()
	if err != nil {
		log.Errorf("smartctl failed for %s: %v, output: %s", dev, err, out.String())
		return types.SmartIndicators{Device: dev}
	}
	var raw map[string]any
	if err := json.Unmarshal(out.Bytes(), &raw); err != nil {
		log.Errorf("failed to parse smartctl JSON for %s: %v", dev, err)
		return types.SmartIndicators{Device: dev}
	}
	attrMap := extractSmartAttributes(raw)
	ind := types.SmartIndicators{
		Device:              dev,
		ReallocatedSectors:  attrMap["Reallocated_Sector_Ct"],
		PendingSectors:      attrMap["Current_Pending_Sector"],
		CRCErrors:           attrMap["UDMA_CRC_Error_Count"],
		WearLevelPercent:    findWearLevel(attrMap),
		RawAttributesSource: attrMap, // optional, you can drop this later
	}
	return ind
}

// extractSmartAttributes parses smartctl JSON and returns a map[name]*value.
func extractSmartAttributes(raw map[string]any) map[string]*int64 {
	result := make(map[string]*int64)
	attrs, ok := raw["ata_smart_attributes"].(map[string]any)
	if !ok {
		return result
	}
	tbl, ok := attrs["table"].([]any)
	if !ok {
		return result
	}
	for _, e := range tbl {
		entry, ok := e.(map[string]any)
		if !ok {
			continue
		}
		name, _ := entry["name"].(string)
		rawVal := int64(0)
		if rawField, ok := entry["raw"].(map[string]any); ok {
			if val, ok2 := rawField["value"].(float64); ok2 {
				rawVal = int64(val)
			}
		}
		// copy to avoid address of loop var
		v := rawVal
		result[name] = &v
	}
	return result
}

// Wear level is very vendor-specific, so we just try a few common names.
func findWearLevel(attr map[string]*int64) *int64 {
	names := []string{
		"Wear_Leveling_Count",
		"Media_Wearout_Indicator",
		"Percent_Lifetime_Remain",
	}
	for _, n := range names {
		if v, ok := attr[n]; ok {
			return v
		}
	}
	return nil
}

func collectThermalZones() []types.TemperatureSensor {
	base := "/sys/class/thermal"
	dirs, err := ioutil.ReadDir(base)
	if err != nil {
		return nil
	}
	var sensors []types.TemperatureSensor
	for _, d := range dirs {
		if !d.IsDir() || !strings.HasPrefix(d.Name(), "thermal_zone") {
			continue
		}
		zonePath := filepath.Join(base, d.Name())
		typ := readTrimmed(filepath.Join(zonePath, "type"))
		tempMilli := readInt(filepath.Join(zonePath, "temp"))
		if tempMilli == 0 {
			continue
		}
		sensors = append(sensors, types.TemperatureSensor{
			Name:         d.Name(),
			Location:     typ,
			TemperatureC: float64(tempMilli) / 1000.0,
		})
	}
	return sensors
}

func collectCPUThrottling() types.CPUThrottling {
	base := "/sys/devices/system/cpu"
	dirs, err := ioutil.ReadDir(base)
	if err != nil {
		return types.CPUThrottling{}
	}
	var coreSum, pkgSum uint64
	for _, d := range dirs {
		if !d.IsDir() || !strings.HasPrefix(d.Name(), "cpu") {
			continue
		}
		cpuPath := filepath.Join(base, d.Name(), "thermal_throttle")
		if !exists(cpuPath) {
			continue
		}
		coreSum += readUint(filepath.Join(cpuPath, "core_throttle_count"))
		pkgSum += readUint(filepath.Join(cpuPath, "package_throttle_count"))
	}
	return types.CPUThrottling{
		CoreThrottleCount:    coreSum,
		PackageThrottleCount: pkgSum,
	}
}

func collectPSUSensors() []types.PSUSensor {
	// Requires: lm-sensors (sensors -j)
	cmd := exec.Command("sensors", "-j")
	var out bytes.Buffer
	cmd.Stdout = &out
	cmd.Stderr = &out
	if err := cmd.Run(); err != nil {
		log.Errorf("sensors -j failed: %v", err)
		return nil
	}
	var raw map[string]map[string]map[string]any
	if err := json.Unmarshal(out.Bytes(), &raw); err != nil {
		log.Errorf("failed to parse sensors JSON: %v", err)
		return nil
	}
	var result []types.PSUSensor
	for chip, chipData := range raw {
		for feature, featureData := range chipData {
			// crude filter: pick entries that have "psu" in their name
			if !strings.Contains(strings.ToLower(feature), "psu") {
				continue
			}
			for key, val := range featureData {
				fv, ok := val.(float64)
				if !ok {
					continue
				}
				unit := ""
				switch {
				case strings.Contains(key, "_input"):
					unit = "V/A/W/C" // unknown, you can refine by mapping
				}
				result = append(result, types.PSUSensor{
					Name:         feature,
					Metric:       key,
					Value:        fv,
					Unit:         unit,
					OriginalChip: chip,
				})
			}
		}
	}
	return result
}

// helpers
func exists(path string) bool {
	_, err := os.Stat(path)
	return err == nil
}

func readTrimmed(path string) string {
	b, err := ioutil.ReadFile(path)
	if err != nil {
		return ""
	}
	return strings.TrimSpace(string(b))
}

func readInt(path string) int64 {
	b, err := ioutil.ReadFile(path)
	if err != nil {
		return 0
	}
	v, err := strconv.ParseInt(strings.TrimSpace(string(b)), 10, 64)
	if err != nil {
		return 0
	}
	return v
}

func readUint(path string) uint64 {
	b, err := ioutil.ReadFile(path)
	if err != nil {
		return 0
	}
	v, err := strconv.ParseUint(strings.TrimSpace(string(b)), 10, 64)
	if err != nil {
		return 0
	}
	return v
}
