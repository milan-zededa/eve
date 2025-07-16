# CLAUDE.md

This file provides guidance to Claude Code (claude.ai/code) when working with code in this repository.

## Overview

`evetest` is a Go-based integration testing framework for [EVE OS](https://github.com/lf-edge/eve). It replaces `eden` with a simpler architecture that uses standard Go testing, runs tests inside Docker containers, and supports programmable network configurations via an SDN VM.

## Running Tests

```bash
# Run a specific test (primary workflow)
EVETEST_LOG_LEVEL=debug EVETEST_NAME=<test-name> make | gotestfmt

# Run with external broker and custom adam version
EVETEST_BROKER_ADDRESS=<ip> EVETEST_ADAM_VERSION=<ver> EVETEST_LOG_LEVEL=debug \
  EVETEST_EVE_VERSION=<ver> EVETEST_NAME=TestLocalNI make evetest

# Install optional test output formatter
go install github.com/gotesttools/gotestfmt/v2/cmd/gotestfmt@latest
```

`EVETEST_NAME` (or `NAME`) is required. The `make evetest` target builds/pulls the evetest Docker image and runs `go test -json -run ^{EVETEST_NAME}$` inside it with `/tests` mounted from the EVE repo.

## Build Targets

```bash
# Build/install CLI tool (evetest binary)
make install-cli

# Build/install broker binary
make install-broker

# Build Docker images
make build-container          # builds evetest + broker containers
make build-broker-container   # broker container only
make build-sdn-container      # SDN VM container (requires linuxkit)

# Regenerate protobuf code (requires protoc)
make proto                    # needs eve-api cloned
make proto-in-container       # runs protoc inside Docker

# Run broker container (for libvirt provider)
sudo make setup-broker-user   # one-time setup
make run-broker-container
```

## Architecture

The framework has three main components that communicate over gRPC:

### 1. evetest container (test runner)
- Runs `go test` on test files from the EVE repo (`../tests/`)
- Tests call `evetest.Init`, `evetest.Setup`, `evetest.Close` and interact with `EdgeDevice` objects
- Runs Adam (controller) and Redis internally
- Exposes a gRPC API (default port 50021) for the CLI
- Source: root package `github.com/lf-edge/eve/evetest` — `requirements.go`, `devconfig.go`, `cipherdata.go`

### 2. evetest-broker
- Manages VM lifecycle (create/destroy EVE device VMs and the SDN VM)
- Provider interface in `broker/provider/provider.go` with implementations for libvirt (`libvirt.go`) and QEMU (`qemu.go`)
- Relays IP packets through a point-to-point tunnel between the SDN VM and the evetest container
- Source: `broker/` — `broker.go`, `image.go`, `main.go`

### 3. SDN VM (`sdn/`)
- A LinuxKit-based VM providing programmable virtual network environments for EVE
- `sdnagent` (`sdn/vm/cmd/sdnagent/`) manages network configuration using a declarative config-items system
- Config items in `sdn/vm/pkg/configitems/` represent network primitives (bridges, bonds, DHCP servers, DNS, iptables, HTTP proxies, SCEP servers, etc.)
- Includes helper services: `httpsrv`, `goproxy`, `netbootsrv`, `conntrack`
- Has its own `go.mod` (separate module) and is built with LinuxKit

### gRPC API
- Proto files: `grpcapi/proto/broker.proto`, `grpcapi/proto/sdn.proto`
- Generated Go code: `grpcapi/go/`
- SDN references helper: `grpcapi/go/sdn_references.go`

### CLI (`cli/`)
- Cobra-based CLI binary (`evetest`) that talks to the running evetest container gRPC API
- Commands grouped as `evetest eve ...`, `evetest sdn ...`, `evetest cluster ...`
- Source: `cli/evecmd.go`, `cli/sdncmd.go`, `cli/clustercmd.go`

## Key Configuration (Environment Variables)

| Variable | Description |
|---|---|
| `EVETEST_NAME` | Test or test suite name to run (required) |
| `EVETEST_BROKER_ADDRESS` | IP of evetest-broker (if external) |
| `EVETEST_EVE_VERSION` | EVE version to test (defaults to current repo HEAD) |
| `EVETEST_LOG_LEVEL` | Log level for evetest framework |
| `EVETEST_PAUSE_ON_FAILURE` | Pause on failure for interactive troubleshooting |
| `EVETEST_PAUSE_ON_CHECKPOINT` | Pause at named checkpoint |
| `EVETEST_COLLECT_ARTIFACTS` | Host path for test artifacts (logs, collect-info, etc.) |
| `EVETEST_API_PORT` | gRPC port exposed by evetest container (default 50021) |
| `EVETEST_ADAM_VERSION` | Adam controller version (default in Makefile) |

## Test Development

Tests live in `../tests/` (the parent EVE repo's `tests/` directory), mounted at `/tests` inside the container. Tests are standard Go tests using the evetest framework:

```go
func TestFoo(t *testing.T) {
    evetest.Init(t)
    defer evetest.Close(t)
    evetest.Setup(t,
        evetest.RequireEdgeDevice{Name: "dev1", MinCPUs: 4},
        evetest.RequireNetworkModel{...},
        evetest.RequireInternetConnectivity{},
    )
    dev := evetest.GetEdgeDevice("dev1")
    // interact with dev...
}
```

## Debugging Running Tests

```bash
# SSH into the SDN VM (when running with qemu/inside container)
ssh -i /root/.ssh/sdn_rsa root@250.250.250.2

# SSH into SDN VM with libvirt (from host)
ssh -i ~/go/src/github.com/lf-edge/eve/evetest/sdn/vm/cert/ssh/sdn_rsa root@192.168.170.X

# Prevent NetworkManager from managing xconnect bridges
sudo vim /etc/NetworkManager/conf.d/99-evetest-unmanaged.conf
# Add: [device-evetest-xconnect-unmanaged] / match-device=interface-name:evetest-x-* / managed=0
sudo systemctl restart NetworkManager
```

## Proto Regeneration

After changing `.proto` files, regenerate with:
```bash
make proto-in-container  # preferred, no local protoc needed
```
The `EVE_API_COMMIT` in `Makefile` pins the `eve-api` proto dependency.
