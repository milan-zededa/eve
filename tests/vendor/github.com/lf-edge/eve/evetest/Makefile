EVETEST_VERSION := $(shell grep -v '^\#' VERSION | head -n1)

EVE_API_REPO = https://github.com/lf-edge/eve-api
EVE_API_COMMIT ?= 2d9b92e

EVETEST_ADAM_VERSION ?= 0.0.57
EVETEST_ADAM_REPO ?= lfedge/adam

EVETEST_IMAGE := lfedge/evetest:$(EVETEST_VERSION)-adam-$(EVETEST_ADAM_VERSION)
EVETEST_API_PORT ?= 50021

PROTOC_VERSION ?= 31.1
PROTOC_GEN_GO_VERSION ?= 1.36.6
PROTOC_GEN_GO_GRPC_VERSION ?= 1.5.1

PROTO_SRC_DIR := $(CURDIR)/grpcapi/proto
EVE_API_DIR ?= $(CURDIR)/grpcapi/eve-api
GEN_GO_DIR := $(CURDIR)/grpcapi/go
PROTOC_OUT_OPTS = paths=source_relative

REPO_ROOT := $(abspath $(CURDIR)/..)

GOBIN ?= $(shell go env GOBIN)
ifeq ($(GOBIN),)
GOBIN := $(HOME)/go/bin
endif

.PHONY: evetest proto proto-container clone-eve-api

evetest: ensure-evetest-image
ifndef EVETEST_NAME
ifndef NAME
	$(error EVETEST_NAME or NAME must be set to specify the test or test suite name to run)
endif
	$(eval EVETEST_NAME := $(NAME))
endif
	$(eval EVE_VERSION_ENV :=)
ifndef EVETEST_EVE_VERSION
	$(eval EVETEST_EVE_VERSION := $(strip $(shell \
		$(MAKE) -s -C $(REPO_ROOT) version 2>/dev/null \
	)))
	$(eval EVE_VERSION_ENV := -e EVETEST_EVE_VERSION=$(EVETEST_EVE_VERSION))
endif
	@echo "Running evetest $(EVETEST_NAME) for EVE $(EVETEST_EVE_VERSION) with evetest image version $(EVETEST_VERSION)-adam-$(EVETEST_ADAM_VERSION)..."
	$(eval ENV_VARS := $(shell env | grep '^EVETEST_' | grep -v '^EVETEST_NAME=' | sed 's/^/-e /'))
	$(eval ARTIFACTS_MOUNT :=)
ifneq ($(EVETEST_COLLECT_ARTIFACTS),)
	$(eval ARTIFACTS_MOUNT := -v $(EVETEST_COLLECT_ARTIFACTS):/artifacts)
endif
	$(eval BROKER_TMP_MOUNT :=)
	$(eval BROKER_TMP_ENV :=)
	$(eval BROKER_PROXY_CA_MOUNT :=)
ifndef EVETEST_BROKER_ADDRESS
	# Broker will run inside the container.
	$(eval BROKER_TMP_MOUNT := -v /tmp:/tmp)
	$(eval BROKER_TMP_ENV := -e EVETEST_BROKER_IMAGE_DIR=/tmp/evetest/qemu)
ifneq ($(EVETEST_BROKER_PROXY_CA_CHAIN),)
	$(eval BROKER_PROXY_CA_MOUNT := -v $(EVETEST_BROKER_PROXY_CA_CHAIN):/etc/evetest/proxy/ca-chain.pem:ro)
