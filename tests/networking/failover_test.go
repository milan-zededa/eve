package networking_test

import "testing"

func TestPortFailover(test *testing.T) {
	// TODO : mgmt and uplink NI failover from one port to another
}

func TestNetworkConfigFallback(test *testing.T) {
	// TODO : go back to previously working config, retry latest, then move to latest once working
}

func TestIntermittentConnectivity(test *testing.T) {
	// TODO : try poor bandwidth and periods of no connectivity
}
