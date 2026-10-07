# Copyright (c) 2026 Zededa, Inc.
# SPDX-License-Identifier: Apache-2.0

# Version of the evetest container image (harness) to run. The harness is built
# and published from master; this branch carries only the tests, netmodels and
# matchers, which are bind-mounted into that container. Bump when needing a
# newer harness.
EVETEST_VERSION ?= 1.9

# Master commit the evetest harness is taken from by "make sync-harness".
# Should correspond to EVETEST_VERSION above (the image the tests run in).
EVETEST_HARNESS_COMMIT ?= 5e153e0c8bba807829888111c9607587c7555336
# Where to fetch that commit from if it is not available locally.
EVETEST_HARNESS_REMOTE ?= https://github.com/lf-edge/eve.git
EVETEST_ORG ?= lfedge

# Repository holding built EVE images (mirrors constants.DefaultEVERepo).
EVETEST_EVE_REPO ?= lfedge/eve

EVETEST_IMAGE := $(EVETEST_ORG)/evetest:$(EVETEST_VERSION)
EVETEST_API_PORT ?= 50021

REPO_ROOT := $(abspath $(CURDIR)/..)

# Allocate a pseudo-TTY only when stdout is a terminal (i.e. interactive use).
DOCKER_IT := $(shell [ -t 1 ] && echo '-it' || echo '-i')

.PHONY: evetest list-tests ensure-evetest-image sync-harness

# Copy the master harness (git-ignored) into this directory so that the tests
# compile locally (go vet, IDE). Not needed to run tests: the container has it.
sync-harness:
	@./sync-harness.sh $(EVETEST_HARNESS_COMMIT) $(EVETEST_HARNESS_REMOTE)


list-tests: ensure-evetest-image
	@docker run --rm $(DOCKER_IT) \
		-v $(REPO_ROOT)/evetest/tests:/evetest/tests \
		$(EVETEST_IMAGE)

evetest: ensure-evetest-image
ifndef EVETEST_NAME
ifndef NAME
	$(error EVETEST_NAME or NAME must be set. Run 'make list-tests' to see available tests)
endif
	$(eval EVETEST_NAME := $(NAME))
endif
	@# Default the EVE version to this checkout's, which is what the container
	@# transport needs to name an image. Deliberately NOT defaulted under
	@# EVETEST_EVE_LIVE_IMAGE: the live transport looks the version up among the
	@# local builds under dist/, and `make version` reports the working tree as it
	@# is *now* -- including a "-dirty-<timestamp>" suffix -- which is a directory
	@# that generally does not exist. Left empty, the newest build (dist/<arch>/
	@# current) is used and reports its own version. An EVETEST_EVE_VERSION the
	@# operator sets explicitly is always honoured, by both transports.
	$(eval EVE_VERSION_ENV :=)
ifndef EVETEST_EVE_VERSION
ifeq ($(strip $(filter-out false False FALSE 0 f F,$(EVETEST_EVE_LIVE_IMAGE))),)
	@# Resolve which EVE version to test. On a dirty tree 'make version' embeds
	@# the wall-clock minute of *this* invocation, so it almost never matches
	@# the timestamp baked into the image tag by an earlier 'make eve' run.
	@# In that case fall back to the version recorded by the last completed
	@# build (dist/<arch>/current, read via 'make currentversion'), provided it
	@# is a dirty build of the same commit and its EVE Docker image exists
	@# locally. 'make currentversion' reports the full version including the
	@# hypervisor/arch suffix; strip it by anchoring on the dirty timestamp.
	$(eval EVETEST_EVE_VERSION := $(strip $(shell \
		v="$$($(MAKE) -s -C $(REPO_ROOT) version 2>/dev/null)"; \
		if echo "$$v" | grep -q -e '-dirty-' && \
			! docker images --format '{{.Tag}}' "$(EVETEST_EVE_REPO)" 2>/dev/null \
				| grep -q "^$$v-"; then \
			base="$${v%%-dirty-*}-dirty-"; \
			cur="$$($(MAKE) -s -C $(REPO_ROOT) currentversion 2>/dev/null \
				| sed -E 's/(-dirty-[0-9]{4}-[0-9]{2}-[0-9]{2}\.[0-9]{2}\.[0-9]{2}).*/\1/')"; \
			if [ -n "$$cur" ] && [ "$${cur#"$$base"}" != "$$cur" ] && \
				docker images --format '{{.Tag}}' "$(EVETEST_EVE_REPO)" 2>/dev/null \
					| grep -q "^$$cur-"; then \
				echo "NOTE: no local EVE image for dirty version $$v;" \
					"falling back to last built version $$cur (from dist/current)." >&2; \
				echo "NOTE: run 'make eve' first if EVE sources changed since that build." >&2; \
				v="$$cur"; \
			fi; \
		fi; \
		echo "$$v" \
	)))
	$(eval EVE_VERSION_ENV := -e EVETEST_EVE_VERSION=$(EVETEST_EVE_VERSION))
