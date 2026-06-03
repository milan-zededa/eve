# EVE DNS Resolver — Design and Implementation Plan

## Problem

EVE can have many management ports (eth0, wlan0, wwan0 …), each with its own DNS servers.
`/etc/resolv.conf` is written by NIM with all servers interleaved, but Go's `net.Resolver`
silently discards every server past the third (POSIX historical limit). The same
`net.Resolver{Dial: …}` pattern is copy-pasted in four places — `controllerconn`,
`portprober`, `diag`, and `nettrace` (vendored) — so all four paths share the bug.

A second, related problem: Go's resolver caches the parsed resolv.conf and only re-reads
it when both 5 seconds have elapsed **and** the file's mtime has changed
(`resolvConf.tryUpdate` in `dnsclient_unix.go`). In the past this stale-cache window
caused EVE to conclude too quickly that a port had no DNS servers — the file had just been
rewritten by NIM but the Go resolver had not yet picked up the new content. Using
`DeviceNetworkStatus` as the authoritative source of DNS servers eliminates this race
entirely: the in-memory status is always current.

A third issue concerns DNS search domains. `LinuxNetworkMonitor.parseDNSInfo` reads the
per-interface resolv.conf files written by dhcpcd (e.g.
`/run/dhcpcd/resolv.conf/<ifname>.dhcp`) and extracts the full `search` list into
`DNSInfo.Domains []string`. However, these domains are **never propagated to
`/etc/resolv.conf`** by NIM, so Go's resolver never applies them. Only the first domain
is reported to the controller (via `NetworkPortStatus.DomainName`, a single `string`);
the rest are silently discarded. As a result, DHCP-provided search domains have no
effect on any DNS resolution performed by EVE today. The new resolver should apply the
complete per-port search domain list for FQDN expansion, which requires extending
`NetworkPortStatus.DomainName` to a `SearchDomains []string` slice and populating it
from the full `DNSInfo.Domains` list in `dpcmanager.getDNSInfo`.

A wider fix is needed: currently `ResolverWithLocalIP` (controllerconn) and
`dnsResolver` (portprober) both wrap `net.Resolver` with a custom `Dial` hook. That hook
only controls **how to connect** to each server — the server list still comes from
resolv.conf and is always capped at 3. There is no supported way in Go to inject a custom
server list into `net.Dialer.Resolver`.

---

## Goal

Ensure that **EVE management traffic** (controller connectivity, EVE/app image download,
port probing, diagnostics) is no longer subject to the 3-nameserver resolv.conf limit,
the stale-cache race, or the missing search domain propagation described above.
The two options below differ in how they achieve this and in how they
affect `/etc/resolv.conf` for non-Go binaries (shell tools, diagnostics run over SSH).

---

## Option A — `pkg/pillar/dialer` Package (Recommended)

### Rationale for bypassing `net.Dialer.Resolver`

Go's `net.Resolver.Dial` function only lets us control the transport for connecting to each
individual DNS server. The server list itself is always read from `/etc/resolv.conf` by
Go's internal parser, capped at 3. No field or hook exists to override this list.

Consequently, **the correct pattern is to resolve the hostname first with our own code, then
pass the resulting IP address to the TCP connect step**. This is not a hack — it is the
same split (resolve then connect) that Go performs internally; we are just doing it ourselves
with a complete server list.

Once we pre-resolve, we must also own the dial loop, because a single
`net.Dialer.DialContext(ip:port)` call skips Happy Eyeballs and per-address time-slicing.
(RFC 6724 sorting is a resolver concern — Go's stdlib sorts inside `goLookupIPCNAMEOrder`
before returning the address list, so the dialer receives an already-sorted slice.) Those behaviours are documented in detail in the reference section
below. Placing the complete resolve-then-dial primitive in one package means every call site
gets the full correct behaviour for free.

**Effect on `/etc/resolv.conf`:** NIM continues to write all DNS servers interleaved
across interfaces, unchanged. Non-Go binaries (command-line tools etc.) read the file directly
and remain subject to the 3-nameserver limitation — accepted, as this only affects
interactive tooling.

### New package: `pkg/pillar/dialer`

```
pkg/pillar/dialer/
    resolver.go      — Resolver type: LookupIPAddr, LookupIP
    resolver_test.go — unit tests with in-process miekg/dns server
    dialer.go        — Dialer type: DialContext (resolve + Happy Eyeballs + serial loop)
    dialer_test.go   — unit tests with mock DNS + mock TCP listener
```

---

### `resolver.go` — DNS resolution

```go
// LookupResult is a resolved address with its DNS TTL.
// Replaces controllerconn.DNSResponse across the codebase.
type LookupResult struct {
    IP  net.IP
    TTL uint32
}

// Resolver resolves hostnames using an explicit list of DNS servers via miekg/dns.
// resolv.conf is never read; no 3-server cap applies.
type Resolver struct {
    IfName           string        // used only in error reporting
    // LocalAddr mirrors net.Dialer.LocalAddr: its IP is bound as the source address
    // on every outgoing DNS query socket. EVE uses this for source-based routing.
    LocalAddr        net.Addr
    DNSServers       []net.IP      // explicit server list; empty → DNSNotAvailError
    SearchDomains    []string      // per-port search domains from DHCP (NetworkPortStatus.SearchDomains)
    DNSServerTimeout time.Duration // timeout per DNS server per attempt; 0 → 5 s (Go stdlib default)
    DNSAttempts      int           // retry rounds over all servers; 0 → 5 (EVE default, matching
                                   // the value NIM writes to resolv.conf:
                                   // dpcreconciler/genericitems/resolvconf.go:160)
}

// LookupIPAddr resolves host. Drop-in for net.Resolver.LookupIPAddr.
func (r *Resolver) LookupIPAddr(ctx context.Context, host string) ([]net.IPAddr, error)

// LookupIP resolves host filtered by network ("ip", "ip4", "ip6").
// Drop-in for net.Resolver.LookupIP.
func (r *Resolver) LookupIP(ctx context.Context, network, host string) ([]net.IP, error)

// LookupIPAddrWithTTL resolves host and returns TTLs alongside the addresses.
// Used by callers that schedule cache refresh based on DNS TTL (e.g. nim resolver cache).
func (r *Resolver) LookupIPAddrWithTTL(ctx context.Context, host string) ([]LookupResult, error)

// LookupAcrossPorts resolves host by querying all management ports in dns simultaneously,
// one goroutine per (localAddr, dnsServer) pair (same semantics as the deleted
// controllerconn.ResolveWithPortsLambda). Returns the first successful result.
// Falls back to net.LookupIP when dns contains no configured DNS servers.
func LookupAcrossPorts(ctx context.Context, host string, dns types.DeviceNetworkStatus) ([]LookupResult, error)
```

