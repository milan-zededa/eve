package main

import (
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"time"
)

const metadataSrvAddr = "http://169.254.169.254"
const netMetricsEndpoint = "/eve/v1/networks/metrics.json"

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

func main() {
	client := http.Client{Timeout: 5 * time.Second}
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

	fmt.Printf("Network metrics: %+v", netMetrics)
}
