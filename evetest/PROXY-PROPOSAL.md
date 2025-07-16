# Docker Image Caching for evetest

## Context

When running evetest repeatedly, Docker image pulls from the evetest container re-download the same images from external registries. This wastes bandwidth and slows test cycles, especially with Docker Hub rate limits.

The goal: deploy a caching reverse proxy **on the host** that transparently intercepts and caches Docker registry pull requests from the evetest container — with no client-side proxy configuration.

## Transparency Mechanism

**DNS interception via `--add-host`**: When starting the evetest container, pass `--add-host=docker.io:<proxy-ip>` (and for other registries) so that DNS for registry hostnames resolves to the caching proxy's IP. The proxy listens on port 443, uses SNI to determine the real upstream registry, does MITM with its CA cert, caches responses, and forwards to the real registry.

The existing `EVETEST_BROKER_PROXY_CA_CHAIN` mechanism injects the proxy's CA cert into EVE's trust store, so EVE trusts the MITM certificates.

## Why not rpardini/docker-registry-proxy?

It operates as an HTTP CONNECT proxy on port 3128 — clients must set `HTTPS_PROXY`. It does **not** support the DNS interception model (listening on port 443, routing by SNI). We need a true HTTPS reverse proxy instead.

## Solution: nginx-based caching reverse proxy container

Build a small Docker image with nginx configured as:
- HTTPS listener on port 443
- SNI-based upstream selection (docker.io, ghcr.io, quay.io, etc.)
- TLS termination with a generated CA cert + per-upstream certificates
- `proxy_cache` for caching registry responses (blobs cached permanently, manifests with TTL)
- TLS re-encryption to upstream registries

### Docker registry caching semantics

- `GET /v2/*/blobs/sha256:*` — **cache indefinitely** (content-addressed, immutable)
- `GET /v2/*/manifests/*` — **cache with short TTL** (e.g., 1h; tags can be re-pointed)
- `GET /v2/` and `/token` — **do not cache** (auth/version check)

## Implementation Plan

### Step 1: Create the caching proxy Docker image

New directory: `evetest/registry-cache/`

**`evetest/registry-cache/Dockerfile`**: Alpine + nginx + openssl + entrypoint script

**`evetest/registry-cache/entrypoint.sh`**:
1. Generate a CA key+cert if not already present in `/ca/` (persisted via volume mount)
2. For each registry in `$REGISTRIES`, generate a server cert signed by the CA
3. Generate nginx config from `$REGISTRIES` list:
   - One `server` block per registry hostname
   - `server_name` matches the hostname
   - `ssl_certificate` / `ssl_certificate_key` use the per-registry cert
   - `proxy_pass https://<real-registry-ip>;` with `proxy_ssl_server_name on`
   - `proxy_cache` directives with rules based on URL path
4. Start nginx

**`evetest/registry-cache/nginx.conf.template`**: Template for nginx config, expanded by entrypoint.

Key nginx config elements per registry:
```nginx
proxy_cache_path /cache levels=1:2 keys_zone=registry:10m max_size=32g inactive=30d;

server {
    listen 443 ssl;
    server_name docker.io *.docker.io;
    ssl_certificate     /certs/docker.io.crt;
    ssl_certificate_key /certs/docker.io.key;

    # Cache blobs permanently (content-addressed, immutable)
    location ~ ^/v2/.*/blobs/ {
        proxy_pass https://registry-1.docker.io;
        proxy_ssl_server_name on;
        proxy_cache registry;
        proxy_cache_valid 200 30d;
        proxy_cache_key $uri;
    }

    # Cache manifests with short TTL
    location ~ ^/v2/.*/manifests/ {
        proxy_pass https://registry-1.docker.io;
        proxy_ssl_server_name on;
        proxy_cache registry;
        proxy_cache_valid 200 1h;
        proxy_cache_key $uri;
    }

    # Don't cache auth/token endpoints
    location / {
        proxy_pass https://registry-1.docker.io;
        proxy_ssl_server_name on;
    }
}
```