`Resolver` internals:
- Check `/etc/hosts` first (same as Go stdlib).
- Skip loopback DNS servers. If all servers are loopback or empty → `types.DNSNotAvailError{IfName}`.
- Filter servers by IP version relative to the IP extracted from `LocalAddr`.
- Build the FQDN list from `host` and `SearchDomains` using the same `nameList` logic
  as the Go stdlib (ndots=1 default: if host has ≥1 dot, try bare name first then search
  domains; otherwise try search domains first then bare name).
- For each FQDN:
  - Launch A and AAAA queries **in parallel** (two goroutines per FQDN).
  - Each query iterates servers sequentially with a per-server timeout of
    `DNSServerTimeout` (default 5 s), retrying up to `DNSAttempts` rounds (default 5).
  - UDP first; if TC=1 returned, retry same server over TCP.
  - `NXDOMAIN` stops the server loop immediately.
- Sort combined results by RFC 6724 (same algorithm as Go stdlib).
- Return on first FQDN that yields addresses; return `DNSNotAvailError` only when no
  server was reachable at all, otherwise return the DNS error.

`LookupAcrossPorts` internals:
- Iterates management ports; for each port collects all global-unicast addresses,
  all DNS servers, and the port's `SearchDomains`.
- Spawns one goroutine per (localAddr, dnsServer) pair using
  `Resolver{LocalAddr: &net.UDPAddr{IP: addr}, DNSServers: []net.IP{dnsServer}, SearchDomains: port.SearchDomains}.LookupIPAddrWithTTL`.
- Up to `DNSMaxParallelRequests` (5) goroutines run simultaneously.
- Returns on first success; accumulates errors and returns them all on total failure.
- When no DNS servers are configured across any port, falls back to `net.LookupIP`.

`resolver_test.go`: start an in-process `miekg/dns` server on a random loopback port
serving synthetic A/AAAA records; cover >3 servers, IP-version filtering, empty server
list, all-servers-fail, NXDOMAIN, TC=1 UDP→TCP fallback, `LookupAcrossPorts` parallel
fan-out and first-success semantics.

**Open-source search result:** `github.com/miekg/dns v1.1.43` is already a direct
dependency. No existing higher-level wrapper provides per-interface source-IP binding AND
explicit server list injection. No new dependencies are needed.

---

### `dialer.go` — resolve + dial

```go
// Dialer wraps Resolver and adds a full DialContext that replicates net.Dialer behaviour:
// IPv4/IPv6 partitioning, Happy Eyeballs, and a serial per-address connect loop with
// proportional time-slicing. RFC 6724 sorting is done by the Resolver before the
// address list reaches the dial loop.
type Dialer struct {
    Resolver                    // embeds LocalAddr, IfName, DNSServers, SearchDomains, DNSServerTimeout, DNSAttempts
    FallbackDelay time.Duration // 0 → 300 ms; negative → disable Happy Eyeballs
}

// DialContext resolves address and connects, replicating net.Dialer.DialContext behaviour.
// If address is already an IP literal, DNS is skipped.
// Resolver.LocalAddr is bound on every TCP connect (source-based routing).
func (d *Dialer) DialContext(ctx context.Context, network, address string) (net.Conn, error)
```

`DialContext` internal steps (see Reference section for detail on each):

1. Parse host and port from address. If host is a bare IP, skip to step 3.
2. Call `d.LookupIPAddr(ctx, host)` — A + AAAA in parallel, all DNS servers, no resolv.conf.
   Results are already sorted by RFC 6724 by the resolver.
3. Partition into primary/fallback groups by the first address's IP family.
5. Happy Eyeballs: start primary `dialSerial` goroutine; start fallback goroutine after
   `FallbackDelay` (300 ms default); first success wins, other is cancelled.
6. `dialSerial`: iterate addresses one at a time, dividing remaining time equally
   (`max(remaining/count, 2s)` per address), one TCP connect attempt per address,
   return first success or first error.
7. Each connect: `net.Dialer{LocalAddr: d.LocalAddr}.DialContext(dialCtx, network, ip:port)`.

`dialer_test.go`: in-process DNS server returning multiple A + AAAA records; in-process
TCP listener; verify Happy Eyeballs timer fires, per-address timeout slicing, first-success
wins, first-error returned on total failure.

---

### Integration — call sites

#### `pkg/pillar/portprober/linuxreachprober.go`

Delete the `dnsResolver` type (lines 153–202).

**ICMP probe** — uses `Resolver` (DNS lookup only, no TCP connect):
```go
r := &dialer.Resolver{LocalAddr: &net.UDPAddr{IP: srcIP}, DNSServers: dnsServers, IfName: portIfName}
ips, err := r.LookupIP(ctx, "ip", addr.Hostname)
```

**TCP probe** — uses `Dialer` (replaces the single `net.Dialer.DialContext` call):
```go
d := &dialer.Dialer{Resolver: dialer.Resolver{LocalAddr: &net.TCPAddr{IP: srcIP}, DNSServers: dnsServers, IfName: portIfName}}
conn, err := d.DialContext(ctx, "tcp", dstAddr.String())
if err == nil { _ = conn.Close() }
```

#### `pkg/pillar/cmd/diag/diag.go`

`tryLookupIP`: delete the inline `resolverDial` closure and `net.Resolver` construction.
Replace with:
```go
r := &dialer.Resolver{LocalAddr: &net.UDPAddr{IP: localAddr}, DNSServers: dnsServers, IfName: ifname}
ips, err := r.LookupIPAddr(context.Background(), ctx.serverName)
```

#### `pkg/pillar/controllerconn/resolver.go`

`DialerWithResolverCache` is simplified:

1. Add `dnsServers []net.IP` field.
2. Replace `dialWithCustomDNS` + `ResolverWithLocalIP` + `net.Resolver` with
   `dialer.Dialer.DialContext` unconditionally:
   ```go
   d := &dialer.Dialer{Resolver: dialer.Resolver{
       IfName: d.ifName, LocalAddr: &net.TCPAddr{IP: d.localIP}, DNSServers: d.dnsServers,
   }}
   conn, err := d.DialContext(ctx, network, address)
   ```
   `dialer.Dialer` returns `types.DNSNotAvailError` directly when `dnsServers` is empty
   and a hostname needs resolving, so the `dialRequested`/`dnsWasAvail` flag mechanism in
   `ResolverWithLocalIP` is no longer needed.
   The `SendLocal`/`OpenLocalStream` paths always pass IP-literal URLs (the caller
   substitutes the resolved IP before calling — see `localcommand/agent.go`), so DNS is
   never triggered there and the empty-`dnsServers` case is a non-issue.
