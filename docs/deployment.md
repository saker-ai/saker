# Deployment Guide

## Quick Start with Docker

```bash
docker compose up -d
```

The container listens on port 8080 by default. Set `ANTHROPIC_API_KEY` in
`.env` or via the `environment` section in `docker-compose.yml`.

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

Then access:

- CPU profile: `go tool pprof http://localhost:10112/debug/pprof/profile?seconds=30`
- Memory profile: `go tool pprof http://localhost:10112/debug/pprof/heap`
- Goroutine dump: `curl http://localhost:10112/debug/pprof/goroutine?debug=1`
- Trace: `curl http://localhost:10112/debug/pprof/trace?seconds=5 > trace.out && go tool trace trace.out`

**Warning:** Do not enable `--debug` in production without auth, as pprof
endpoints expose internal state.