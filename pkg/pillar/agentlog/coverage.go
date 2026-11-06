// Copyright (c) 2026 Zededa, Inc.
// SPDX-License-Identifier: Apache-2.0

//go:build cover

package agentlog

import (
	"os"
	"path/filepath"
	"runtime/coverage"
	"strconv"
	"strings"
	"syscall"
	"time"

	"github.com/lf-edge/eve/pkg/pillar/base"
)

// This file is compiled only when the binary is built with -tags cover (i.e.
// COVER=y in the pillar Makefile).
//
// NOTE: Go's binary coverage runtime writes covmeta only via WriteMetaDir
// (or at normal os.Exit); signal termination does NOT flush the meta file.
// We therefore call WriteMetaDir explicitly on each SIGUSR2.

// MaybeInitSigUsr2 sets up a SIGUSR2 handler if !coverage
// This is a no-op with coverage to avoid having two very different actions
// for SIGUSR2
func MaybeInitSigUsr2(sigs chan os.Signal) {
}

// FlushCoverage writes the current in-memory coverage counters to GOCOVERDIR.
// It is a no-op when GOCOVERDIR is unset.
//
// Call this before any operation that bypasses a normal os.Exit, such as
// zboot.Reset or zboot.Poweroff, to ensure coverage data accumulated since
// the last SIGUSR2 snapshot is not lost when the device reboots.
func FlushCoverage(log *base.LogObject) {
	dir := os.Getenv("GOCOVERDIR")
	if dir == "" {
		log.Notice("FlushCoverage: no GOCOVERDIR")
		return
	}
	log.Notice("FlushCoverage")
	if err := coverage.WriteMetaDir(dir); err != nil {
		log.Errorf("FlushCoverage: WriteMetaDir(%s) failed: %v", dir, err)
	}
	if err := coverage.WriteCountersDir(dir); err != nil {
		log.Errorf("FlushCoverage: WriteCountersDir(%s) failed: %v", dir, err)
	}
	syscall.Sync()
}

// peerFlushTimeout caps the wait for the peer's flush. COVER=y images are
// never used in production, so this cannot delay a production reboot.
const peerFlushTimeout = 10 * time.Second

// FlushCoverageAndPeers flushes this process and asks kube-init (a separate
// process, eve-k only) to flush too.
func FlushCoverageAndPeers(log *base.LogObject) {
	dir := os.Getenv("GOCOVERDIR")
	if dir == "" {
		FlushCoverage(log)
		return
	}
	before := countCounterFiles(dir)
	peers := 0
	for _, pid := range findPidsByComm("kube-init") {
		if err := syscall.Kill(pid, syscall.SIGUSR2); err != nil {
			log.Errorf("FlushCoverageAndPeers: kill -USR2 %d failed: %v", pid, err)
			continue
		}
		peers++
	}
	FlushCoverage(log)
	// Our own flush adds one file, each peer one more.
	deadline := time.Now().Add(peerFlushTimeout)
	for peers > 0 && countCounterFiles(dir) < before+1+peers {
		if time.Now().After(deadline) {
			log.Warnf("FlushCoverageAndPeers: peer did not flush within %v",
				peerFlushTimeout)
			break
		}
		time.Sleep(200 * time.Millisecond)
	}
	syscall.Sync()
}

func countCounterFiles(dir string) int {
	files, _ := filepath.Glob(filepath.Join(dir, "covcounters.*"))
	return len(files)
}

func findPidsByComm(name string) []int {
	var pids []int
	entries, _ := os.ReadDir("/proc")
	for _, e := range entries {
		pid, err := strconv.Atoi(e.Name())
		if err != nil {
			continue
		}
		comm, err := os.ReadFile("/proc/" + e.Name() + "/comm")
		if err == nil && strings.TrimSpace(string(comm)) == name {
			pids = append(pids, pid)
		}
	}
	return pids
}
