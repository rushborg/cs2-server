# RUSH-B.ORG CS2 Server

Docker image and management agent for CS2 dedicated servers on [RUSH-B.ORG](https://rush-b.org) match platform.

## Stack

| Component | Version | Purpose |
|-----------|---------|---------|
| CS2 Dedicated Server | App 730 | Game server |
| [MetaMod:Source](https://www.sourcemm.net/) | 2.0 (git1390) | Plugin framework |
| [CounterStrikeSharp](https://github.com/roflmuffin/CounterStrikeSharp) | v1.0.364 | .NET plugin API for CS2 |
| [MatchZy](https://github.com/shobhit-pathak/MatchZy) | 0.8.15 | Match management (get5-compatible) |

## Architecture

```
Host Machine
├── cs2-base/                    <- Shared CS2 installation (~62GB, downloaded once)
│   └── game/csgo/addons/        <- MetaMod + CounterStrikeSharp + MatchZy
├── shared/                      <- Shared across all instances
│   ├── admins_simple.ini
│   ├── plugins/                 <- Custom CSSharp plugins
│   └── maps/                    <- Custom maps
└── instances/
    ├── 27015/                   <- Per-instance
    │   ├── config/              <- server.cfg, matchzy.cfg (read-only mount)
    │   ├── data/                <- Writable: CSSharp logs, MatchZy data
    │   ├── demos/               <- Match recordings
    │   └── docker-compose.yml
    └── 27016/
        └── ...
```

CS2 base is downloaded once and bind-mounted into all containers. Per-instance writable directories (logs, plugin data, demos) are symlinked inside the container. The 2nd+ server on the same host starts in seconds.

## Docker Image

Base: `cm2network/steamcmd:root`

### Security

1. Entrypoint runs as **root** for setup (SteamCMD, plugin install, symlinks, permissions)
2. CS2 process drops to **steam** user via `gosu` before starting
3. CS2 never runs as root

### Environment Variables

| Variable | Default | Description |
|----------|---------|-------------|
| `CS2_PORT` | 27015 | Game server port |
| `CS2_GOTV_PORT` | 27020 | GOTV port |
| `CS2_MAP` | de_mirage | Starting map |
| `CS2_GSLT` | | [Game Server Login Token](https://steamcommunity.com/dev/managegameservers) |
| `DOTNET_SYSTEM_GLOBALIZATION_INVARIANT` | 1 | Required for CounterStrikeSharp .NET runtime |

### Volumes

| Mount | Purpose | Mode |
|-------|---------|------|
| `/home/steam/cs2-dedicated` | Shared CS2 base | rw |
| `/instance/config` | server.cfg, matchzy.cfg | ro |
| `/instance/data` | Logs, plugin state | rw |
| `/shared` | Admin config | ro |
| `/custom/plugins` | Custom plugins (.dll) | ro |
| `/custom/maps` | Custom maps (.bsp) | ro |
| `/demos` | Match recordings | rw |

### Build & Run

```bash
# Build
cd docker/cs2-server
docker build -t ghcr.io/rushborg/cs2-server:latest .

# Run
docker run -d --network host \
  --security-opt seccomp=unconfined \
  -e CS2_PORT=27015 \
  -e CS2_GSLT=YOUR_TOKEN \
  -e DOTNET_SYSTEM_GLOBALIZATION_INVARIANT=1 \
  -v /opt/cs2-base:/home/steam/cs2-dedicated \
  -v ./config:/instance/config:ro \
  -v ./data:/instance/data \
  ghcr.io/rushborg/cs2-server:latest
```

## Agent

Go binary running on each game server host. Connects to the platform via WebSocket and manages CS2 Docker containers.

### Commands

| Command | Description |
|---------|-------------|
| `deploy_server` | Create and start a new CS2 instance |
| `stop_server` / `remove_server` / `restart_server` | Lifecycle management |
| `setup_base` | Install/verify shared CS2 base |
| `update_base` | Update CS2 + plugins, restart all instances |
| `update_agent` | Self-update agent binary from platform |
| `query_server` | A2S status query |
| `exec_rcon` | Execute RCON command |
| `sync_admins` | Sync admin list |
| `install_plugin` / `install_map` | Custom content management |
| `get_logs` / `get_status` | Monitoring |

### Build

```bash
cd agent
CGO_ENABLED=0 GOOS=linux GOARCH=amd64 go build -ldflags "-s -w" -o rushborg-agent ./cmd/rushborg-agent
```

### Config (`/etc/rushborg/agent.yaml`)

```yaml
api_url: "https://rush-b.org/api"
host_id: "uuid"
api_key: "key"
data_dir: "/opt/rushborg-srv"
docker_image: "ghcr.io/rushborg/cs2-server:latest"
```

## CI/CD

`.github/workflows/docker-cs2.yml`:
- **Trigger:** push to main (`docker/` or `agent/`), manual dispatch
- **CS2 image:** self-hosted runner, pushed to GHCR
- **Agent:** GitHub runners, amd64 + arm64 artifacts

## License

MIT
