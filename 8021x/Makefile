# ---------------------------
# 802.1X testbed Makefile
# ---------------------------

IFNAME ?=

build-authenticator:
	docker build -t milan-zededa/pnac-authenticator -f Dockerfile.authenticator .

build-scepserver:
	docker build -t milan-zededa/scep-server -f Dockerfile.scepserver .

build: build-authenticator build-scepserver

# Move host interface into pnac-authenticator container
add-eve-interface:
	@if [ -z "$(IFNAME)" ]; then \
		echo "❌ Usage: make add-eve-interface IFNAME=<interface>"; \
		exit 1; \
	fi

	@PID_AUTH=$$(docker inspect -f '{{.State.Pid}}' pnac-authenticator); \
	echo "Moving interface $(IFNAME) into pnac-authenticator (PID $$PID_AUTH)"; \
	sudo ip link set $(IFNAME) netns $$PID_AUTH; \
	docker exec pnac-authenticator add-eve-interface.sh $(IFNAME)

run-lab: clean
	# Start authenticator
	docker run -d --rm --name pnac-authenticator \
		--network bridge \
		--privileged \
		milan-zededa/pnac-authenticator

	## Start SCEP server
	#docker run -d --rm --name scep-server \
	#	--network bridge \
	#	--privileged \
	#	-p 2016:2016 \
	#	milan-zededa/scep-server

	# Get SCEP server IP
	#@SCEP_IP=$$(docker inspect -f '{{range .NetworkSettings.Networks}}{{.IPAddress}}{{end}}' scep-server); \
	echo ""; \
	echo "✅ Lab started"; \
	#echo ""; \
	#echo "SCEP Server URL:"; \
	#echo "  http://$$SCEP_IP:2016/scep"; \
	echo ""; \
	echo "To enter containers:"; \
	echo "  make enter-auth"; \
	#echo "  make enter-scep-server"

clean:
	docker rm -f pnac-authenticator scep-server 2>/dev/null || true

enter-auth:
	docker exec -it pnac-authenticator bash

enter-scep-server:
	docker exec -it scep-server bash