3. Remove the file-level TODO comment (lines 4–27).

#### `pkg/pillar/controllerconn/send.go`

Pass the already-computed `dnsServers` when constructing `DialerWithResolverCache`
(non-tracing path, line 776):
```go
dialer := &DialerWithResolverCache{
    // ... existing fields ...
    dnsServers: dnsServers, // add this
}
```

#### `pkg/pillar/cmd/nim/resolvercache.go`

Delete `doDNSQuery` — it is only called from `resolveAndCacheIP` and is a trivial
one-liner wrapper. Call `dialer.LookupAcrossPorts` directly inside `resolveAndCacheIP`:

```go
func (n *nim) resolveAndCacheIP(hostname string) (minTTL uint32) {
    queryTime := time.Now()
    results, errs := dialer.LookupAcrossPorts(ctx, hostname, n.dpcManager.GetDNS())
    if len(errs) > 0 {
        n.Log.Warnf("resolveAndCacheIP: DNS failed: %+v", errs)
    }
    cachedData := types.CachedResolvedIPs{Hostname: hostname}
    for _, r := range results {
        ttl := r.TTL
        if ttl == 0 {
            ttl = defaultTTL
        }
        cachedData.CachedIPs = append(cachedData.CachedIPs, types.CachedIP{
            IPAddress:  r.IP,
            ValidUntil: queryTime.Add(time.Duration(ttl) * time.Second),
        })
        if minTTL == 0 || ttl < minTTL {
            minTTL = ttl
        }
    }
    // ... publish cachedData unchanged ...
}
```

`ResolveCacheWrap` is not needed here — nim already manages its own TTL-driven
refresh loop via pubsub.

#### `pkg/pillar/cmd/zedrouter/networkinstance.go`

`attachNTPServersToPortConfigs` replaces `ResolveWithPortsLambda` +
`ResolveCacheWrap` with `dialer.LookupAcrossPorts`:

```go
results, err := dialer.LookupAcrossPorts(ctx, ntpServer.String(), *z.deviceNetworkStatus)
for _, r := range results {
    ntpServers = append(ntpServers, r.IP)
}
```

`ResolveCacheWrap` provided an in-process DNS cache to avoid redundant queries
across reconciliation cycles. Replace it with a simple `map[string][]net.IP` on
the `zedrouter` struct, populated on first resolution and invalidated when
`DeviceNetworkStatus` changes.

#### `pkg/pillar/vendor/…/zedUpload/commonutils.go`

Used by the downloader to fetch app and EVE images. Contains the same
`net.Resolver{Dial: resolverDial}` pattern on the non-tracing path (lines 191–197):
it binds the source IP but leaves the server list to resolv.conf (max 3). The tracing
path uses `nettrace.HTTPClientCfg` with `SourceIP` only — no `SkipNameserver` and no
`HostnameResolver` — so it also has the 3-server limit.

Fix: `zedUpload` is an `eve-libs` package and cannot import `pillar/dialer`, so the
same hook pattern is used. Add a `DialContext` field to `httpClientWrapper` and a
corresponding `withDialContext` setter:

```go
// DialContext, when set, overrides the transport's dial function.
// The downloader passes dialer.Dialer.DialContext here so that DNS uses the
// full per-interface server list instead of resolv.conf.
DialContext func(ctx context.Context, network, address string) (net.Conn, error)
```

`withDialContext` **replaces** both `withSrcIP` and `withBindIntf`: since
`dialer.Dialer` already binds `LocalAddr` on every connection, the caller no longer
needs to set the source IP separately on the wrapper.

Non-tracing path: use `c.DialContext` as `http.Transport.DialContext` when set;
otherwise fall back to the current `net.Dialer` logic (backward compatible).

Tracing path: already picks up the `HostnameResolver` hook added to `nettrace.HTTPClientCfg`
(see below); set it from the downloader side the same way as `controllerconn` does.

In `cmd/downloader` (pillar): when constructing `httpClientWrapper`, populate both fields:
```go
d := &dialer.Dialer{Resolver: dialer.Resolver{LocalAddr: &net.TCPAddr{IP: srcIP}, DNSServers: dnsServers}}
wrapper.withDialContext(d.DialContext)
// for the tracing path, passed into nettrace.HTTPClientCfg:
cfg.HostnameResolver = d.LookupIPAddr
```

This change is made to the vendored copy and should be upstreamed to `lf-edge/eve-libs`
as a follow-up.

#### Nettrace vendored library

`pkg/pillar/vendor/github.com/lf-edge/eve-libs/nettrace/` also uses
`net.Resolver{Dial: tr.dial}` (dial.go:194), sharing the same bug on the tracing path.

Fix: add a `HostnameResolver` function field to `HTTPClientCfg` (httpclient.go). Using a
function avoids an import cycle (nettrace cannot import `pillar/dialer`):

```go
// HostnameResolver overrides the default net.Resolver (which reads /etc/resolv.conf,
// capped at 3 servers). Signature matches net.Resolver.LookupIPAddr so that
// dialer.Resolver.LookupIPAddr can be passed directly as a method value.
HostnameResolver func(ctx context.Context, host string) ([]net.IPAddr, error)
```

In `tracedDialer.dial` (dial.go): when `HostnameResolver` is set and the address contains
a hostname, call it to get IPs, emit DNS trace events per result, try dialing each IP.
Fall through to the existing `net.Resolver` path when not set (backward compatible).

In `controllerconn/send.go`, tracing path:
```go
r := &dialer.Resolver{LocalAddr: &net.UDPAddr{IP: srcIP}, DNSServers: dnsServers, IfName: intf}
clientCfg.HostnameResolver = r.LookupIPAddr
```

This change is made to the vendored copy and should be upstreamed to `lf-edge/eve-libs`
as a follow-up.

### Files Changed