endif
endif
	# Detect host Go build cache (optional, used to speed up the test build)
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
	$(eval EVETEST_HOST_UID := $(shell id -u))
	$(eval EVETEST_HOST_GID := $(shell id -g))
	docker run --rm -it \
		--name evetest-$(EVETEST_API_PORT) \
		-p $(EVETEST_API_PORT):$(EVETEST_API_PORT) \
		-v $(REPO_ROOT)/tests:/tests \
		-v /var/run/docker.sock:/var/run/docker.sock \
		--cap-add=NET_ADMIN \
		--sysctl net.ipv6.conf.all.disable_ipv6=0 \
		--sysctl net.ipv6.conf.default.disable_ipv6=0 \
		--sysctl net.ipv6.conf.all.forwarding=1 \
		--sysctl net.ipv6.conf.default.forwarding=1 \
		--device=/dev/net/tun \
		--device=/dev/kvm \
		$(ARTIFACTS_MOUNT) \
		$(BROKER_TMP_MOUNT) \
		$(BROKER_PROXY_CA_MOUNT) \
		$(GO_CACHE_MOUNT) \
		$(ENV_VARS) \
		$(EVE_VERSION_ENV) \
		$(BROKER_TMP_ENV) \
		$(GO_CACHE_ENV) \
		-e EVETEST_NAME=$(EVETEST_NAME) \
		-e EVETEST_HOST_UID=$(EVETEST_HOST_UID) \
		-e EVETEST_HOST_GID=$(EVETEST_HOST_GID) \
		lfedge/evetest:$(EVETEST_VERSION)-adam-$(EVETEST_ADAM_VERSION)

ensure-evetest-image:
	@if ! docker image inspect $(EVETEST_IMAGE) >/dev/null 2>&1; then \
		echo "Docker image $(EVETEST_IMAGE) not found locally, trying to pull..."; \
		if ! docker pull $(EVETEST_IMAGE); then \
			echo "Pull failed, building image locally..."; \
			$(MAKE) build-container; \
		fi; \
	fi

