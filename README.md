<div align="center">

```
▐▛███▜▌
▝▜█████▛▘
  ▘▘ ▝▝
```

# tailway

**Expose local ports through any firewall — a fast TCP/UDP reverse tunnel with a beautiful terminal UI.**

[![Go](https://img.shields.io/badge/Go-1.21+-00ADD8?style=flat&logo=go&logoColor=white)](https://go.dev)
[![License](https://img.shields.io/github/license/kiiimatz/tailway?style=flat)](LICENSE)
[![Release](https://img.shields.io/github/v/release/kiiimatz/tailway?style=flat)](https://github.com/kiiimatz/tailway/releases)

</div>

---

## What is tailway?

tailway is a self-hosted reverse tunnel proxy. It lets you expose services running on machines behind NAT or firewalls to the public internet — similar to ngrok or frp, but minimal, fast, and fully controlled by you.

You run a **server** on a public machine. Clients connect to it over a persistent control connection, register tunnels, and incoming traffic is proxied back to the client transparently. Both **TCP** and **UDP** are supported.

Everything is driven from a clean, keyboard-first **terminal UI** built with [Bubble Tea](https://github.com/charmbracelet/bubbletea).

---

## Features

- **TCP & UDP tunneling** — expose any local port over any protocol
- **Key-based authentication** — shared secret protects the server
- **Multiple tunnels per client** — register as many ports as you need
- **Live terminal UI** — manage tunnels interactively without touching config files
- **Debug mode** — opt-in verbose logging with `--debug`
- **Zero config** — single binary, no YAML, no daemon
- **Self-hosted** — your traffic, your server

---

## How it works

```
[ your machine ]              [ public server ]              [ internet ]
  local service  <──tunnel──>  tailway server  <──────────>  anyone
  :25565                        :9000 (public)
```

1. The server listens on a **control port** (default `7000`) and a **data port** (`7001`)
2. The client connects to the control port and authenticates with a shared key
3. The client registers tunnels — each tunnel binds a public port on the server
4. When someone connects to that public port, the server signals the client over the data port and proxies the connection end-to-end

---

## Installation

### Build from source

```bash
git clone https://github.com/kiiimatz/tailway.git
cd tailway
go build -o tailway ./cmd/tailway
```

> Requires Go 1.21 or later.

### Pre-built binaries

Download from [Releases](https://github.com/kiiimatz/tailway/releases).

---

## Usage

### Server

Run tailway on your public machine:

```bash
tailway server
```

You will be prompted for an authentication key. Once entered, the server starts listening.

```
  tailway server

  Connection      0
  Key             ************
  Open UDP        0
  Open TCP        0

  q: quit  ctrl+c: quit
```

**Options:**

| Flag | Default | Description |
|------|---------|-------------|
| `--port` | `7000` | Control port (data port is always `port + 1`) |
| `--debug` | off | Enable verbose logging |

```bash
tailway server --port 8000
tailway server --debug
```

---

### Client

Run tailway on the machine you want to expose:

```bash
tailway client
```

**Login screen** — enter your server address and key:

```
  tailway client

  Server: 203.0.113.1:7000

  Key:    ••••••••••••

  tab: next field  enter: connect  ctrl+c: quit
```

**Main screen** — manage your tunnels:

```
  tailway client  connected to 203.0.113.1:7000

  + ADD

  PROTOCOL    LOCAL PORT    GLOBAL PORT
  ──────────────────────────────────────
  TCP         25565         25565
  UDP         19132         19132

  ↑↓: select  enter: add  d: delete  q: quit
```

**Add tunnel screen** — pick protocol and ports:

```
  tailway client

  Protocol:    TCP  UDP

  Client Port: 25565

  Server Port: 25565

  Add Tunnel

  tab/↑↓: navigate  ←/→: protocol  enter: confirm  esc: back
```

---

## Architecture

```
cmd/tailway/
└── main.go                  Entry point, flag parsing

internal/
├── proto/
│   └── proto.go             Wire protocol (JSON framing over TCP)
├── server/
│   ├── server.go            Listener management, client registry
│   ├── client_conn.go       Auth handshake, control loop, keepalive
│   ├── tunnel.go            Tunnel registration (TCP/UDP)
│   ├── proxy.go             Data proxying between public and client
│   └── ui/
│       └── app.go           Server TUI (Bubble Tea)
└── client/
    ├── client.go            Connection management, event bus
    ├── tunnel.go            Tunnel state tracking
    ├── conn.go              Data port connector
    └── ui/
        ├── app.go           Client TUI root
        ├── styles.go        Shared styles
        ├── login.go         Login screen
        ├── main_screen.go   Tunnel list screen
        └── add_screen.go    Add tunnel screen
```

---

## Protocol

The control channel uses a simple newline-delimited JSON framing over TCP:

```json
{"type": "auth",         "payload": {"key": "secret"}}
{"type": "auth_ok",      "payload": null}
{"type": "add_tunnel",   "payload": {"protocol": "tcp", "client_port": 25565, "server_port": 25565}}
{"type": "tunnel_added", "payload": {"tunnel_id": "uuid"}}
{"type": "ping",         "payload": null}
{"type": "pong",         "payload": null}
```

The data channel uses the same framing for signaling, then switches to raw byte proxying once both sides are connected.

---

## License

[GPL-3.0](LICENSE) © kiiimatz