| File | Change |
|------|--------|
| `pkg/pillar/dialer/resolver.go` | New — `Resolver` type + `LookupIPAddr` + `LookupIP` |
| `pkg/pillar/dialer/resolver_test.go` | New — unit tests with in-process DNS server |
| `pkg/pillar/dialer/dialer.go` | New — `Dialer` type + `DialContext` |
| `pkg/pillar/dialer/dialer_test.go` | New — unit tests with mock DNS + TCP listener |
| `pkg/pillar/portprober/linuxreachprober.go` | Delete `dnsResolver`; use `dialer.Resolver` + `dialer.Dialer` |
| `pkg/pillar/cmd/diag/diag.go` | Replace inline resolver in `tryLookupIP` with `dialer.Resolver` |
| `pkg/pillar/controllerconn/resolver.go` | Add `dnsServers`; replace manual loop with `dialer.Dialer`; remove TODO |
| `pkg/pillar/controllerconn/send.go` | Pass `dnsServers` (non-tracing) + `HostnameResolver` (tracing) |
| `pkg/pillar/vendor/…/nettrace/httpclient.go` | Add `HostnameResolver` field to `HTTPClientCfg` |
| `pkg/pillar/vendor/…/nettrace/dial.go` | Use `HostnameResolver` in `tracedDialer.dial` when set |
| `pkg/pillar/vendor/…/zedUpload/commonutils.go` | Add `DialContext` hook to `httpClientWrapper`; use it in non-tracing transport |
| `pkg/pillar/cmd/downloader/…` | Populate `DialContext` + `HostnameResolver` with `dialer.Dialer` |
| `pkg/pillar/cmd/nim/resolvercache.go` | Delete `doDNSQuery`; call `dialer.LookupAcrossPorts` directly |
| `pkg/pillar/cmd/zedrouter/networkinstance.go` | Replace `ResolveWithPortsLambda` + `ResolveCacheWrap` with `dialer.LookupAcrossPorts` |
| `pkg/pillar/controllerconn/resolver.go` | Delete `DNSResponse`, `ResolveWithSrcIP[WithTimeout]`, `ResolveWithSrcIPFunc`, `ResolveCacheWrap`, `ResolveWithPortsLambda`, and their supporting cache types |
| `pkg/pillar/types/dns.go` | Extend `NetworkPortStatus.DomainName string` to `SearchDomains []string` |
| `pkg/pillar/dpcmanager/dns.go` | Store all `DNSInfo.Domains` into `NetworkPortStatus.SearchDomains` instead of only the first |

What does NOT change: `cmd/downloader/mdns.go` (mDNS / zeroconf — different protocol).

---

## Option B — dnsmasq as DNS Forwarder

### Context: dnsmasq is already used for apps

EVE already runs a per-NI dnsmasq instance for every Local Network Instance. Each
instance forwards DNS queries to the upstream DNS servers of the NI's uplink ports. Apps
on the NI resolve hostnames through their NI's dnsmasq without any notion of port cost
or per-port isolation — dnsmasq simply queries all configured upstream servers and
returns the first answer. This already works correctly for app traffic.

### Proposal: a management dnsmasq instance

Run a dedicated dnsmasq instance for management traffic (separate from per-NI instances),
listening on a loopback address (e.g. `127.0.0.53:53`). Configure it with all mgmt port
DNS servers, with servers from lower-cost ports listed first and `strict-order` enabled
so dnsmasq tries them in order before falling back to higher-cost ones:

```
# eth0 (cost 0) — tried first
server=10.156.8.4@eth0
server=10.152.16.4@eth0
# wlan0 (cost 1) — tried if eth0 servers fail
server=192.168.1.1@wlan0
# wwan0 (cost 2) — last resort
server=172.26.38.1@wwan0
options strict-order
```

`/etc/resolv.conf` lists `127.0.0.53` as the sole nameserver. All Go resolvers, nettrace,
third-party libraries, and command-line tools pick this up without any code changes.

**IPv6-only uplinks are fully supported without listening on `::1`.** DNS is decoupled
from the upstream connectivity protocol: local clients always reach dnsmasq over IPv4
loopback (`127.0.0.53:53`), while dnsmasq forwards queries to upstream servers using
whatever IP version those servers use (e.g. `server=2001:4860:4860::8888@eth0` for an
IPv6-only port). This is the standard approach used by systemd-resolved, macOS
mDNSResponder, and OpenWrt — a single IPv4 loopback nameserver entry in resolv.conf
covers all uplink scenarios.

NIM generates and hot-reloads this config whenever `DeviceNetworkStatus` changes, the
same way it already manages per-NI dnsmasq configs.

**Effect on `/etc/resolv.conf`:** NIM writes a single entry pointing to the mgmt
dnsmasq (`nameserver 127.0.0.53`). Non-Go binaries resolve via the mgmt dnsmasq,
which carries DNS servers from **management ports only**. This is consistent with
today's behaviour: `dpcreconciler/linux.go` already filters to `port.IsMgmt` ports
when generating resolv.conf, so app-shared-only ports' DNS servers are already
excluded. No behavioural change for command-line tools.

### Impact on the mgmt connection loop

The current loop:
```
for every mgmt port in cost-ascending order:
    resolve hostname AND connect using this port only
    break if success
```

With mgmt dnsmasq, resolution is decoupled from the port being tested:
```
for every mgmt port in cost-ascending order:
    resolve hostname via mgmt dnsmasq
      (prefers lower-cost port DNS servers via strict-order, but may use any mgmt port)
    connect to resolved IP using net.Dialer{LocalAddr: port.LocalAddr}
    (i.e. non-DNS traffic still uses only the selected port)
    break if success
```

For general mgmt traffic this is acceptable: the IP returned by dnsmasq is correct
regardless of which port's DNS server answered, and the TCP connection is still bound
to the correct port's source IP via `LocalAddr`.

### DPC verification and portprober

DPC verification and the portprober need to check whether a **specific port** works.
With dnsmasq, DNS resolution cannot be forced to use only one port's upstream servers.
Two sub-cases:

**Pure IP connectivity checks** (ping, TCP handshake to a known IP): no DNS involved.
`net.Dialer{LocalAddr: port.LocalAddr}` is sufficient and unchanged.

**Hostname-based checks** (e.g. checking that the controller is reachable by name via
a given port): resolution goes through the mgmt dnsmasq, which may use any mgmt port's
DNS servers. This is intentional — verification should reflect how real traffic works.
If normal mgmt traffic resolves via dnsmasq, verification must do the same; using a
per-port custom resolver in verification only would produce results that don't match
production behaviour, making the verification misleading.

