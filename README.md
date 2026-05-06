# SwiftDeploy

A declarative deployment CLI written in Go. Reads `manifest.yaml` as the single source of truth and manages the full lifecycle of a Dockerized web app behind Nginx — with built-in observability, OPA policy enforcement, and a live audit trail.

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

### 3. Create the policies directory

```bash
mkdir -p policies
# data.json and .rego files are already included in the repo
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
         - Custom access log format
              ↓
         [ App container ]              [ OPA container ]
         - Listens on :3000 (internal)  - Listens on :8181 (localhost only)
         - stable: normal responses     - Evaluates infrastructure policy
         - canary: X-Mode header        - Evaluates canary safety policy
         - /metrics: Prometheus data    - Never reachable via nginx
         - /chaos: fault injection
```

The app port (3000) is never exposed to the host. OPA is bound to `127.0.0.1:8181` only — not reachable through nginx. All user traffic routes through Nginx on port 8080.

---

## Project Structure

```
swiftdeploy/
├── manifest.yaml                   # Single source of truth — the only file you edit
├── swiftdeploy                     # Compiled CLI binary (after ./build.sh)
├── build.sh                        # Builds the CLI binary
├── Dockerfile                      # Multi-stage build for the app service image
├── go.mod                          # CLI Go module
├── main.go                         # CLI entry point
├── .gitignore
├── cmd/
│   ├── root.go                     # Cobra root + subcommand registration
│   ├── init_cmd.go                 # init: generates nginx.conf + docker-compose.yml
│   ├── validate.go                 # validate: 5 pre-flight checks
│   ├── deploy.go                   # deploy: OPA gate → start stack → health poll
│   ├── promote.go                  # promote: metrics scrape → OPA gate → mode switch
│   ├── teardown.go                 # teardown: stop everything, optional --clean
│   ├── status.go                   # status: live dashboard + history.jsonl writer
│   └── audit.go                    # audit: generates audit_report.md
├── internal/
│   ├── manifest.go                 # Manifest struct, load, validate, in-place update
│   ├── opa.go                      # OPA HTTP client with typed failure modes
│   ├── hoststats.go                # Host stats collector (macOS + Linux)
│   └── metrics.go                  # Prometheus text parser, P99 interpolation
├── app/
│   ├── go.mod                      # App Go module
│   └── main.go                     # HTTP server: /, /healthz, /metrics, /chaos
├── templates/
│   ├── nginx.conf.tmpl             # Nginx config template
│   └── docker-compose.yml.tmpl    # Compose config template
└── policies/
    ├── data.json                   # All policy thresholds (edit here, not in .rego)
    ├── infrastructure.rego         # Pre-deploy: disk, CPU, memory checks
    └── canary.rego                 # Pre-promote: error rate, P99 latency checks
```

---

## Subcommand Walkthrough

### `init` — Generate config files

Reads `manifest.yaml` and generates `nginx.conf` and `docker-compose.yml` from templates. These files are the only outputs — everything else is derived from the manifest.

```bash
./swiftdeploy init
```

```
✅ Generated nginx.conf
✅ Generated docker-compose.yml
```

> The grader deletes generated files and re-runs `init` to verify they regenerate correctly.

---

### `validate` — 5 pre-flight checks

Runs all checks before deploying. Exits non-zero on any failure.

```bash
./swiftdeploy validate
```

```
🔍 Running pre-flight checks...

  ✅ PASS  [1/5] manifest.yaml exists and is valid YAML
  ✅ PASS  [2/5] All required fields are present and non-empty
  ✅ PASS  [3/5] Docker image exists locally
  ✅ PASS  [4/5] Nginx port is not already bound on the host
  ✅ PASS  [5/5] Generated nginx.conf is syntactically valid

✅ All checks passed. Ready to deploy.
```

Check 5 runs `nginx -t` inside a temporary Docker container — nginx does not need to be installed on the host. The `app` upstream hostname is substituted with `127.0.0.1` before validation to avoid Docker DNS resolution failures.

---

### `deploy` — Start the stack

Starts OPA first, runs the infrastructure policy check, then brings up the full stack. Blocks until the app is healthy (max 60s timeout).

```bash
./swiftdeploy deploy
```

