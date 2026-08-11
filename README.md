# Z1

[![License](https://img.shields.io/badge/license-AGPLv3-blue.svg)](LICENSE)
[![Go](https://img.shields.io/badge/Go-1.25-00ADD8.svg)](https://go.dev/)

An open-source NAS operating system built on Debian 13 (trixie). All native systemd services (no Docker), with a Go single-binary web management panel — stable, high-performance, easy to maintain.

> **Philosophy**: Protocols are just system capabilities; data is the core. Users should manage **storage, sharing, users, permissions, apps** — not Samba, NFS, or RAID jargon.

[简体中文](README.zh-CN.md) | **English**

## Vision

Z1 has two parallel tracks:

| Track | Goal | Description |
|-------|------|-------------|
| 🔓 **Open-Source Software** | Community-driven NAS OS | Fully open (AGPLv3), contributions welcome |
| 📦 **Hardware Integration** | Ready-to-use NAS product | Pre-installed hardware, lower barrier |

> The two tracks reinforce each other: community contributions → better product → revenue funds development.

## Features

### NAS Services (9)

| Service | Port | Description |
|---------|------|-------------|
| Samba | 139, 445 | Windows/Mac/Linux file sharing |
| NFS | 2049 | Linux high-speed file access |
| FTP | 21 | vsftpd traditional file transfer |
| WebDAV | 8080 | rclone serve WebDAV |
| FileBrowser | 8081 | Web file manager (mobile-friendly) |
| S3 API | 9000 | rclone serve s3, auto bucket mapping |
| Fail2ban | - | SSH/FTP brute-force protection |
| UFW | - | Firewall, default deny |
| Z1 Panel | 8090 | Web management panel (Go binary) |

> All native systemd services, no Docker dependency. One file, six protocol access.

### Web Panel Modules (11)

| Module | Features |
|--------|----------|
| Dashboard | System info, CPU/mem/disk usage, disk slot map, pool overview cards, service status |
| Service Mgmt | 9 services start/stop/restart, view logs |
| User Mgmt | User list (service badges/quota/shares), 4-step wizard, groups, permission matrix, login logs, service toggles |
| Storage Mgmt | **4-layer model** (Disk→Pool→Volume→Folder), slot map, RAID wizard (goal-oriented), LVM/RAID expansion (SSE), folder CRUD |
| Firewall | Status cards, visual rule management, source IP/notes, service presets, auto-allow SSH |
| Remote Sync | rclone remotes (S3/SFTP/WebDAV/FTP), upload/download tasks, shared dir whitelist, bandwidth limits |
| Monitoring | Real-time status, network traffic, top processes, error logs, 4-channel alerts |
| Config Mgmt | Samba shares, FTP whitelist, config editing, service autostart |
| Backup/Restore | Manual/auto backups, backup list, one-click restore |
| System Settings | Network config, timezone, hostname, SSH config, kernel params |
| Disk Mgmt | Partitions, mount/unmount, format, LVM, I/O performance, SMART details |

### 4-Layer Storage Model

```
Physical Disk    → Hardware: model/SMART/temp/serial/power-on hours
Storage Pool     → Data pool: RAID/LVM, UI hides implementation
Volume           → Logical volume: mountpoint/fs/capacity, ready for snapshots/quota/compression
Shared Folder    → User object: permissions/recycle bin/quota/protocol toggles
```

### RAID Wizard (Goal-Oriented)

Pick "Data Safety" or "Max Capacity" — the system recommends the best option. Supports RAID0/1/5/6 + LVM, 7 configuration modes.

## Design Principles

1. **Debian First** — Built on Debian 13, not tailored to specific hardware
2. **Native Linux First** — All system capabilities managed natively, no container layer
3. **Go Single Binary** — Single Go binary, embedded frontend, zero intranet deps
4. **systemd First** — All services as systemd units
5. **Storage First** — UI/API centered on "users managing data"
6. **Web First** — The only official client is Web UI (with PWA)

> Not recommended: Docker First / Protocol First / RAID First / hardware-specific First

## Quick Deploy

```bash
# 1. Clone repo
git clone https://gitee.com/gitdogcat/nas.git ~/soft/nas

# 2. Symlink
sudo ln -sfn ~/soft/nas /opt/nas

# 3. Configure password
cp /opt/nas/.env.example /opt/nas/.env
# Edit .env (min 12 chars)

# 4. Deploy
sudo bash /opt/nas/scripts/setup.sh
```

setup.sh auto-detects the sudo user as the NAS management user.

> Deployment takes ~5-10 minutes. Access at `http://<NAS IP>:8090`.

## Compile from Source

```bash
cd ~/soft/nas/web
export GOPROXY=https://goproxy.cn,direct
go build -o nas-panel .
strip nas-panel   # 11MB → 6.2MB
```

> Frontend is embedded — no Node.js needed. GOPROXY required in China.

## Directory Structure

```
nas/
├── configs/            # Service config files
│   ├── smb.conf        # Samba
│   ├── exports         # NFS exports
│   ├── vsftpd.conf     # FTP
│   ├── jail.local      # Fail2ban
│   ├── nas-panel.service
│   └── ...
├── scripts/            # Management scripts
│   ├── setup.sh        # One-click deploy (10 steps)
│   ├── cleanup.sh      # Cleanup (--keep-data to preserve data)
│   ├── monitor.sh      # Monitoring (cron, 5-min)
│   ├── backup-config.sh
│   └── restore-config.sh
├── web/                 # Web panel source (Go)
│   ├── main.go          # Entry: route registration + startup
│   ├── common/          # Shared utils (auth, JSON, sudo)
│   ├── modules/         # Feature modules (independent)
│   │   ├── dashboard/
│   │   ├── services/
│   │   ├── users/
│   │   ├── diskmgmt/    # Storage management (4-layer model)
│   │   ├── firewall/
│   │   ├── monitor/
│   │   ├── rclone/      # Remote sync
│   │   └── ...
│   ├── go.mod
│   └── frontend/        # Frontend (Alpine.js + CSS)
│       ├── index.html
│       ├── app.js
│       └── style.css
├── docs/
│   ├── nas-product-manual.md
│   ├── architecture-v1.0.md   # Architecture constitution
│   └── external/              # External advisory docs
├── .env.example        # Env template (copy to .env)
├── CHANGELOG.md
├── TODO.md             # Roadmap (26 items, 15 done)
└── README.md           # This file
```

## Service Access

| Service | URL | Credentials |
|---------|-----|-------------|
| Samba | `\\NAS_IP\shared` | NAS_USER / NAS_PASS |
| NFS | `mount NAS_IP:/data/shared` | IP-based |
| FTP | `ftp://NAS_IP/` | NAS_USER / NAS_PASS |
| WebDAV | `http://NAS_IP:8080/` | NAS_USER / NAS_PASS |
| FileBrowser | `http://NAS_IP:8081/` | NAS_USER / NAS_PASS |
| S3 API | `http://NAS_IP:9000` | NAS_USER / NAS_PASS |
| Web Panel | `http://NAS_IP:8090` | NAS_USER / NAS_PASS |

## Alert Configuration

Configure alert channels in `.env` — enable as many as you need:

| Channel | Env Var | How to Get |
|---------|---------|------------|
| DingTalk | ALERT_DINGTALK_WEBHOOK | Group settings → Bot |
| Telegram | ALERT_TELEGRAM_TOKEN | @BotFather → /newbot |
| Bark (iOS) | ALERT_BARK_KEY | Bark app → copy key |
| Email | ALERT_SMTP_HOST | Any SMTP service |

Alert thresholds configurable in `.env` or Web panel.

## Backup & Restore

- **Auto-backup before upgrade** (setup.sh integrated)
- **Weekly backup** (cron, Sunday 3 AM)
- **Keeps last 5 backups**, auto-cleanup
- **Web panel**: one-click backup/restore

```bash
# Manual backup
sudo bash /opt/nas/scripts/backup-config.sh

# Restore (interactive)
sudo bash /opt/nas/scripts/restore-config.sh
```

## System Requirements

| Requirement | Minimum | Recommended |
|-------------|---------|-------------|
| OS | Debian 12+ | Debian 13 (trixie) |
| CPU | 2 cores | 4 cores+ |
| RAM | 2GB | 4GB+ |
| Disk | 20GB | Separate data disk |
| Network | Gigabit | 2.5GbE |
| Arch | x86_64 | x86_64 / ARM64 |

> Also supports Raspberry Pi 4/5, VMs, Mini PCs.

## Tech Stack

| Layer | Technology | Notes |
|-------|-----------|-------|
| NAS Services | Samba/NFS/vsftpd/rclone/FileBrowser | All native systemd, no Docker |
| Backend | Go 1.25 + go:embed | Single binary, <3MB RAM |
| Frontend | Alpine.js + CSS | No build tools, light theme |
| Auth | JWT | 24h validity |
| Monitoring | Shell + cron | Zero extra services, 5-min checks |
| Deploy | Shell scripts | 10-step one-click deploy |
| Passwords | .env file | .gitignore excluded |

## Development & Contributing

| Doc | Audience | Content |
|-----|----------|---------|
| [CONTRIBUTING.md](CONTRIBUTING.md) | External contributors | Contribution flow, code style, PR guide |
| [DEVELOPMENT.md](DEVELOPMENT.md) | Team members | Environment setup, project structure, dev workflow |
| [Architecture v1.0](docs/architecture-v1.0.md) | All | Architecture constitution, 20 recommendations review |

### Quick Start

```bash
make build      # Compile
make dev        # Local run
make build-all  # Cross-compile all platforms
```

### Module Architecture

Add new features by:
1. Create directory under `web/modules/`
2. Implement `RegisterRoutes(mux *http.ServeMux)`
3. Add import + one line in `main.go`
4. Build & deploy

Each module is independent, sharing `common/` package for auth/JSON/sudo.

## License

- **This project**: GNU AGPLv3 (see [LICENSE](LICENSE))
- **Third-party components**: see [THIRD_PARTY_LICENSES.md](THIRD_PARTY_LICENSES.md)

> AGPLv3 means: even if you provide services over a network, you must open-source your modifications. This ensures all Z1 derivatives give back to the community.

## Links

- **Website**: https://www.z1.sale
- **Gitee**: https://gitee.com/gitdogcat/nas
- **Issues**: https://gitee.com/gitdogcat/nas/issues
- **Downloads**: https://file.abwen.com
- **Changelog**: [CHANGELOG.md](CHANGELOG.md)
- **Roadmap**: [TODO.md](TODO.md)