However, dnsmasq caches DNS responses with their original TTL. If a previous DPC already
resolved the controller hostname, that cached answer would be served to the next DPC
verification pass — even if the new DPC's upstream DNS servers cannot resolve it. To
prevent this, **the mgmt dnsmasq DNS cache must be cleared before each DPC verification
pass**. dnsmasq clears its cache on `SIGHUP`. This is implemented declaratively: a
`CacheClearCounter` field on the `MgmtDnsmasq` dep-graph item is incremented by
`DpcManager` at the start of every `restartVerify()` call; the reconciler detects the
change, calls `Modify()`, which writes the (unchanged) config and sends `SIGHUP`.

**portprober for NI-attached (app-shared) ports**: must use the NI's dnsmasq
(not the mgmt dnsmasq) to properly test NI upstream connectivity. Since
`net.DefaultResolver` reads `/etc/resolv.conf` → mgmt dnsmasq, the portprober
needs a way to redirect DNS to the NI's bridge IP.

Solution: replace the portprober's `dnsServers []net.IP` parameter with a
`*net.Resolver`. The caller constructs the resolver with a custom `Dial` that
ignores the address from resolv.conf and always connects to the target dnsmasq:

```go
// redirect all DNS to this dnsmasq instance (no source IP binding needed —
// the bridge IP is directly reachable from pillar's host namespace)
niDNSAddr := net.JoinHostPort(niBridgeIP.String(), "53")
resolver := &net.Resolver{
    PreferGo: true,
    Dial: func(ctx context.Context, network, _ string) (net.Conn, error) {
        return (&net.Dialer{}).DialContext(ctx, network, niDNSAddr)
    },
}
```

The caller (dpcmanager / portprober manager) picks the right dnsmasq address:
- Mgmt-only port → `127.0.0.53:53` (mgmt dnsmasq)
- NI-attached port → `<NI bridge IP>:53` (per-NI dnsmasq)

For the TCP probe: `net.Dialer{LocalAddr: srcAddr, Resolver: resolver}.DialContext(...)`.
For the ICMP probe: `resolver.LookupIP(ctx, "ip", hostname)`.

This is the same `net.Resolver{PreferGo: true, Dial: ...}` pattern the portprober
already uses — just with a simpler `Dial` function that redirects to a known address
instead of filtering a server list.

An important advantage over a custom resolver (Option A): portprober tests DNS
**exactly as apps experience it** — it queries the NI's dnsmasq and lets dnsmasq
perform the upstream resolution, the same path apps in the NI take. A failure in
dnsmasq's upstream resolution shows up in both the portprober result and in app
traffic, making the probe a faithful end-to-end test rather than a synthetic one.

### Search domains

dnsmasq does **not** perform FQDN expansion — it forwards whatever FQDN the client
sends without appending search domains. Search domain expansion is strictly
client-side: the resolver reads `search` from `/etc/resolv.conf` and appends domains
before sending the query.

