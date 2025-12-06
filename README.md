# Host-Side Deployment Server

A production-ready Go HTTP server for managing Docker-based service deployments, running outside containers on the host machine.

## Features

- **CRUD API** for managing deploy targets (containers/services)
- **Secure deployments** via per-container secret tokens
- **Atomic operations** with per-container mutex to prevent parallel deploys
- **Host-level execution**: git pull + docker compose up
- **JSON file persistence** with thread-safe access
- **Panic recovery** and structured logging
- **Cloudflare Tunnel compatible**

## Quick Start

### Build

```bash
go mod tidy
go build -o deploy-server .
```

### Run

```bash
./deploy-server
# Server starts on port 8080 (configurable via PORT env)
```

### Configure as System Service

```bash
# Copy files
sudo cp deploy-server /opt/deploy-server/
sudo cp containers.json /opt/deploy-server/
sudo cp deploy-server.service /etc/systemd/system/

# Create user
sudo useradd -r -s /bin/false deploy
sudo chown -R deploy:deploy /opt/deploy-server

# Enable and start
sudo systemctl daemon-reload
sudo systemctl enable deploy-server
sudo systemctl start deploy-server

# Check status
sudo systemctl status deploy-server
sudo journalctl -u deploy-server -f
```

## API Reference

### Health Check

```bash
curl http://localhost:8080/health
```

Response:
```json
{
  "success": true,
  "data": {
    "status": "healthy",
    "time": "2024-01-15T10:30:00Z"
  }
}
```

### Create Container

```bash
curl -X POST http://localhost:8080/containers \
  -H "Content-Type: application/json" \
  -d '{
    "id": "myapp",
    "path": "/home/deploy/myapp",
    "secret": "supersecret123"
  }'
```

Response:
```json
{
  "success": true,
  "data": {
    "id": "myapp",
    "path": "/home/deploy/myapp"
  }
}
```

### List All Containers

```bash
curl http://localhost:8080/containers
```

Response:
```json
{
  "success": true,
  "data": [
    {"id": "myapp", "path": "/home/deploy/myapp"},
    {"id": "api", "path": "/home/deploy/api"}
  ]
}
```

### Get Single Container

```bash
curl http://localhost:8080/containers/myapp
```

### Update Container

```bash
curl -X PUT http://localhost:8080/containers/myapp \
  -H "Content-Type: application/json" \
  -d '{
    "path": "/home/deploy/myapp-v2",
    "secret": "newsecret456"
  }'
```

### Delete Container

```bash
curl -X DELETE http://localhost:8080/containers/myapp
```

### Trigger Deployment

```bash
curl -X POST "http://localhost:8080/deploy/myapp?secret=supersecret123"
```

Response:
```json
{
  "success": true,
  "data": {
    "container_id": "myapp",
    "output": "=== git reset --hard ===\nHEAD is now at abc123...\n...",
    "duration": "45.231s"
  }
}
```

## Deployment Workflow

When `/deploy/{id}` is called, the server executes:

```bash
cd /path/to/project
git reset --hard
git clean -fd
git pull
docker compose up -d --build --force-recreate
```

## Security Considerations

1. **Per-container secrets**: Each container has its own secret token
2. **Path validation**: Only absolute paths allowed, no directory traversal
3. **No arbitrary commands**: Only predefined git/docker commands execute
4. **Cloudflare Tunnel**: Use Cloudflare Tunnel for HTTPS without exposing ports

### Cloudflare Tunnel Setup

```bash
# Install cloudflared
curl -L https://github.com/cloudflare/cloudflared/releases/latest/download/cloudflared-linux-amd64 -o /usr/local/bin/cloudflared
chmod +x /usr/local/bin/cloudflared

# Login and create tunnel
cloudflared tunnel login
cloudflared tunnel create deploy-server

# Configure tunnel (config.yml)
tunnel: <TUNNEL_ID>
credentials-file: /root/.cloudflared/<TUNNEL_ID>.json
ingress:
  - hostname: deploy.yourdomain.com
    service: http://localhost:8080
  - service: http_status:404

# Run tunnel
cloudflared tunnel run deploy-server
```

## Manual Deployment Script

For manual deployments without the API:

```bash
chmod +x deploy.sh
./deploy.sh /path/to/project
```

## Environment Variables

| Variable | Default | Description |
|----------|---------|-------------|
| `PORT` | `8080` | HTTP server port |

## Error Codes

| Code | Description |
|------|-------------|
| 400 | Invalid request body or validation error |
| 401 | Missing secret token |
| 403 | Invalid secret token |
| 404 | Container not found |
| 409 | Container ID conflict or deployment in progress |
| 500 | Internal server error |

## File Structure

```
/opt/deploy-server/
├── deploy-server          # Binary
├── containers.json        # Data file (auto-created)
├── deploy.sh             # Manual deploy script
└── deploy-server.service  # Systemd unit (copy to /etc/systemd/system/)
```

## License

MIT