Note: Each registry has a different actual hostname for its API (e.g., `docker.io` → `registry-1.docker.io`, `ghcr.io` → `ghcr.io`). The entrypoint needs a mapping.

### Step 2: Add Makefile targets

Add to `evetest/Makefile`:

```makefile
REGISTRY_CACHE_DIR ?= /tmp/evetest/registry-cache
REGISTRY_CACHE_REGISTRIES ?= docker.io ghcr.io quay.io registry.k8s.io gcr.io

build-registry-cache:
	docker build -t evetest-registry-cache evetest/registry-cache/

start-registry-cache: build-registry-cache
	docker run -d --name evetest-registry-cache \
		-p 443 \
		-v $(REGISTRY_CACHE_DIR)/ca:/ca \
		-v $(REGISTRY_CACHE_DIR)/cache:/cache \
		-e REGISTRIES="$(REGISTRY_CACHE_REGISTRIES)" \
		evetest-registry-cache
	@echo "CA cert: $(REGISTRY_CACHE_DIR)/ca/ca.crt"
	@echo "Use: export EVETEST_BROKER_PROXY_CA_CHAIN=$(REGISTRY_CACHE_DIR)/ca/ca.crt"

stop-registry-cache:
	docker rm -f evetest-registry-cache 2>/dev/null || true
```

### Step 3: Wire `--add-host` into the evetest container launch

In the `evetest` Makefile target, when the `evetest-registry-cache` container is running, auto-detect its IP and generate `--add-host` flags for each registry:

```makefile
$(eval REGISTRY_CACHE_HOSTS :=)
$(if $(shell docker ps -q -f name=evetest-registry-cache 2>/dev/null), \
    $(eval CACHE_IP := $(shell docker inspect -f '{{range .NetworkSettings.Networks}}{{.IPAddress}}{{end}}' evetest-registry-cache)) \
    $(foreach reg,docker.io ghcr.io quay.io registry.k8s.io gcr.io, \
        $(eval REGISTRY_CACHE_HOSTS += --add-host=$(reg):$(CACHE_IP)) \
    ) \
)
```

Then add `$(REGISTRY_CACHE_HOSTS)` to the `docker run` command.

### Step 4: Auto-detect `EVETEST_BROKER_PROXY_CA_CHAIN`

When the cache is running but `EVETEST_BROKER_PROXY_CA_CHAIN` is not set, auto-set it:

```makefile
ifndef EVETEST_BROKER_PROXY_CA_CHAIN
$(if $(shell docker ps -q -f name=evetest-registry-cache 2>/dev/null), \
    $(eval EVETEST_BROKER_PROXY_CA_CHAIN := $(REGISTRY_CACHE_DIR)/ca/ca.crt) \
)
endif
```

### Step 5: Document in README.md

Add a "Registry Cache" section explaining setup and usage.

## Files to Create/Modify

| File | Change |
|------|--------|
| `evetest/registry-cache/Dockerfile` | **New** — Alpine + nginx + openssl |
| `evetest/registry-cache/entrypoint.sh` | **New** — CA gen, cert gen, nginx config gen, start nginx |
| `evetest/registry-cache/nginx.conf.template` | **New** — nginx config template |
| `evetest/Makefile` | Add cache targets + `--add-host` wiring + auto-detect CA chain |
| `evetest/README.md` | Document registry cache setup |

## Verification

1. `make -C evetest start-registry-cache`
2. Check CA cert exists: `ls /tmp/evetest/registry-cache/ca/ca.crt`
3. `make -C evetest evetest NAME=TestLocalNI` — should auto-detect cache, add `--add-host` flags, set CA chain
4. Check proxy logs: `docker logs evetest-registry-cache` — should show requests and cache hits/misses
5. Run same test again — image download should be faster, logs show cache HITs
6. Inspect cache: `du -sh /tmp/evetest/registry-cache/cache/`
