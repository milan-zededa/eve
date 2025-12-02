package main

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
)

// loadFixture loads a NodeRawMetrics JSON fixture from testdata/.
func loadFixture(t *testing.T, name string) NodeRawMetrics {
	t.Helper()

	path := filepath.Join("testdata", name)
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("failed to read fixture %s: %v", path, err)
	}

	var metrics NodeRawMetrics
	if err := json.Unmarshal(data, &metrics); err != nil {
		t.Fatalf("failed to unmarshal JSON from %s: %v", path, err)
	}
	return metrics
}

func TestAnalyzeNode_HealthyFixture(t *testing.T) {
	metrics := loadFixture(t, "input_healthy.json")

	// No history for this test – analyze current snapshot only.
	report := AnalyzeNode(metrics, nil)

	if report.NodeID != "node-healthy-01" {
		t.Errorf("expected NodeID=node-healthy-01, got %s", report.NodeID)
	}

	// For a healthy node we expect overall status "ok".
	if report.OverallStatus != "ok" {
		t.Errorf("expected overall_status == 'ok', got %s", report.OverallStatus)
	}

	// Health score should be high for a healthy node.
	if report.Summary.HealthScore < 80 {
		t.Errorf("expected HealthScore >= 80, got %d", report.Summary.HealthScore)
	}

	// Should be safe to deploy.
	if report.SafeToDeployNewApp.Status != "yes" {
		t.Errorf("expected safe_to_deploy_new_app.status == 'yes', got %s",
			report.SafeToDeployNewApp.Status)
	}

	// No critical memory issues expected.
	if comp := report.Components.Memory; comp.Status != "" {
		if comp.Status == "critical" || comp.Status == "warning" {
			t.Errorf("expected memory status not degraded for healthy node, got %s", comp.Status)
		}
	}
}

func TestAnalyzeNode_UnhealthyFixture(t *testing.T) {
	metrics := loadFixture(t, "input_unhealthy.json")

	// No history for this test – analyze current snapshot only.
	report := AnalyzeNode(metrics, nil)

	if report.NodeID != "node-bad-01" {
		t.Errorf("expected NodeID=node-bad-01, got %s", report.NodeID)
	}

	// Overall status should NOT be ok for an unhealthy node.
	if report.OverallStatus == "ok" {
		t.Errorf("expected overall_status to be degraded (warning/critical) for unhealthy node, got %s", report.OverallStatus)
	}

	// Health score should be clearly lower for the bad node.
	if report.Summary.HealthScore > 80 {
		t.Errorf("expected HealthScore <= 80 for unhealthy node, got %d", report.Summary.HealthScore)
	}

	// Memory should be at least warning/critical (ECC issues).
	mem := report.Components.Memory
	if mem.Status == "" {
		t.Fatalf("expected 'memory' component in report")
	}
	if mem.Status != "warning" && mem.Status != "critical" {
		t.Errorf("expected memory status warning/critical, got %s", mem.Status)
	}

	// Storage should have a warning because /dev/sda is degraded.
	stor := report.Components.Storage
	if stor.Status == "" {
		t.Fatalf("expected 'storage' component in report")
	}
	if stor.Status != "warning" && stor.Status != "critical" {
		t.Errorf("expected storage status warning/critical, got %s", stor.Status)
	}

	// CPU should at least be warning due to throttling.
	cpu := report.Components.CPU
	if cpu.Status == "" {
		t.Fatalf("expected 'cpu' component in report")
	}
	if cpu.Status != "warning" && cpu.Status != "critical" {
		t.Errorf("expected cpu status warning/critical, got %s", cpu.Status)
	}

	// Network: NIC eth0 has many drops/errors => expect warning or worse.
	netComp := report.Components.Network
	if netComp.Status == "" {
		t.Fatalf("expected 'network' component in report")
	}
	if netComp.Status != "warning" && netComp.Status != "critical" {
		t.Errorf("expected network status warning/critical, got %s", netComp.Status)
	}

	// Should *not* be safe to deploy new apps.
	if report.SafeToDeployNewApp.Status != "no" {
		t.Errorf("expected safe_to_deploy_new_app.status == 'no', got %s",
			report.SafeToDeployNewApp.Status)
	}
}