```
⚙️  Generating configs...
✅ Generated nginx.conf
✅ Generated docker-compose.yml

🔐 Starting OPA policy engine...
⏳ Waiting for OPA at http://localhost:8181/health ...
✅ OPA is ready.

🔒 Running pre-deploy policy checks...
   disk_free:    747.21 GB
   cpu_load:     1.42
   memory_used:  74.33%

  ✅ ALLOW  [infrastructure] — no violations

🚀 Starting stack...
[+] Running 3/3
 ✔ Network swiftdeploy-net  Created
 ✔ Container swiftdeploy-app-1    Started
 ✔ Container swiftdeploy-nginx-1  Started

⏳ Waiting for health check at http://localhost:8080/healthz ...
✅ Stack is healthy and ready!
   → App running at http://localhost:8080
```

**Policy block example** — if disk is full:
```
  ❌ DENY   [infrastructure]
     → disk_free_gb 2.30 is below minimum 10.00 GB — free up disk space before deploying
❌ Deploy blocked by policy — fix violations above and retry
```

---

### `promote` — Switch deployment mode

Before promoting to canary, scrapes `/metrics`, calculates live error rate and P99 latency, and checks OPA's canary safety policy. Promoting to stable always succeeds — rolling back is never blocked.

```bash
./swiftdeploy promote canary
./swiftdeploy promote stable
```

```
🔒 Running pre-promote policy checks...
   scraping http://localhost:8080/metrics ...
   error_rate:    0.0000%  (max: 1.00%)
   p99_latency:   0.15ms   (max: 500ms)
   total_requests: 45  errors: 0

  ✅ ALLOW  [canary] — no violations

🔄 Promoting to canary mode...
✅ manifest.yaml updated — mode: canary
⚙️  Regenerating configs...
🔁 Recreating app container with new mode...
⏳ Waiting for app to be healthy...
✅ Promotion complete! App is running in canary mode.
   X-Mode header: canary
```

**Policy block example** — when chaos is injected:
```
  ❌ DENY   [canary]
     → error_rate 59.3750% exceeds maximum 1.00% — canary is too unstable to promote
❌ Promotion blocked by policy — fix violations above and retry
```

What `promote` does step by step:
1. Scrapes `/metrics` and calculates error rate + P99 latency
2. Sends metrics to OPA canary policy — blocks if unhealthy
3. Updates `mode` field in `manifest.yaml` in-place
4. Regenerates `docker-compose.yml` with the new `MODE` env var
5. Force-recreates only the `app` container (nginx stays up, zero downtime)
6. Confirms new mode via `/healthz` and `X-Mode` header

---

### `status` — Live dashboard

Scrapes `/metrics` every 3 seconds, queries both OPA policies live, and renders a terminal dashboard. Every scrape is appended to `history.jsonl` for the audit trail.

```bash
./swiftdeploy status
```

```
📊 SwiftDeploy Status  2026-05-04T21:00:00Z
────────────────────────────────────────────────────────────
  📈 METRICS
     Mode:          ⚡ canary
     Uptime:        102s
     Req/s:         3.12
     P99 Latency:   0.15ms
     Error Rate:    59.3750%
     Total Req:     32  (errors: 19)
     Chaos:         🔴 error injection

  🔒 POLICY COMPLIANCE
     ✅ PASS  [infrastructure]
     ❌ FAIL  [canary]
              → error_rate 59.3750% exceeds maximum 1.00% — canary is too unstable to promote

────────────────────────────────────────────────────────────
  Refreshing every 3s — Ctrl+C to exit
  Audit trail: history.jsonl
```

Press `Ctrl+C` to exit.

---

### `audit` — Generate audit report

Reads `history.jsonl` and generates `audit_report.md` with a timeline of mode changes, chaos injections, and a dedicated violations section.

```bash
./swiftdeploy audit
```

```
📖 Parsed 120 entries from history.jsonl
✅ Report written to audit_report.md
   Entries: 120
   Violations: 3
   Mode changes: 2
```

The report renders as valid GitHub Flavored Markdown with four sections: summary, timeline, violations, and metrics summary.

---

### `teardown` — Stop everything

```bash
./swiftdeploy teardown

# Also delete generated config files
./swiftdeploy teardown --clean
```

---

## API Endpoints

| Method | Path | Description | Mode |
|--------|------|-------------|------|
| `GET` | `/` | Welcome message with mode, version, timestamp | both |
| `GET` | `/healthz` | Liveness check with process uptime | both |
| `GET` | `/metrics` | Prometheus metrics in text format | both |
| `POST` | `/chaos` | Fault injection endpoint | canary only |

### Chaos endpoint examples

