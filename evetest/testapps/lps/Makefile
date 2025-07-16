# Copyright (c) 2026 Zededa, Inc.
# SPDX-License-Identifier: Apache-2.0

# TODO: temporary, change to lfedge
REPO ?= milan4zededa
IMAGE = $(REPO)/evetest-lps
# Update VERSION whenever there is a change made to this app.
VERSION ?= 1.0

build:
	docker build -t $(IMAGE):$(VERSION) .

push: build
	docker push $(IMAGE):$(VERSION)
