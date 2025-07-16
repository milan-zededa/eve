# evetest-lps

A Local Profile Server (LPS) implementation for EVE integration testing.

This container application implements the
[LPS API specification](https://github.com/lf-edge/eve-api/blob/main/PROFILE.md)
and adds a management REST API and a web UI for controlling the server and
inspecting data received from EVE.

## Architecture

A single HTTP server runs on port **8888** with three path groups:

- `/api/v1/` -- LPS protocol endpoints (protobuf binary, consumed by EVE)
- `/manage/v1/` -- Management REST API (JSON, for tests and programmatic control)
- `/ui/` -- Web UI (auto-refreshing page for human operators)

The container also runs an SSH daemon (port 22, `root:testpassword`) and uses
bash as PID 1 so that EVE console access works.

## LPS Protocol Endpoints

These implement the EVE LPS specification. All request/response bodies use
`application/x-proto-binary` encoding.

| Method | Path | Description |
|--------|------|-------------|
| GET | `/api/v1/local_profile` | Retrieve local profile |
| POST | `/api/v1/radio` | Publish radio status, get radio config |
| POST | `/api/v1/appinfo` | Publish app info, get app commands |
| POST | `/api/v1/devinfo` | Publish device info, get device command |
| POST | `/api/v1/location` | Publish GNSS location |
| POST | `/api/v1/network` | Publish network info, get local network config |
| POST | `/api/v1/appbootinfo` | Publish app boot info, get boot config |

Protobuf definitions: [local_profile.proto][lp-proto], [network.proto][net-proto].

## Management REST API

All endpoints use JSON encoding. GET endpoints return data that EVE has posted
to the LPS. PUT endpoints configure what the LPS sends back to EVE.

### Reading state

| Method | Path | Description |
|--------|------|-------------|
| GET | `/manage/v1/status` | Full state (config + all received data) |
| GET | `/manage/v1/config` | Current LPS config (token, profile, etc.) |
| GET | `/manage/v1/radio-status` | Last radio status from EVE |
| GET | `/manage/v1/appinfo` | Last app info list from EVE |
| GET | `/manage/v1/devinfo` | Last device info from EVE |
| GET | `/manage/v1/location` | Last location from EVE |
| GET | `/manage/v1/network` | Last network info from EVE |
| GET | `/manage/v1/appbootinfo` | Last app boot info from EVE |

GET endpoints return `404` if EVE has not yet posted data for that endpoint.

### Setting config

| Method | Path | Body (JSON) | Description |
|--------|------|-------------|-------------|
| PUT | `/manage/v1/token` | `{"token": "..."}` | Set server token |
| PUT | `/manage/v1/profile` | `{"profile": "..."}` | Set local profile |
| PUT | `/manage/v1/radio-config` | `{"radioSilence": true}` | Set radio silence |
| PUT | `/manage/v1/app-command` | [`AppCommand[]`][lp-proto] | Set app commands |
| PUT | `/manage/v1/dev-command` | [`LocalDevCmd`][lp-proto] | Set device command |
| PUT | `/manage/v1/app-boot-config` | [`AppBootConfig[]`][lp-proto] | Set app boot configs |
| PUT | `/manage/v1/network-config` | [`LocalNetworkConfig`][net-proto] | Set local network config |

[lp-proto]: https://github.com/lf-edge/eve-api/blob/main/proto/profile/local_profile.proto
[net-proto]: https://github.com/lf-edge/eve-api/blob/main/proto/profile/network.proto

#### Examples

Set the server token:

```bash
curl -X PUT -d '{"token":"my-secret"}' http://localhost:8888/manage/v1/token
```

Set a local profile:

```bash
curl -X PUT -d '{"profile":"office"}' http://localhost:8888/manage/v1/profile
```

Submit local network config (MTU override for one port):

```bash
curl -X PUT -H 'Content-Type: application/json' -d '{
  "serverToken": "my-secret",
  "ports": [
    {
      "logicalLabel": "ethernet1",
      "useDhcp": true,
      "mtu": 9000
    }
  ]
}' http://localhost:8888/manage/v1/network-config
```

Issue a device shutdown command:

```bash
curl -X PUT -d '{"timestamp":1234567890,"command":"COMMAND_SHUTDOWN"}' \
  http://localhost:8888/manage/v1/dev-command
```

Read the latest network info posted by EVE:

```bash
curl -s http://localhost:8888/manage/v1/network | jq .
```

## Web UI

Navigate to `http://<host>:8888/ui/` (or just `/` which redirects there).

The UI provides:

- **Monitoring panel** -- auto-refreshing display of all data EVE posts
  (device info, app info, radio status, location, network info, app boot info).
- **Control panel** -- forms to set the server token, local profile, radio
  silence, device commands, and app commands.

## Building and Pushing

```bash
make build            # builds docker image
make push             # builds and pushes to Docker Hub
REPO=myrepo make push # use a different Docker Hub repository
```

## Command-line Flags

| Flag | Default | Description |
|------|---------|-------------|
| `-port` | `8888` | HTTP server port |
| `-token` | (empty) | Initial server token |