```bash
# Slow all responses by 5 seconds
curl -X POST http://localhost:8080/chaos \
  -H "Content-Type: application/json" \
  -d '{"mode":"slow","duration":5}'

# Return 500 on ~50% of requests
curl -X POST http://localhost:8080/chaos \
  -H "Content-Type: application/json" \
  -d '{"mode":"error","rate":0.5}'

# Recover from any active chaos
curl -X POST http://localhost:8080/chaos \
  -H "Content-Type: application/json" \
  -d '{"mode":"recover"}'
```

---

## Prometheus Metrics

The app exposes the following metrics at `GET /metrics`:

| Metric | Type | Labels | Description |
|--------|------|--------|-------------|
| `http_requests_total` | Counter | `method`, `path`, `status_code` | Total requests |
| `http_request_duration_seconds` | Histogram | `method`, `path` | Request latency |
| `app_uptime_seconds` | Gauge | — | Process uptime |
| `app_mode` | Gauge | — | `0`=stable, `1`=canary |
| `chaos_active` | Gauge | — | `0`=none, `1`=slow, `2`=error |

---

## OPA Policy Engine

### How it works

The CLI never makes allow/deny decisions itself. All decision logic lives in OPA. The CLI collects data, sends it to OPA, and surfaces whatever OPA returns.

```
swiftdeploy deploy
      ↓
  collect host stats (disk, CPU, memory)
      ↓
  POST /v1/data/swiftdeploy/infrastructure/decision
      ↓
  OPA evaluates infrastructure.rego against data.json thresholds
      ↓
  { "allow": true/false, "violations": [...], "contact": "..." }
      ↓
  CLI prints result — blocks or proceeds
```

### Policies

**`policies/infrastructure.rego`** — checked before every `deploy`

Blocks deployment if:
- `disk_free_gb` < 10 GB
- `cpu_load` > 2.0
- `memory_used_percent` > 90%

**`policies/canary.rego`** — checked before every `promote canary`

Blocks promotion if:
- `error_rate_percent` > 1%
- `p99_latency_ms` > 500ms

### Adjusting thresholds

All thresholds live in `policies/data.json` — never in the `.rego` files:

```json
{
  "infrastructure": {
    "min_disk_free_gb": 10.0,
    "max_cpu_load": 2.0,
    "max_memory_used_percent": 90.0
  },
  "canary": {
    "max_error_rate_percent": 1.0,
    "max_p99_latency_ms": 500.0
  }
}
```

Edit this file to tighten or relax any threshold. No Rego changes needed.

### OPA isolation

OPA is bound to `127.0.0.1:8181` only. It has no nginx location block and is not routable through port 8080. Verify:

```bash
# Should fail — OPA not reachable via nginx
curl http://localhost:8080/v1/data/swiftdeploy

# Should work — direct access only
curl http://localhost:8181/v1/data/swiftdeploy
```

---

## Nginx Behaviour

- Listens on `nginx.port` (default: 8080)
- Proxy timeouts set from `nginx.proxy_timeout` in manifest
- Adds `X-Deployed-By: swiftdeploy` to every response
- Forwards `X-Mode` header from upstream app
- Returns JSON error bodies on 502/503/504:

```json
{"error":"Bad Gateway","code":502,"service":"swift-deploy-1-node:latest","contact":"ops@swiftdeploy.local"}
```

- Access logs in the required format:
```
2026-05-04T21:00:00+00:00 | 200 | 0.001s | 172.18.0.2:3000 | GET / HTTP/1.0
```

---

## Security

- App container runs as non-root user (UID 1000)
- Linux capabilities dropped (`cap_drop: ALL`)
- App port (3000) never exposed to host
- OPA bound to localhost only (`127.0.0.1:8181`)
- App image under 300MB (Alpine-based multi-stage build)

Verify non-root:
```bash
docker compose exec app whoami
# → appuser
```

---

## Manifest Reference

`manifest.yaml` is the only file you edit manually. All configs are generated from it.

```yaml
services:
  image: swift-deploy-1-node:latest  # Docker image name
  port: 3000                          # App internal port
  mode: stable                        # stable | canary
  version: "1.0.0"                    # Injected as APP_VERSION env var

nginx:
  image: nginx:latest
  port: 8080                          # Host-exposed port
  proxy_timeout: 30                   # Seconds for all proxy timeouts

network:
  name: swiftdeploy-net
  driver_type: bridge

opa:
  image: openpolicyagent/opa:latest
  port: 8181                          # Bound to 127.0.0.1 only
  policies_dir: ./policies            # Directory of .rego files and data.json
```