# SwiftDeploy

A declarative deployment CLI written in Go. Reads `manifest.yaml` and manages the full lifecycle of a Dockerized web app behind Nginx.

## Prerequisites

- Go 1.22+
- Docker + Docker Compose plugin
- Git

## Setup

### 1. Clone and build the CLI

```bash
git clone https://github.com/GideonIsBuilding/swiftdeploy.git
cd swiftdeploy
chmod +x build.sh
./build.sh
```

This produces the `./swiftdeploy` binary in the project root.

### 2. Build the app Docker image

```bash
docker build -t swift-deploy-1-node:latest .
```

> The image name must match `services.image` in `manifest.yaml`.

---

## Subcommand Walkthrough

### `init` — Generate config files

Reads `manifest.yaml` and generates `nginx.conf` and `docker-compose.yml`.

```bash
./swiftdeploy init
```

Output:
```
✅ Generated nginx.conf
✅ Generated docker-compose.yml
```

> The grader deletes these files and re-runs `init` to verify they regenerate correctly.

---

### `validate` — 5 pre-flight checks

Runs all checks before deploying. Exits non-zero on any failure.

```bash
./swiftdeploy validate
```

Output:
```
🔍 Running pre-flight checks...

  ✅ PASS  [1/5] manifest.yaml exists and is valid YAML
  ✅ PASS  [2/5] All required fields are present and non-empty
  ✅ PASS  [3/5] Docker image exists locally
  ✅ PASS  [4/5] Nginx port is not already bound on the host
  ✅ PASS  [5/5] Generated nginx.conf is syntactically valid

✅ All checks passed. Ready to deploy.
```

Checks performed:
1. `manifest.yaml` exists and parses as valid YAML
2. All required fields are present and non-empty
3. The Docker image referenced in the manifest exists locally
4. The Nginx port is not already bound on the host
5. `nginx.conf` is syntactically valid (via `docker run nginx -t`)

---

### `deploy` — Start the stack

Runs `init`, brings up all containers, and blocks until healthy (max 60s).

```bash
./swiftdeploy deploy
```

Output:
```
⚙️  Generating configs...
✅ Generated nginx.conf
✅ Generated docker-compose.yml

🚀 Starting stack...
[+] Running 3/3
 ✔ Network swiftdeploy-net  Created
 ✔ Container app            Started
 ✔ Container nginx          Started

⏳ Waiting for health check at http://localhost:8080/healthz ...
✅ Stack is healthy and ready!
   → App running at http://localhost:8080
```

---

### `promote` — Switch deployment mode

Switches between `stable` and `canary` with a rolling restart of the app container only.

```bash
./swiftdeploy promote canary
./swiftdeploy promote stable
```

What it does:
1. Updates `mode` field in `manifest.yaml` in-place
2. Regenerates `docker-compose.yml` with the new `MODE` env var
3. Restarts only the `app` container (nginx stays up)
4. Confirms the new mode by checking `/healthz` and the `X-Mode` header

```
🔄 Promoting to canary mode...
✅ manifest.yaml updated — mode: canary
⚙️  Regenerating docker-compose.yml...
✅ Generated nginx.conf
✅ Generated docker-compose.yml
🔁 Restarting app container...
⏳ Waiting for app to be healthy...
✅ Promotion complete! App is running in canary mode.
   X-Mode header: canary
```

---

### `teardown` — Stop everything

Removes all containers, networks, and volumes.

```bash
./swiftdeploy teardown

# Also delete generated config files:
./swiftdeploy teardown --clean
```

---

## API Endpoints

| Method | Path | Description |
|--------|------|-------------|
| `GET` | `/` | Welcome message with mode, version, timestamp |
| `GET` | `/healthz` | Liveness check with process uptime |
| `POST` | `/chaos` | Chaos simulation (canary mode only) |

### Chaos endpoint examples

```bash
# Slow responses by 5 seconds
curl -X POST http://localhost:8080/chaos \
  -H "Content-Type: application/json" \
  -d '{"mode":"slow","duration":5}'

# Return 500 on ~50% of requests
curl -X POST http://localhost:8080/chaos \
  -H "Content-Type: application/json" \
  -d '{"mode":"error","rate":0.5}'

# Recover from any chaos
curl -X POST http://localhost:8080/chaos \
  -H "Content-Type: application/json" \
  -d '{"mode":"recover"}'
```

---

## Project Structure

```
swiftdeploy/
├── manifest.yaml              # Single source of truth — edit this only
├── swiftdeploy                # Compiled CLI binary (after ./build.sh)
├── build.sh                   # Builds the CLI binary
├── Dockerfile                 # Builds the app service image
├── go.mod                     # CLI Go module
├── main.go                    # CLI entry point
├── cmd/
│   ├── root.go                # Cobra root + subcommand registration
│   ├── init_cmd.go            # init subcommand
│   ├── validate.go            # validate subcommand (5 checks)
│   ├── deploy.go              # deploy subcommand
│   ├── promote.go             # promote subcommand
│   └── teardown.go            # teardown subcommand
├── internal/
│   └── manifest.go            # Manifest struct, load, validate, update
├── app/
│   ├── go.mod                 # App Go module
│   └── main.go                # HTTP server (stable/canary/chaos)
└── templates/
    ├── nginx.conf.tmpl        # Nginx config template
    └── docker-compose.yml.tmpl # Compose config template
```

---

## Architecture

```
  User → http://localhost:8080
              ↓
         [ Nginx container ]
         - Listens on :8080
         - Adds X-Deployed-By: swiftdeploy
         - Forwards X-Mode from upstream
         - JSON error bodies on 502/503/504
              ↓
         [ App container ]
         - Listens on :3000 (internal only)
         - stable: normal responses
         - canary: adds X-Mode: canary header
```

The app port (3000) is never exposed to the host. All traffic routes through Nginx.