endif
endif
	@echo "Running evetest $(EVETEST_NAME) for EVE $(or $(EVETEST_EVE_VERSION),the newest local build) with evetest image version $(EVETEST_VERSION)..."
	$(eval ENV_VARS := $(shell env | grep '^EVETEST_' | grep -v '^EVETEST_NAME=' | sed 's/^/-e /'))
	$(eval ARTIFACTS_MOUNT :=)
ifneq ($(EVETEST_COLLECT_ARTIFACTS),)
	$(eval ARTIFACTS_MOUNT := -v $(EVETEST_COLLECT_ARTIFACTS):/artifacts)
endif
	$(eval BROKER_IMAGE_MOUNT :=)
	$(eval BROKER_IMAGE_ENV :=)
	$(eval BROKER_PROXY_CA_MOUNT :=)
	@# Always mount the evetest data directory so that Docker bind-mounts issued
	@# from inside the container (e.g. rootfs extraction for upgrades) resolve
	@# correctly on the host.
	$(eval EVETEST_HOME_DIR := $(HOME)/.evetest)
	$(eval EVETEST_HOME_MOUNT := -v $(EVETEST_HOME_DIR):$(EVETEST_HOME_DIR))
	@mkdir -p $(EVETEST_HOME_DIR)
	@# Tests that compile a bpftrace script for the device's kernel (see
	@# tests/apps/pciaccess_helpers_test.go) run eve-tools/bpftrace-compiler
	@# from the checkout, mounted at the same position relative to /evetest/tests
	@# as in the repository. The compiler keeps the pulled kernel images in the
	@# linuxkit cache and the compiled scripts in its own cache, which it uses
	@# only when the directory exists; mounting both from the evetest home makes
	@# them survive runs.
	$(eval BPFTRACE_COMPILER_MOUNTS := \
		-v $(REPO_ROOT)/eve-tools/bpftrace-compiler:/eve-tools/bpftrace-compiler:ro \
		-v $(EVETEST_HOME_DIR)/linuxkit-cache:/root/.linuxkit/cache \
		-v $(EVETEST_HOME_DIR)/bpftrace-compiler-cache:/root/.bpftrace-compiler/cache)
	@mkdir -p $(EVETEST_HOME_DIR)/linuxkit-cache $(EVETEST_HOME_DIR)/bpftrace-compiler-cache
ifndef EVETEST_BROKER_ADDRESS
	@# Broker runs inside the container; it stores per-VM disk images on the
	@# host. Default to <home-dir>/.evetest because /tmp is usually on the small
	@# root partition (or tmpfs) and quickly fills up with three-node clusters.
	$(eval IMAGE_DIR := $(or $(EVETEST_BROKER_IMAGE_DIR),$(EVETEST_HOME_DIR)))
	@# Only add a broker image mount when the image dir differs from EVETEST_HOME_DIR
	@# to avoid a duplicate bind-mount error.
	$(eval BROKER_IMAGE_MOUNT := $(if $(filter-out $(EVETEST_HOME_DIR),$(IMAGE_DIR)),-v $(IMAGE_DIR):$(IMAGE_DIR),))
	$(eval BROKER_IMAGE_ENV := -e EVETEST_BROKER_IMAGE_DIR=$(IMAGE_DIR))
ifneq ($(EVETEST_BROKER_PROXY_CA_CHAIN),)
	$(eval BROKER_PROXY_CA_MOUNT := -v $(EVETEST_BROKER_PROXY_CA_CHAIN):/etc/evetest/proxy/ca-chain.pem:ro)
endif
	@mkdir -p $(IMAGE_DIR)