proto: clone-eve-api clean-proto-go
	mkdir -p $(GEN_GO_DIR)
	protoc -I$(PROTO_SRC_DIR) -I$(EVE_API_DIR)/proto \
		--go_out=$(PROTOC_OUT_OPTS):$(GEN_GO_DIR) \
		--go-grpc_out=$(PROTOC_OUT_OPTS):$(GEN_GO_DIR) \
		$(PROTO_SRC_DIR)/*.proto

proto-in-container:
	docker build \
		--build-arg PROTOC_VERSION=$(PROTOC_VERSION) \
		--build-arg PROTOC_GEN_GO_VERSION=$(PROTOC_GEN_GO_VERSION) \
		--build-arg PROTOC_GEN_GO_GRPC_VERSION=$(PROTOC_GEN_GO_GRPC_VERSION) \
		-t evetest-api-builder -f protobuilder/Dockerfile .
	docker run --rm \
		--env HOME=/src \
		--env EVE_API_DIR=/tmp/eve-api \
		-v $(CURDIR):/src \
		-w /src \
		-u $$(id -u):$$(id -g) \
		evetest-api-builder make proto

clone-eve-api:
	@if [ ! -d "$(EVE_API_DIR)/.git" ]; then \
		echo "Cloning eve-api into $(EVE_API_DIR)"; \
		git clone $(EVE_API_REPO) $(EVE_API_DIR); \
	else \
		echo "eve-api already cloned, fetching latest"; \
		cd $(EVE_API_DIR) && git fetch --all --tags; \
	fi
	cd $(EVE_API_DIR) && git checkout $(EVE_API_COMMIT)

clean-proto-go:
	rm -rf $(GEN_GO_DIR)/*.pb.go

build-container: build-broker-container
	docker build -f Dockerfile.evetest \
		--build-arg EVETEST_VERSION=$(EVETEST_VERSION) \
		--build-arg EVETEST_ADAM_REPO=$(EVETEST_ADAM_REPO) \
		--build-arg EVETEST_ADAM_VERSION=$(EVETEST_ADAM_VERSION) \
		-t lfedge/evetest:$(EVETEST_VERSION)-adam-$(EVETEST_ADAM_VERSION) .

build-broker-container:
	@echo "Building Docker image lfedge/evetest-broker:$(EVETEST_VERSION)"
	docker build \
		--build-arg EVETEST_VERSION=$(EVETEST_VERSION) \
		-t lfedge/evetest-broker:$(EVETEST_VERSION) \
		-f Dockerfile.broker .

# Create the eve-broker user/group for the libvirt provider
setup-broker-user:
	@echo "Setting up eve-broker user and groups..."
	@if [ "$$(id -u)" -ne 0 ]; then \
		echo "Error: This target must be run as root."; \
		echo "       Please rerun with: sudo make setup-broker-user"; \
		exit 1; \
	fi
	@if ! id -u eve-broker >/dev/null 2>&1; then \
		echo "Creating eve-broker user and group..."; \
		groupadd -f eve-broker; \
		useradd -r -g eve-broker -m -s /bin/bash eve-broker; \
	else \
		echo "eve-broker user already exists"; \
	fi
	@echo "Adding eve-broker to docker and libvirt groups (for socket access)..."
	usermod -aG docker eve-broker || true
	usermod -aG libvirt eve-broker || true
	@echo "Allowing libvirt-qemu to access eve-broker-owned images..."
	usermod -aG eve-broker libvirt-qemu || true
	@echo "Configuring /var/lib/libvirt/images ownership and permissions..."
	if [ -d /var/lib/libvirt/images ]; then \
		chgrp libvirt /var/lib/libvirt/images; \
		chmod 771 /var/lib/libvirt/images; \
		echo "Image directory now writable by libvirt group (eve-broker is a member)."; \
	else \
		echo "Warning: /var/lib/libvirt/images does not exist. Skipping permission setup."; \
	fi
	@echo "setup-broker-user complete. You can now run the container safely as eve-broker."

# Run broker container for the libvirt provider.
run-broker-container:
	@echo "Running Evetest Broker version $(EVETEST_VERSION)..."
	# Capture EVETEST_* and DOCKER_* environment variables
	$(eval EV_VARS := $(shell env | grep -E '^(EVETEST|DOCKER)_' | sed 's/^/-e /'))
	$(eval IMAGE_DIR := $(or $(EVETEST_BROKER_IMAGE_DIR),/var/lib/libvirt/images))
	# Optionally mount (docker registry and/or app image datastore) proxy CA chain if provided.
ifneq ($(EVETEST_BROKER_PROXY_CA_CHAIN),)
	$(eval PROXY_CA_MOUNT := -v $(EVETEST_BROKER_PROXY_CA_CHAIN):/etc/evetest/proxy/ca-chain.pem:ro)
endif
	# Determine group IDs dynamically
	$(eval LIBVIRT_GID := $(shell getent group libvirt | cut -d: -f3))
	$(eval DOCKER_GID := $(shell getent group docker | cut -d: -f3))
	# Determine user for container (if eve-broker exists)
	$(eval EVE_BROKER_UID := $(shell getent passwd eve-broker | cut -d: -f3))
	$(eval EVE_BROKER_GID := $(shell getent passwd eve-broker | cut -d: -f4))
	@if [ -n "$(EVE_BROKER_UID)" ]; then \
		echo "Running container as eve-broker user ($(EVE_BROKER_UID):$(EVE_BROKER_GID))..."; \
		USER_OPT="-u $(EVE_BROKER_UID):$(EVE_BROKER_GID)"; \
	else \
		echo "Warning: eve-broker user not found. Running as root."; \
		echo "         To improve security, run: sudo make setup-broker-user"; \
		USER_OPT=""; \
	fi; \
	# Run the broker container in privileged mode to allow enabling
	# forwarding of link-local protocols (EAPOL, LLDP) on the xconnect bridges.
	docker run --rm -it \
		$$USER_OPT \
		--group-add $(LIBVIRT_GID) \
		--group-add $(DOCKER_GID) \
		$(EV_VARS) \
		$(PROXY_CA_MOUNT) \
		--network host \
		--privileged \
		-v /tmp:/tmp \
		-v /var/run/libvirt/:/var/run/libvirt/ \
		-v /var/run/docker.sock:/var/run/docker.sock \
		-v ~/.docker:/root/.docker:ro \
		-v $(IMAGE_DIR):$(IMAGE_DIR) \
		lfedge/evetest-broker:$(EVETEST_VERSION)

install-broker:
	@echo "Installing evetest Broker version $(EVETEST_VERSION)..."
	cd ./broker && go build -ldflags "-X main.version=$(EVETEST_VERSION)" -o $(GOBIN)/evetest-broker
	@echo "Installed evetest Broker to $(GOBIN)/evetest-broker"

install-cli:
	@echo "Installing evetest CLI version $(EVETEST_VERSION)..."
	cd ./cli && go build -ldflags "-X main.version=$(EVETEST_VERSION)" -o $(GOBIN)/evetest
	@echo "Installed evetest CLI to $(GOBIN)/evetest"

build-sdn-container:
	@$(MAKE) -C sdn build
