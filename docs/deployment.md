# Deployment Guide

## Quick Start with Docker

```bash
docker compose up -d
```

The container listens on port `10112` by default (matches `Dockerfile`'s
`EXPOSE 10112`, the `docker-compose.yml` port mapping, and the
`--server-addr :10112` default in `cmd/saker/main.go`). Set
`ANTHROPIC_API_KEY` in `.env` or via the `environment` section in
`docker-compose.yml`.

## systemd Service (Linux)

Create `/etc/systemd/system/saker.service`:

```ini
[Unit]
Description=Saker Agent Server
After=network.target

[Service]
Type=simple
User=saker
Group=saker
WorkingDirectory=/opt/saker
ExecStart=/usr/local/bin/saker --server --server-addr :10112 --auth-user admin --auth-pass ${SAKER_AUTH_PASS}
Environment=ANTHROPIC_API_KEY=${ANTHROPIC_API_KEY}
Environment=SAKER_MODEL=claude-sonnet-4-5-20250929
Restart=on-failure
RestartSec=5
LimitNOFILE=65536

# Security hardening
NoNewPrivileges=yes
ProtectSystem=strict
ProtectHome=read-only
ReadWritePaths=/var/saker
PrivateTmp=yes

[Install]
WantedBy=multi-user.target
```

Enable and start:

```bash
sudo systemctl daemon-reload
sudo systemctl enable saker
sudo systemctl start saker
```

## nginx Reverse Proxy

```nginx
server {
    listen 80;
    server_name saker.example.com;

    # Redirect to HTTPS
    return 301 https://$host$request_uri;
}

server {
    listen 443 ssl;
    server_name saker.example.com;

    ssl_certificate     /etc/ssl/certs/saker.pem;
    ssl_certificate_key /etc/ssl/private/saker.key;

    location / {
        proxy_pass http://127.0.0.1:10112;
        proxy_http_version 1.1;

        # WebSocket support (required for /ws endpoint)
        proxy_set_header Upgrade $http_upgrade;
        proxy_set_header Connection "upgrade";

        proxy_set_header Host $host;
        proxy_set_header X-Real-IP $remote_addr;
        proxy_set_header X-Forwarded-For $proxy_add_x_forwarded_for;
        proxy_set_header X-Forwarded-Proto $scheme;

        # SSE support (required for streaming responses)
        proxy_buffering off;
        proxy_cache off;

        # Timeout for long-running agent runs
        proxy_read_timeout 300s;
    }
}
```

## Debugging with pprof

Start the server with `--debug` to enable profiling endpoints:

```bash
saker --server --debug
```

Quick captures:

- CPU profile: `go tool pprof http://localhost:10112/debug/pprof/profile?seconds=30`
- Heap: `go tool pprof http://localhost:10112/debug/pprof/heap`
- Goroutines: `curl http://localhost:10112/debug/pprof/goroutine?debug=2 > stacks.txt`
- Allocs / block / mutex / threadcreate: same pattern under `/debug/pprof/<name>`
- Trace: `curl http://localhost:10112/debug/pprof/trace?seconds=5 > trace.out && go tool trace trace.out`

For a full bundle (cpu + heap + goroutine + allocs + block + mutex +
threadcreate + full stack dump in one timestamped directory) use
`scripts/pprof-snapshot.sh`. See the **Profiling (pprof)** section of
[`observability.md`](./observability.md#profiling-pprof) for the
runbook covering when to capture which profile and how to interpret it.

**Warning:** `--debug` exposes pprof without authentication. Bind to
localhost or put it behind a VPN/SSH tunnel; never run with `--debug`
on a public listener.