For Option B, NIM writes both `nameserver 127.0.0.53` and a `search` line to
`/etc/resolv.conf`. The `search` line contains all unique search domains from all
management ports, space-separated on a single line (multiple `search` lines are not
used — each would replace the previous in Go's parser).

Go's resolver reads all search domains from the `search` line without any cap —
unlike nameservers (hard limit of 3), the `search` parser in `dnsconfig_unix.go`
appends every domain unconditionally. The traditional POSIX/glibc limit of 6 search
domains is not enforced by Go.

NIM generates the mgmt dnsmasq config using the following algorithm, which gives both
per-search-domain routing and cost-ordered fallback for all other queries:

```
# 1. Split-horizon entries:
#    for every port in cost-ascending order:
#      for every DNS server of that port:
#        for every search domain of that port:
#          add server entry with conditional forwarding syntax
server=/corp.example.com/10.0.0.1@eth0   # eth0 cost 0, server 1
server=/corp.example.com/10.0.0.2@eth0   # eth0 cost 0, server 2
server=/home.local/192.168.1.1@wlan0     # wlan0 cost 1
server=/office.net/172.26.38.1@wwan0     # wwan0 cost 2

# 2. Default upstream servers:
#    for every port in cost-ascending order:
#      for every DNS server of that port:
#          add server entry
server=10.0.0.1@eth0      # eth0 cost 0, server 1
server=10.0.0.2@eth0      # eth0 cost 0, server 2
server=192.168.1.1@wlan0  # wlan0 cost 1
server=172.26.38.1@wwan0  # wwan0 cost 2

strict-order
no-resolv
```

**Parallelism within a port:** dnsmasq does not support multiple IPs on a single
`server=` line. With `strict-order`, servers within the same port are tried
**sequentially** in config-file order. `--all-servers` would parallelise queries to
all servers simultaneously but abandons cost ordering entirely — not suitable here.
In practice, ports typically have 1–2 DNS servers, so the overhead of trying them
sequentially is at most one extra DNS round-trip before moving to the next port.

Behaviour:
- Query for `machine.corp.example.com.` (client-expanded from search) → matched by
  split-horizon entry → routed to eth0's servers in config order.
- Query for `zedcloud.net` (FQDN, no search domain match) → falls through to default
  entries → eth0 first (cost 0), then wlan0, then wwan0 via `strict-order`.

**Ordering and failover are correct.** From `domain-match.c` (`order_qsort`,
`order_servers`, `order`):

- Primary sort key: **domain length descending** — longer (more specific) domains
  sort before shorter ones, then **case-insensitive lexicographic** for equal-length
  domains.
- Tiebreaker for entries sharing the exact same domain: **`serial`** — the order of
  appearance in the config file.

Consequence: all `server=` entries for a given domain form a **contiguous,
isolated range** in the sorted array. `lookup_domain` returns only that range via
binary search + `filter_servers` (which extends using `order_servers() == 0`, true
only for identical domain strings). Two ports with equal-length but different search
domains are therefore never mixed — each has its own independent range.

Within a range, `strict-order` always starts at `first` (the lowest `serial`). Since
NIM writes the config in cost-ascending order, `serial` order = cost order, so the
lower-cost port's servers are tried first.

For the **default (no-domain) servers**: all have `domain_len = 0` and identical
comparison results, so `serial` is the only tiebreaker there too — same guarantee
applies.

### Code changes with Option B

**No `pillar/dialer` package is needed.** All DNS resolution goes through
`net.DefaultResolver` → resolv.conf → mgmt dnsmasq, and all TCP connections use a
plain `net.Dialer{LocalAddr: ...}`:
- **No custom resolver** — `ResolverWithLocalIP`, `dnsResolver` (portprober), and the
  inline resolver in `diag` are all deleted. DNS queries go to `127.0.0.53` (loopback)
  so source IP binding on the resolver socket is irrelevant and must not be set.
- **No custom dialer** — `net.Dialer{LocalAddr: ...}` is sufficient for TCP.
  `LocalAddr` is set only for the TCP connection to the external endpoint (source-based
  routing), not for DNS. `DialerWithResolverCache` is simplified to a plain `net.Dialer`.
- **No in-process DNS cache** — dnsmasq maintains its own TTL-based in-memory cache.
  Repeat queries within the TTL window are served from dnsmasq without hitting upstream
  servers. This makes the `CachedResolvedIPs` pubsub mechanism (NIM resolves hostnames
  in advance and publishes the results for other agents to use as a fast-path dialing
  shortcut) entirely redundant. It is deleted along with its consumers in `zedagent`,
  `loguploader`, `scepclient`, and `client`.
- `zedUpload`: `withSrcIP` and `withBindIntf` remain but are simplified — remove the
  `dialer.Resolver = resolver` block (lines 191–197), leaving only
  `dialer.LocalAddr = localTCPAddr`. No new API surface, no `withDialContext` needed.
- `nettrace`: **no API changes**. `SourceIP` in `HTTPClientCfg` is still set by the
  caller for source-based routing on the TCP connection socket. The `HostnameResolver`
  hook is not needed — nettrace's built-in `net.DefaultResolver` reads
  `/etc/resolv.conf` → mgmt dnsmasq transparently. Source IP binding on the DNS query
  socket is a non-issue: `tracedResolver.dial` (dial.go:215) already guards with
  `!ip.IsLoopback()` and skips source binding when the DNS server is on 127.0.0.x,
  so no code change is required.
- **Neither eve-libs library (`zedUpload`, `nettrace`) requires any API additions or
  vendored patches under Option B**, unlike Option A which needs `HostnameResolver` in
  nettrace and `withDialContext` / `DialContext` field in zedUpload.
- **New NIM code**: generate and manage the mgmt dnsmasq config (cost-ordered server
  list, `strict-order`, search domains, hot-reload on DNS change). See the section
  below for reuse analysis of the existing `Dnsmasq` item.

### Reuse of `nireconciler/genericitems.Dnsmasq`

`pkg/pillar/nireconciler/genericitems/dnsmasq.go` already implements a full
`Dnsmasq` config item and `DnsmasqConfigurator` (start/stop/SIGHUP/config-file
generation). The per-NI dnsmasq instances use it today. The question is whether NIM
can reuse it for the mgmt dnsmasq, or whether a separate item/configurator is needed.

**What fits without changes:**
- `DNSServer.UpstreamServers []UpstreamDNSServer` — each entry already carries both
  `IPAddress` and `Port.IfName` (`server=<ip>@<if>`), exactly the syntax needed for
  mgmt dnsmasq.
- `DnsmasqConfigurator`: start/stop via `proc.ProcessManager`, SIGHUP for hot-reload,
  PID file and config file management — all reusable verbatim.
- `DNSServer.StaticEntries` — not needed for mgmt, but harmless when empty.

**What is missing or NI-specific:**

| Gap | Impact |
|-----|--------|
| `strict-order` not emitted by `CreateDnsmasqConfig` | Needed for cost-ordered server preference; requires adding `StrictOrder bool` to `DNSServer` |
| `SearchDomains` not in `DNSServer` | Needed to write `search <domains>` to the dnsmasq config; requires adding `SearchDomains []string` to `DNSServer` |
| `DHCPServer` always present and included in `Equal`/`NeedsRecreate` | Mgmt dnsmasq has no DHCP; make `DHCPServer` a pointer (`*DHCPServer`), skip all DHCP config generation when nil |
| `ForNI uuid.UUID` in `Dnsmasq` | Semantically meaningless for mgmt; use `uuid.Nil` as the mgmt instance sentinel — no code change needed |
| `ListenIf.ItemRef` dependency check requires the interface to be a dep-graph item with `GetAssignedIPs()` | Mgmt dnsmasq listens on a loopback address; loopback is not a managed dep-graph item. Make the `MustSatisfy` check optional when `ItemRef` is zero |
| Config/PID file path uses `zedrouterRunDir = "/run/zedrouter"` | Acceptable for mgmt too — paths are per-`instanceName` and won't collide. Or make the run-dir configurable in `DnsmasqConfigurator` |
| Static dnsmasq config includes `quiet-dhcp`, `quiet-dhcp6`, `dhcp-ttl=600` | Harmless noise when DHCP is disabled, but slightly untidy |

**Recommended approach: a small NIM-specific item + configurator in `dpcreconciler`**

The mgmt dnsmasq config is vastly simpler than a per-NI one: no DHCP, no lease
files, no DHCP-host directories, no ipsets, no route propagation. The NIM-specific
code would be:

- **`MgmtDnsmasq` item struct** (~15 lines): listen address, cost-ordered upstream
  servers (`[]UpstreamDNSServer` reused from `nireconciler/genericitems`), search domains.
- **Config generation** (~30 lines): `no-resolv`, `bind-interfaces`, `strict-order`,
  `listen-address`, one `server=<ip>@<if>` line per upstream server, `search` line,
  `pid-file`.
- **`MgmtDnsmasqConfigurator`** (~40 lines): write config file, use
  `proc.ProcessManager` (already a shared utility) for start/SIGHUP/stop.

Total: ~100 lines in `pkg/pillar/dpcreconciler/genericitems/mgmtdnsmasq.go`.

This is smaller and cleaner than the alternative of moving the 1065-line
`nireconciler/genericitems/dnsmasq.go` to a shared package, making `DHCPServer`
optional, patching the dep-graph loopback check, and adding `StrictOrder`/
`SearchDomains` — all of which risks breaking the existing per-NI path.
The only shared piece is `proc.ProcessManager`, which is already available to both
packages.

---

## Trade-offs and Recommendation

| Aspect | Option B (dnsmasq) | Option A (pillar/dialer) |
|--------|-------------------|--------------------------|
| resolv.conf 3-server cap | Fixed | Fixed |
| resolv.conf stale-cache race | Fixed (dnsmasq is always current) | Fixed (bypasses resolv.conf) |
| Search domains | Fixed (dnsmasq split-horizon) | Fixed (Resolver.SearchDomains) |
| Per-port DNS isolation for mgmt | Lost (dnsmasq pools all mgmt servers) | Preserved |
| Per-port DNS isolation for DPC verification | Not preserved — by design: verification must match real traffic behaviour | Preserved |
| portprober (NI-attached ports) | Uses NI dnsmasq via `net.Resolver{Dial: redirect to bridge IP}` — portprober API changes from `dnsServers []net.IP` to `*net.Resolver` | Uses Resolver with explicit server list |
| Code to write and maintain | Small (NIM dnsmasq config gen) | Full pillar/dialer package |
| Third-party / command-line tools | All benefit automatically | No change (resolv.conf unchanged) |
| Operational dependency | mgmt dnsmasq process must run and be healthy | None |
| eve-libs changes (nettrace, zedUpload) | **None** — no API additions or vendored patches | Both need new hooks (`HostnameResolver`, `withDialContext`) + vendored patches |
| Testability | Requires mock dnsmasq in integration tests | Unit-testable with in-process DNS server |

Both options are viable. The choice is primarily a maintenance trade-off:

**Option A** (`pkg/pillar/dialer`) if the team prefers a fully custom, self-contained,
unit-testable package with no operational dependencies and perfect per-port DNS
isolation throughout.

**Option B** (mgmt dnsmasq) if minimising the amount of custom Go DNS/dial code to
maintain long-term is the priority. Every call site collapses to plain
`net.Dialer{LocalAddr: ...}` with zero resolver customisation; neither eve-libs
library needs any API change or vendored patch; the dnsmasq infrastructure already
exists in EVE; and verification correctly mirrors real traffic behaviour by
construction.

**Long-term alignment:** EVE's roadmap includes migrating to systemd.
`systemd-resolved` and dnsmasq are alternatives — both are DNS stub resolvers that
can serve on `127.0.0.53:53`. When the systemd migration happens, `systemd-resolved`
simply replaces the mgmt dnsmasq: NIM switches from writing a dnsmasq config file to
calling `resolvectl dns`/`resolvectl domain` (or the D-Bus API) per interface, and
`systemd-resolved` provides the same per-link DNS forwarding and search domain
routing. The rest of the codebase — `net.DefaultResolver` pointing to `127.0.0.53`,
plain `net.Dialer{LocalAddr: ...}` everywhere — stays completely unchanged.
Option A would require an additional migration step at that point.

## Verification

### Unit tests

(option A only)

```
GOTOOLCHAIN=local GOWORK=off go test ./pkg/pillar/dialer/...
GOTOOLCHAIN=local GOWORK=off go test ./pkg/pillar/controllerconn/... ./pkg/pillar/portprober/...
```

### Integration test — `TestDNSFunctionality`

`evetest/tests/networking/dns_test.go` already contains a purpose-built integration test
for this fix. It uses the `netmodels.ManyDNSServers` network model with four management
ports carrying 7 DNS servers in total (well above the historical 3-entry cap) and verifies
all four phases described in the test's header comment.

**Before the fix**, Phase 1 contains a commented-out block (lines 252–275) guarded by:

```go
/* TODO: this will be failing until we implement support in EVE
         for more than 3 DNS servers
    ...
*/
```

**After the fix**, uncomment that block. It asserts:
- `eth0`, `eth1`, `eth2` have empty `DevicePort.err` — confirming that ports whose DNS
  servers previously fell past position 3 in resolv.conf are no longer errored.
- `eth3` has a non-empty `DevicePort.err` that does **not** contain
  `"no DNS server available"` — confirming that the error is a genuine DNS resolution
  failure (bad-dns3 cannot resolve the controller), not an artefact of the old cap.
  The exact expected error string should be determined when running the test for the
  first time after the fix and then hardened into the assertion.

Run the full test:

```bash
make evetest NAME=TestDNSFunctionality
```

---

## Reference: How `net.Dialer` Works Internally

This section documents the exact behaviour of `net.Dialer.DialContext` so that
`pkg/pillar/dialer` (option A) replicates it faithfully.

### Phase 0 — Deadline computation

`d.deadline(ctx, now)` picks the **earliest** of three values:
- `now + d.Timeout`
- `d.Deadline` (absolute)
- `ctx.Deadline()`

A child context is created with that deadline so every subsequent operation (DNS + TCP) is
bounded by it.

---

### Phase 1 — DNS resolution (`resolveAddrList` → `goLookupIPCNAMEOrder`)

#### 1a. resolv.conf caching and re-reading

`resolvConf.tryUpdate("/etc/resolv.conf")` is called before every lookup, but it only
re-reads the file if **at least 5 seconds** have passed since the last check AND the
file's mtime has changed. Otherwise the cached `dnsConfig` is reused.

| Parameter | Default | EVE resolv.conf |
|-----------|---------|-----------------|
| `timeout` | 5 s | 5 s |
| `attempts` | 2 | 5 (`pillar/dialer` default, matching `dpcreconciler/genericitems/resolvconf.go:160`) |
| `nameservers` | none → `127.0.0.1` | all interface servers (**max 3 read**) |
| `rotate` | off | on |
| `ndots` | 1 | 1 |

#### 1b. /etc/hosts check

Before any DNS query, `lookupStaticHost(name)` is called. If the name has a static
entry it is returned immediately and DNS is skipped entirely.

#### 1c. FQDN expansion — `nameList`

`nameList(name)` builds the ordered list of fully-qualified names to try:
- If `name` has `≥ ndots` dots: bare name (with trailing `.`) tried **first**, then each
  search domain appended.
- If `name` has `< ndots` dots: search domains tried first, bare name tried **last**.
- For a name like `zedcloud.net` (1 dot, ndots=1): bare name `zedcloud.net.` is tried
  first.

The outer loop over FQDNs stops as soon as one succeeds. An `NXDOMAIN` response from any
server also stops the loop immediately.

#### 1d. A and AAAA queries — **parallel by default**

For each FQDN, both A and AAAA goroutines are launched **simultaneously**:

```go
go func() { p, server, err = r.tryOneName(ctx, conf, fqdn, TypeA);    lane <- result{...} }()
go func() { p, server, err = r.tryOneName(ctx, conf, fqdn, TypeAAAA); lane <- result{...} }()
// results collected in order: A first, AAAA second
```

Exception: `options single-request` makes them sequential (A then AAAA).
With `network="ip4"` or `network="ip6"` only one query type is issued.

#### 1e. `tryOneName` — per-query retry loop

```
for attempt in 0..cfg.attempts-1:           // 2 by default; 5 for EVE
    for j in 0..len(cfg.servers)-1:         // at most 3 servers
        server = cfg.servers[(offset+j) % n]
        exchange(server, timeout=cfg.timeout)   // 5 s per server
        success  → return immediately
        NXDOMAIN → return immediately (no point retrying other servers)
        SERVFAIL / network error → continue to next server
```

`cfg.serverOffset()` atomically increments a global counter, so with `options rotate`
each concurrent call starts at the next server (distributes load across goroutines).

With EVE's `attempts:5` and 3 servers: up to **15 DNS packets** per query type, per FQDN,
all bounded by the overall context deadline.

#### 1f. `exchange` — per-server UDP → TCP fallback

```
networks = ["udp", "tcp"]  // or ["tcp"] only if options use-vc
for network in networks:
    deadline = now + cfg.timeout   // fresh 5 s window
    conn = r.Dial(ctx, network, server:53)
    conn.SetDeadline(deadline)
    send DNS query
    if UDP and response.Truncated:
        continue  // retry same server over TCP immediately
    return response
```

UDP is tried first. If the server returns TC=1 (truncated), the **same server** is
retried over TCP immediately before moving to the next server.

#### 1g. Address sorting — RFC 6724

After collecting all A and AAAA answers, `sortByRFC6724(addrs)` is called:
- For each candidate destination IP, Go does a zero-packet `DialUDP("udp", nil, &dst)`
  to ask the OS which source address it would use.
- Destinations are sorted so those **sharing the longest prefix with an available local
  source address** come first.
- This naturally prefers routable IPv6 over ULA, and reachable IPv4 over unreachable IPv6.

---

### Phase 2 — Address partitioning

With `network="tcp"` and `FallbackDelay ≥ 0` (default):

```go
primaries, fallbacks = addrs.partition(isIPv4)
```

`partition` is **label-following**: the first address in the RFC 6724-sorted list
determines the primary family.
- If sorted[0] is IPv6 → primaries = all IPv6 addrs, fallbacks = all IPv4 addrs.
- If sorted[0] is IPv4 → primaries = all IPv4 addrs, fallbacks = all IPv6 addrs.

If all addresses are the same family, `fallbacks` is empty and `dialParallel` immediately
delegates to `dialSerial(primaries)` — no Happy Eyeballs race.

---

### Phase 3 — Happy Eyeballs (`dialParallel`)

Only entered when both `primaries` and `fallbacks` are non-empty.

```
start goroutine A: dialSerial(primaryCtx, primaries)
arm timer: FallbackDelay (300 ms default)

─── 300 ms later (if primary hasn't finished) ───
start goroutine B: dialSerial(fallbackCtx, fallbacks)

first goroutine to return a conn wins → cancel the other
if primary errors before 300 ms → reset timer to 0 (start fallback immediately)
if both error → return primary's error (fallback error discarded)
```

The 300 ms window is RFC 6555: give IPv6 a head-start, fall back to IPv4 if it does not
respond quickly.

---

### Phase 4 — Serial IP-address loop (`dialSerial`)

Within each group, addresses are tried **one at a time**:

```
firstErr = nil
for i, addr in range(addresses):
    if ctx is done: return error

    if deadline set:
        remaining = deadline - now
        perAddr   = max(remaining / len(addresses[i:]), 2 s)   // minimum 2 s
        dialCtx   = context.WithDeadline(ctx, now + perAddr)

    conn, err = dialSingle(dialCtx, addr)
    if err == nil: return conn
    if firstErr == nil: firstErr = err   // keep only first error
```

Key points:
- Remaining time is divided equally over the **remaining** addresses (not all of them),
  so earlier addresses get more time.
- Floor of **2 seconds** per address regardless of how little time is left.
- **No retry per address** — one attempt, then move on.
- Only the **first error** is preserved; later errors are discarded.

---

### Phase 5 — Single TCP connect (`dialSingle` → `dialTCP`)

```
socket(AF_INET[6], SOCK_STREAM)
bind(laddr)               // if d.LocalAddr is set
connect(raddr)            // non-blocking; kernel sends SYN
poll.WaitWrite(fd, ctx)   // waits until connected or dialCtx deadline expires
```

No application-level retries. One `connect(2)` per address.

---

### Complete timeout budget example

Setup: `d.Timeout = 30 s`, host resolves to 2×IPv6 + 2×IPv4.

```
t=0        DialContext starts; deadline = t+30 s

t=0        DNS: A goroutine + AAAA goroutine launched simultaneously
           Each: up to 5 attempts × 3 servers × 5 s/server = 75 s budget,
           but all bounded by overall ctx deadline (t+30 s).
t=~0.1 s   DNS returns: [2001:db8::1, 2001:db8::2, 192.0.2.1, 192.0.2.2]
           sorted by RFC 6724 — assume IPv6 first

           partition: primaries=[2001:db8::1, 2001:db8::2]
                      fallbacks=[192.0.2.1, 192.0.2.2]

t=0.1 s    goroutine A: dialSerial(primaries)
             2001:db8::1 — perAddr ≈ 14.95 s — connect …
t=0.4 s    goroutine B: dialSerial(fallbacks) starts (Happy Eyeballs 300 ms)
             192.0.2.1   — perAddr ≈ 14.8 s  — connect …

first conn wins; other goroutine cancelled
```

---

### What `pkg/pillar/dialer` must replicate

| Go behaviour | `dialer` package |
|---|---|
| A + AAAA in parallel | `resolver.go`: two goroutines per FQDN |
| Search domain expansion (`nameList`) | `resolver.go`: applied from `Resolver.SearchDomains`, populated from `NetworkPortStatus.SearchDomains` (requires extending the field from `DomainName string` to `SearchDomains []string`) |
| Per-server timeout + retry loop | `resolver.go`: `exchange` loop; driven by `Resolver.ServerTimeout` / `Resolver.Attempts` (defaults 5 s / 2) |
| UDP first, TCP on TC=1 | `resolver.go`: `exchange` |
| RFC 6724 address sorting | `resolver.go` only — sorting is part of resolution, not dialing (matches Go stdlib: `sortByRFC6724` called inside `goLookupIPCNAMEOrder`) |
| Partition by first address's family | `dialer.go`: `partition` — receives pre-sorted list from resolver |
| Happy Eyeballs 300 ms fallback | `dialer.go`: `dialParallel` |
| Per-address time division, min 2 s | `dialer.go`: `dialSerial` |
| One TCP attempt per IP, first error returned | `dialer.go`: `dialSerial` |