endif
	@# Detect host Go build cache (optional, used to speed up the test build)
	$(eval HOST_GOCACHE := $(strip $(shell \
		if command -v go >/dev/null 2>&1; then \
			go env GOCACHE 2>/dev/null; \
		fi \
	)))
	$(eval GO_CACHE_MOUNT :=)
	$(eval GO_CACHE_ENV :=)
	$(if $(HOST_GOCACHE), \
		$(eval GO_CACHE_MOUNT := -v $(HOST_GOCACHE):/go-build-cache) \
		$(eval GO_CACHE_ENV := -e GOCACHE=/go-build-cache) \
	)
	$(eval DOCKER_CONFIG_MOUNT :=)
	$(if $(wildcard $(HOME)/.docker/config.json), \
		$(eval DOCKER_CONFIG_MOUNT := -v $(HOME)/.docker/config.json:/root/.docker/config.json:ro) \
	)
	$(eval EVETEST_HOST_UID := $(shell id -u))
	$(eval EVETEST_HOST_GID := $(shell id -g))
	@# On Linux, only pass /dev/kvm if it exists on the host. On macOS, Docker
	@# Desktop's Linux VM does not support nested virtualization, so /dev/kvm is
	@# never available; omit the flag so the container starts and QEMU falls back
	@# to TCG software emulation.
	$(eval KVM_DEVICE := $(shell [ -e /dev/kvm ] && echo '--device=/dev/kvm'))
	@# Mount the EVE build output directory so a locally built live.qcow2 is
	@# visible inside the container. Same absolute path on both sides so that
	@# EVETEST_EVE_DIST_DIR is valid both inside the container and in any error
	@# message a developer sees on the host. Read-write: the harness caches the
	@# image's sha256 in a sidecar file next to it. Only added when a local
	@# build exists, so nothing breaks without one.
	$(eval EVETEST_DIST_DIR := $(or $(EVETEST_EVE_DIST_DIR),$(REPO_ROOT)/dist))
	$(eval DIST_DIR_MOUNT :=)
	$(eval DIST_DIR_ENV :=)
	$(if $(wildcard $(EVETEST_DIST_DIR)), \
		$(eval DIST_DIR_MOUNT := -v $(EVETEST_DIST_DIR):$(EVETEST_DIST_DIR)) \
		$(eval DIST_DIR_ENV := -e EVETEST_EVE_DIST_DIR=$(EVETEST_DIST_DIR)) \
	)
	@if docker inspect --type container evetest-$(EVETEST_API_PORT) >/dev/null 2>&1; then \
		echo ""; \
		echo "Error: evetest instance 'evetest-$(EVETEST_API_PORT)' is already running."; \
		echo ""; \
		echo "To run multiple instances in parallel, set a different EVETEST_API_PORT for"; \
		echo "each one (default is 50021):"; \
		echo ""; \
		echo "  EVETEST_API_PORT=50022 make evetest NAME=<test-name>"; \
		echo ""; \
		echo "When using the evetest CLI against a non-default port, set the same variable:"; \
		echo ""; \
		echo "  EVETEST_API_PORT=50022 evetest status"; \
		echo ""; \
		exit 1; \
	fi >&2
	@# Colorize framework log output only when stdout is a terminal. The test
	@# runs inside the container where stdout is never a TTY (go test pipes it),
	@# so the detection must happen here. It also cannot use $(shell [ -t 1 ])
	@# because within $(shell) stdout is make's capture pipe, never a terminal;
	@# only a recipe shell sees make's real stdout.
	[ -t 1 ] && COLOR=true || COLOR=false; \
	docker run --rm $(DOCKER_IT) \
		--name evetest-$(EVETEST_API_PORT) \
		-p $(EVETEST_API_PORT):$(EVETEST_API_PORT) \
		-v $(REPO_ROOT)/evetest/tests:/evetest/tests \
		-v $(REPO_ROOT)/evetest/netmodels:/evetest/netmodels \
		-v $(REPO_ROOT)/evetest/matchers:/evetest/matchers \
		-v /var/run/docker.sock:/var/run/docker.sock \
		--cap-add=NET_ADMIN \
		--sysctl net.ipv6.conf.all.disable_ipv6=0 \
		--sysctl net.ipv6.conf.default.disable_ipv6=0 \
		--sysctl net.ipv6.conf.all.forwarding=1 \
		--sysctl net.ipv6.conf.default.forwarding=1 \
		--device=/dev/net/tun \
		$(KVM_DEVICE) \
		$(ARTIFACTS_MOUNT) \
		$(EVETEST_HOME_MOUNT) \
		$(BPFTRACE_COMPILER_MOUNTS) \
		$(BROKER_IMAGE_MOUNT) \
		$(BROKER_PROXY_CA_MOUNT) \
		$(DIST_DIR_MOUNT) \
		$(GO_CACHE_MOUNT) \
		$(DOCKER_CONFIG_MOUNT) \
		-e EVETEST_COLOR_OUTPUT=$$COLOR \
		$(ENV_VARS) \
		$(EVE_VERSION_ENV) \
		$(BROKER_IMAGE_ENV) \
		$(DIST_DIR_ENV) \
		$(GO_CACHE_ENV) \
		-e EVETEST_NAME=$(EVETEST_NAME) \
		-e EVETEST_HOME=$(EVETEST_HOME_DIR) \
		-e EVETEST_HOST_UID=$(EVETEST_HOST_UID) \
		-e EVETEST_HOST_GID=$(EVETEST_HOST_GID) \
		$(EVETEST_IMAGE)

# Ensure the evetest image is available locally; it is only ever pulled, never
# built here (the harness is built from master).
ensure-evetest-image:
	@if ! docker image inspect $(EVETEST_IMAGE) >/dev/null 2>&1; then \
		echo "Docker image $(EVETEST_IMAGE) not found locally, pulling..."; \
		docker pull $(EVETEST_IMAGE) || { \
			echo "Error: failed to pull $(EVETEST_IMAGE)" >&2; exit 1; }; \
	fi
