# ---------------------------
# 802.1X testbed Makefile
# ---------------------------

build-supplicant:
	docker build -t milan-zededa/pnac-supplicant -f Dockerfile.supplicant .

build-authenticator:
	docker build -t milan-zededa/pnac-authenticator -f Dockerfile.authenticator .

build-scepproxy:
	docker build -t milan-zededa/scep-proxy -f Dockerfile.scepproxy .

build-scepserver:
	docker build -t milan-zededa/scep-server -f Dockerfile.scepserver .

build: build-authenticator build-supplicant build-scepproxy build-scepserver

gen-certs:
	./scripts/gen-certs.sh

run-lab: clean
	# ---------------------------
	# Create veth pairs
	# ---------------------------
	sudo ip link add veth0 type veth peer name veth1
	sudo ip link add veth2 type veth peer name veth3
	sudo ip link add veth4 type veth peer name veth5

	sudo ip link set veth0 up
	sudo ip link set veth1 up
	sudo ip link set veth2 up
	sudo ip link set veth3 up
	sudo ip link set veth4 up
	sudo ip link set veth5 up

	# ---------------------------
	# Start authenticator
	# ---------------------------
	docker run -d --rm --name pnac-authenticator \
		--network bridge \
		--privileged \
		milan-zededa/pnac-authenticator

	PID_AUTH=$$(docker inspect -f '{{.State.Pid}}' pnac-authenticator); \
	sudo ip link set veth0 netns $$PID_AUTH

	# ---------------------------
	# Start supplicant
	# ---------------------------
	docker run -d --rm --name pnac-supplicant \
		--network bridge \
		--privileged \
		--device /dev/tpm0:/dev/tpm0 \
		--device /dev/tpmrm0:/dev/tpmrm0 \
		milan-zededa/pnac-supplicant sleep infinity

	PID_SUPP=$$(docker inspect -f '{{.State.Pid}}' pnac-supplicant); \
	sudo ip link set veth1 netns $$PID_SUPP; \
	sudo ip link set veth2 netns $$PID_SUPP; \
	sudo nsenter -t $$PID_SUPP -n ip addr add 192.168.60.1/24 dev veth2; \
	sudo nsenter -t $$PID_SUPP -n ip link set veth1 up; \
	sudo nsenter -t $$PID_SUPP -n ip link set veth2 up; \
	sudo nsenter -t $$PID_SUPP -n ip route add 192.168.70.0/24 via 192.168.60.2

	# ---------------------------
	# Start SCEP proxy
	# ---------------------------
	docker run -d --rm --name scep-proxy \
		--network bridge \
		--privileged \
		milan-zededa/scep-proxy

	PID_PROXY=$$(docker inspect -f '{{.State.Pid}}' scep-proxy); \
	sudo ip link set veth3 netns $$PID_PROXY; \
	sudo ip link set veth4 netns $$PID_PROXY; \
	sudo nsenter -t $$PID_PROXY -n ip addr add 192.168.60.2/24 dev veth3; \
	sudo nsenter -t $$PID_PROXY -n ip addr add 192.168.70.1/24 dev veth4; \
	sudo nsenter -t $$PID_PROXY -n ip link set veth3 up; \
	sudo nsenter -t $$PID_PROXY -n ip link set veth4 up

	# ---------------------------
	# Start SCEP server
	# ---------------------------
	docker run -d --rm --name scep-server \
		--network bridge \
		--privileged \
		milan-zededa/scep-server

	PID_SCEP=$$(docker inspect -f '{{.State.Pid}}' scep-server); \
	sudo ip link set veth5 netns $$PID_SCEP; \
	sudo nsenter -t $$PID_SCEP -n ip addr add 192.168.70.2/24 dev veth5; \
	sudo nsenter -t $$PID_SCEP -n ip link set veth5 up; \
	sudo nsenter -t $$PID_SCEP -n ip route add 192.168.60.0/24 via 192.168.70.1

	@echo ""
	@echo "✅ Lab started"
	@echo ""
	@echo "To enter containers:"
	@echo "  make enter-auth"
	@echo "  make enter-sup"
	@echo "  make enter-scep-proxy"
	@echo "  make enter-scep-server"

clean:
	docker rm -f pnac-authenticator pnac-supplicant scep-proxy scep-server 2>/dev/null || true
	sudo ip link del veth0 2>/dev/null || true
	sudo ip link del veth2 2>/dev/null || true
	sudo ip link del veth4 2>/dev/null || true

enter-auth:
	@docker exec -it pnac-authenticator bash

enter-sup:
	@docker exec -it pnac-supplicant bash

enter-scep-proxy:
	@docker exec -it scep-proxy bash

enter-scep-server:
	@docker exec -it scep-server bash
