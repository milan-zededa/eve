# TODO: temporary, change to lfedge
REPO ?= milan4zededa
IMAGE = $(REPO)/evetest-ubuntu-ctr
# Update VERSION whenever there is a change made to this app.
VERSION ?= 1.0

build:
	docker build -t $(IMAGE):$(VERSION) .

push: build
	docker push $(IMAGE):$(VERSION)
