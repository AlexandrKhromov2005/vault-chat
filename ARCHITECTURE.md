# Vault Chat — Architecture

> Corporate team messenger with security-first design.

This document describes the target architecture, not an inventory of deployed
features. Target features below are not evidence of completion. The current
system on `main` (v0.7.1, Android 0.2.1, 2026-11-27) is drawn in
[C1/C2](docs/c4.md); [README](README.md) lists implemented features and
[requirements and acceptance criteria](docs/requirements.md) the delivery stages
and course coverage.

| | Components |
|---|---|
| **Running in the base stack** | Auth, Chat, Media and Gateway (Go); Calls control plane (Erlang/OTP) with its PostgreSQL and coturn; web client; Gateway public profile cache in its own Redis ([ADR 0004](docs/adr/0004-public-profile-cache.md)); internal mTLS by default on the VPS ([mTLS](docs/mtls.md)); four PostgreSQL databases, Redis, MinIO; encrypted backups |
| **Built separately** | Android client (`android/`, Kotlin and Compose) |
| **Opt-in overlays** | Push: NATS JetStream signals Chat → Auth and FCM ([push](docs/push.md)); email account security ([mail](docs/mail.md)); image previews in isolated Zig workers ([ADR 0006](docs/adr/0006-image-previews.md)); Prometheus, Grafana and alerts ([monitoring](docs/monitoring.md)) |
| **Target, not implemented** | TOTP, RBAC, organizations; threads, reactions, edits/deletes, message search, presence; E2EE; malware scanning, retention, avatars; audit log; circuit breakers; tracing; Kubernetes and service mesh; Gateway replicas behind a load balancer; video and group calls |

Sections 2–5 mix both: rows marked *(target)* are not implemented. See
[Media](docs/media/files.md) for the implemented storage lifecycle and
development S3 limitations.

---

## 1. Architectural Style

**Microservices** with API Gateway pattern.

### Rationale

| Criterion | Decision |
|-----------|----------|
| Bounded Contexts | Each service owns exactly one domain: auth, chat, media, calls; presence is a target domain. |
| Team Size | 3 developers → 4 core services + gateway. Each service fits "two-pizza team" rule. |
| Independent Deploy | Services deploy independently; chat can be updated without touching auth. |
| Separate Databases | Every service owns its data exclusively; no cross-service table access. |

### Why not Monolith?

A modular monolith would be easier for 3 people short-term, but:
- The course requires demonstrating microservice decomposition and justification.
- Open-source projects benefit from independent scaling (auth is read-heavy, chat is write-heavy).
- Future contributors can own a single service without learning the entire codebase.

---

## 2. Service Catalog

The sketch below is the original target layout. The implemented containers,
including Calls, coturn, the profile cache, NATS and preview workers, are in
[C2](docs/c4.md#c2-контейнеры-и-связи).

```
                    ┌─────────────┐
    Clients  ──────▶│   Gateway   │◀───── mTLS
    (Web/Mobile)    │  (Public)   │
                    └──────┬──────┘
                           │ gRPC
           ┌───────────────┼───────────────┐
           ▼               ▼               ▼
    ┌──────────┐   ┌──────────┐   ┌──────────┐
    │   Auth   │   │   Chat   │   │  Media   │
    │ Service  │   │ Service  │   │ Service  │
    └──────────┘   └──────────┘   └──────────┘
           │               │               │
           ▼               ▼               ▼
      ┌─────────┐    ┌─────────┐    ┌─────────┐
      │  Auth   │    │  Chat   │    │  Media  │
      │   DB    │    │   DB    │    │   DB    │
      │(Postgre)│    │(Postgre)│    │(Postgre)│
      └─────────┘    └────┬────┘    └─────────┘
                          │
                     ┌────┴────┐
                     │  Redis  │
                     │ (Cache, │
                     │ Pub/Sub)│
                     └─────────┘
```

### 2.1 Gateway Service (`cmd/gateway/`)

**Responsibility:** Single entry point for all clients.

| Aspect | Detail |
|--------|--------|
| Protocol | HTTP/1.1, HTTP/2, WebSocket (for real-time) |
| Auth | Validates JWT via Auth service; rejects unauthorized requests at the edge |
| Rate Limiting | Redis-backed token bucket per client IP / user ID |
| Routing | REST → gRPC translation to backend services |
| Cache | Public profiles in a dedicated Redis `profile-cache` ([ADR 0004](docs/adr/0004-public-profile-cache.md)) |
| Security | TLS termination, CORS, request size limits |

### 2.2 Auth Service (`cmd/auth/`)

**Responsibility:** Identity, access control, sessions.

| Aspect | Detail |
|--------|--------|
| Features | Registration by invitation, password login, JWT issuance/refresh, sessions and revocation, optional email verification and recovery, push tokens and dispatch; *(target)* TOTP, RBAC, org/team management |
| DB | PostgreSQL (`auth_db`) — users, credentials (hashed), sessions, invitations, push tokens; *(target)* roles, orgs |
| Cache | None: sessions are checked in `auth_db` on every call; Auth only publishes revocations to Redis |
| Crypto | Argon2id for passwords, RS256 for JWT; *(target)* TOTP for 2FA |
| API | gRPC internally; REST via Gateway |

### 2.3 Chat Service (`cmd/chat/`)

**Responsibility:** Messaging, rooms, threads, history, search.

| Aspect | Detail |
|--------|--------|
| Features | Direct messages, channels, attachments, message history with pagination; *(target)* threads, full-text search, reactions, edits, deletes |
| DB | PostgreSQL (`chat_db`) — messages, rooms, memberships |
| Cache | None; *(target)* recent message lists, unread counters, presence |
| Real-time | WebSocket hub (via Gateway) + Redis Pub/Sub for multi-instance broadcast |
| Search | *(target)* PostgreSQL `tsvector`, then Elasticsearch |
| API | gRPC internally; WebSocket for real-time; REST via Gateway |

### 2.4 Media Service (`cmd/media/`)

**Responsibility:** File uploads, downloads, avatars, attachments.

| Aspect | Detail |
|--------|--------|
| Features | Upload, download, image previews in isolated workers (opt-in); *(target)* avatars, virus scanning (ClamAV), retention policies |
| Storage | MinIO (S3-compatible) for blob storage |
| DB | PostgreSQL (`media_db`) — file metadata and ownership |
| Cache | None; *(target)* presigned URL cache |
| Security | File type and content validation, size limits, access via `Auth.ValidateToken` and current membership from `Chat.GetRoom`; *(target)* malware scanning |

### 2.5 Calls Service (`calls/`, Erlang/OTP)

**Responsibility:** One-to-one audio call control. Media flows between clients
directly or through coturn, never through the services.

| Aspect | Detail |
|--------|--------|
| Features | Invite, accept, reject, cancel, end, WebRTC signals ordered by `signal_seq`, heartbeats, TURN credentials; one supervised process per call; *(target)* video and group calls |
| DB | PostgreSQL (`calls_db`) — calls, participants, command and signal journal; recovery after restart |
| Events | Redis Pub/Sub call events to clients and best-effort push hints to Auth |
| Security | Accepts gRPC only from Gateway; checks the caller's token and direct room via Auth and Chat, re-checks sessions and membership of ongoing calls |
| Details | [ADR 0005](docs/adr/0005-call-control.md), [operations](docs/calls/operations.md) |

---

## 3. Inter-Service Communication

### 3.1 Synchronous (gRPC)

Used for request/response operations requiring immediate consistency.

**Implementation:** the official [`google.golang.org/grpc`](https://pkg.go.dev/google.golang.org/grpc) (grpc-go) library for servers and clients; `.proto` sources in `api/proto/`, code generated with [`buf`](https://buf.build). Connect-RPC was considered and rejected: grpc-go is the reference implementation with the most mature interceptor / mTLS / load-balancing ecosystem, and the team already knows it.

Calls uses `grpcbox` generated from the same `.proto` files.

| Call | From | To | Purpose |
|------|------|-----|---------|
| Any `AuthService`, `ChatService`, `MediaService`, `CallsService` method | Gateway | Auth, Chat, Media, Calls | REST → gRPC for client requests, including `ValidateToken` on every request |
| `ValidateToken`, `GetUser` | Chat | Auth | Verify the caller; check that an added user exists |
| `GetFile` | Chat | Media | Verify an attachment before storing a message |
| `ValidateToken` | Media | Auth | Verify the caller |
| `GetRoom` | Media | Chat | Current membership for uploads and every download |
| `Decode` | Media | Preview (opt-in) | Decode an image in an isolated worker |
| `ValidateToken`, `ValidateSession` | Calls | Auth | Verify the caller; re-check sessions of ongoing calls |
| `GetRoom`, `CheckMembership` | Calls | Chat | Resolve the direct room and callee; re-check membership |

The enforced matrix is in [mTLS](docs/mtls.md#матрица-rpc). Every other pair is
denied.

### 3.2 Asynchronous (Redis Pub/Sub, NATS JetStream)

Used for events, notifications, decoupled side effects.

Implemented:

| Event | Publisher | Subscribers | Purpose |
|-------|-----------|-------------|---------|
| `chat:user:<id>` (Redis) | Chat, Calls, Auth | Gateway | Live message, room and call events to WebSockets, and Auth's session-revoked hint that makes Gateway re-check the user's sockets; lost events are recovered by catch-up |
| `push.signal.v1` (JetStream, opt-in) | Chat | Auth | Durable push signal with ACK and retries ([ADR 0003](docs/adr/0003-push-signals-jetstream.md)) |
| `push:signal:v1` (Redis) | Calls | Auth | Best-effort push hint for an incoming call |

*(target)*:

| Event | Publisher | Subscribers | Purpose |
|-------|-----------|-------------|---------|
| `user.registered` | Auth | Chat, Media | Pre-provision user workspace |
| `file.scanned` | Media | Chat | Approve/reject attachment in message |
| `user.presence_changed` | Gateway | Chat | Update online status |

### 3.3 Service Mesh (Future)

Current implementation: direct gRPC with mTLS, verified service identities
and unary/stream RPC ACLs; on by default on the VPS.
Local plaintext remains for development. See [ADR 0001](docs/adr/0001-internal-mtls.md).
Linkerd or Istio remain future options
for managing mTLS, retries and circuit breakers.

---

## 4. Data Architecture

### 4.1 Database per Service

| Service | Primary DB | Purpose |
|---------|-----------|---------|
| Auth | PostgreSQL | Users, passwords, sessions, invitations, push tokens; *(target)* orgs, roles |
| Chat | PostgreSQL | Messages, rooms, memberships; *(target)* threads |
| Media | PostgreSQL | File metadata; *(target)* retention rules |
| Calls | PostgreSQL | Calls, participants, command and signal journal |

**Rule:** No service queries another service's database directly. All data access via API.

### 4.2 Caching Strategy (Redis)

| Key Pattern | TTL | Purpose |
|-------------|-----|---------|
| `rate:*` | window | Rate limit buckets (shared Redis) |
| `cache:profile:v1:<user_id>` | 10m, 1m for a missing account | Public profiles (separate `profile-cache` Redis) |
| *(target)* `room:recent:<room_id>` | 1h | Last 50 messages (pagination cache) |
| *(target)* `unread:<user_id>` | 5m | Unread message counters |
| *(target)* `presence:<user_id>` | 2m | Online/away/last seen |

Sessions are not cached: Auth checks them in `auth_db` on every call.

### 4.3 Blob Storage (MinIO)

| Bucket | Content | Lifecycle |
|--------|---------|-----------|
| `vault-chat-media` | Message attachments | Kept; unpublished uploads are collected ([Media](docs/media/files.md)) |
| *(target)* avatars, exports | User/org avatars, data export archives | — |

---

## 5. Security Architecture

### 5.1 Defense in Depth

```
┌─────────────────────────────────────────────┐
│  Layer 1: Edge (DDoS, WAF) — Cloudflare/    │
│           nginx rate limit                  │
├─────────────────────────────────────────────┤
│  Layer 2: Gateway (TLS, JWT validation,     │
│           CORS, request limits)             │
├─────────────────────────────────────────────┤
│  Layer 3: Service (mTLS, ACL checks,        │
│           input validation)                 │
├─────────────────────────────────────────────┤
│  Layer 4: Database (RLS, encrypted at rest, │
│           least-privilege users)            │
├─────────────────────────────────────────────┤
│  Layer 5: Application (E2EE for DMs,        │
│           audit logs, data retention)       │
└─────────────────────────────────────────────┘
```

### 5.2 Authentication & Authorization

- **AuthN:** JWT (RS256, short-lived access tokens + long-lived refresh tokens). *(target)* Optional TOTP 2FA.
- **AuthZ:** implemented as room membership and channel ownership checked by the data owner; *(target)* RBAC with permissions: `message:read`, `message:write`, `room:admin`, `org:owner`, etc.
- **Service-to-service:** mTLS with internal CA; each service has its own certificate.

### 5.3 Encryption

| Layer | Method | Notes |
|-------|--------|-------|
| Transport | TLS 1.3 | Client ↔ Gateway, Gateway ↔ Services |
| Service mesh | mTLS | Service ↔ Service |
| Database | At-rest encryption (planned) | Select and verify a storage-level or database-supported mechanism; not implemented |
| Backup | age (X25519, ChaCha20-Poly1305) | Streamed encryption before upload to backup storage (`scripts/vps/encrypted_backup.py`) |
| End-to-end (DMs) | *(target)* Signal Protocol / Double Ratchet | Optional; server sees only ciphertext |

### 5.4 Audit & Compliance *(target)*

- All admin actions logged to immutable audit log (append-only table).
- Message edits/deletes soft-deleted with tombstones (compliance retention).
- GDPR: data export API, right to erasure (hard delete with 30-day grace).

---

## 6. High Availability & Resilience

### 6.1 Deployment Topology

Implemented: one VPS with Docker Compose, one instance of each service
([deployment](docs/deployment.md)). The topology below is the target; two
Gateway replicas behind a load balancer are tracked in #180.

```
                    ┌─────────┐
                    │  LB     │
                    │ (nginx) │
                    └────┬────┘
           ┌─────────────┼─────────────┐
           ▼             ▼             ▼
      ┌─────────┐  ┌─────────┐  ┌─────────┐
      │Gateway 1│  │Gateway 2│  │Gateway 3│
      └────┬────┘  └────┬────┘  └────┬────┘
           └─────────────┼─────────────┘
                         │
              ┌──────────┼──────────┐
              ▼          ▼          ▼
         ┌──────┐   ┌──────┐   ┌──────┐
         │Auth 1│   │Auth 2│   │Auth 3│
         └──────┘   └──────┘   └──────┘
              ▲          ▲          ▲
              └──────────┼──────────┘
                         │
                    ┌────┴────┐
                    │PostgreSQL│
                    │ Primary │
                    │  + Hot  │
                    │ Standby │
                    └─────────┘
```

### 6.2 Resilience Patterns

| Pattern | Implementation | Purpose |
|---------|---------------|---------|
| Circuit Breaker | *(target)* `github.com/sony/gobreaker` | Fail fast when downstream is unhealthy |
| Retry + Backoff | *(target)* gRPC interceptor with exponential backoff; today the client retries and sends are idempotent | Transient network failures |
| Timeout | Context deadlines per request | Prevent cascading hangs |
| Health Checks | `/health` and `/ready` probes, gRPC health in Compose healthchecks | Orchestrator decisions |
| Graceful Degradation | Chat works without Redis (events are lost and recovered by catch-up); Calls' monitor does not end calls while Auth or Chat is unavailable, though without Auth Gateway rejects heartbeats and a call ends within 45 s | Partial outages |

### 6.3 Scaling Strategy

| Service | Bottleneck | Scale Strategy |
|---------|-----------|----------------|
| Gateway | Connections | Horizontal (stateless) |
| Auth | DB reads | Horizontal + Redis cache |
| Chat | DB writes | Horizontal + read replicas + sharding by org (future) |
| Media | Storage I/O | Horizontal + CDN for downloads |

---

## 7. Observability

### 7.1 Logging

- Structured JSON logs via `log/slog`.
- Correlation ID propagated across all services (via gRPC metadata / HTTP headers).
- Log levels: `DEBUG` (dev), `INFO` (prod), `WARN`, `ERROR`.
- Sensitive fields redacted (passwords, tokens).

### 7.2 Metrics

| Metric | Tool | Purpose |
|--------|------|---------|
| Request latency (p50/p95/p99) | Prometheus + Grafana | Performance baselines |
| Error rate by endpoint | Prometheus | Alert on spikes |
| Active connections | Prometheus | Capacity planning |
| DB query duration | pg_stat_statements + Prometheus | Slow query detection |
| Cache hit ratio | Redis INFO + Prometheus | Cache effectiveness |

### 7.3 Tracing *(target)*

- OpenTelemetry / Jaeger for distributed tracing; today a request ID is
  propagated instead.
- Trace spans: Gateway → Auth (validate) → Chat (save) → Redis (publish).

---

## 8. CI/CD Pipeline (Local)

Local verification via `xmake` is implemented; no cloud runners are configured.
`xmake setup` installs pre-commit and pre-push hooks. Commit first, then run the
full `xmake ci` pipeline on a clean checkout: it tests that exact commit in a
separate checkout and writes commit-bound evidence only after success. Do not
edit the checkout during CI. The pre-push hook validates fresh evidence for each
pushed tip; it does not start CI. Publish the verified status, then obtain
independent approval of the latest push before merging.

```
Developer Machine:
pre-commit hook → staged secret scan + fast formatting, vet and lint checks
        │
        ▼
git commit → clean-checkout xmake ci → commit-bound evidence
        │
        ▼
git push → pre-push hook validates the pushed commit's evidence
        │
        ▼
python3 scripts/dev/verification.py publish → vault/local-ci status
        │
        ▼
PR → independent approval of latest push → guarded merge to main
```

---

## 9. Technology Stack

| Layer | Technology |
|-------|-----------|
| Language | Go 1.26+ (toolchain go1.27.4); Erlang/OTP 28 for Calls; Zig 0.17.1 for the preview decoder; JavaScript modules for the web client; Kotlin and Jetpack Compose for Android |
| API (external) | HTTP/2, REST, WebSocket |
| API (internal) | gRPC (grpc-go, grpcbox in Calls) + Protocol Buffers (codegen via buf) |
| Message Bus | Redis Pub/Sub for live events; NATS JetStream for push signals (opt-in) |
| Primary DB | PostgreSQL 16+ |
| Cache | Redis 7+ |
| Blob Storage | MinIO (S3-compatible) |
| Calls media | WebRTC, coturn |
| Auth | JWT (RS256), Argon2id; *(target)* TOTP |
| Encryption | TLS 1.3, mTLS, age for backups |
| Container | Docker, Docker Compose; *(target)* Kubernetes |
| Observability | slog, Prometheus, Grafana, Alertmanager; *(target)* Jaeger |
| Build | xmake |

---

## 10. Project Structure

```
vault-chat/
├── cmd/
│   ├── gateway/ auth/ chat/ media/   # Go service entry points
│   ├── preview/          # Isolated preview worker (opt-in)
│   ├── migrate/          # One-shot migration job for each Go service
│   └── healthcheck/ invite/ s3-init/ # Container healthcheck, invitation and S3 setup tools
├── internal/
│   ├── gateway/          # handler, middleware, router, response, ratelimit,
│   │                     # realtime (WebSocket hub), profilecache
│   ├── auth/             # domain, service, repository, handler, validator,
│   │                     # events, mail, push
│   ├── chat/             # domain, service, repository, handler, validator,
│   │                     # events, media (client of Media)
│   ├── media/            # domain, service, handler, validator, storage (S3), preview
│   └── shared/           # config, eventbus, jwt, logger, migration, probe,
│                         # pushbus, ratelimit, requestid, rpctls, telemetry, transfer
├── calls/                # Calls control plane (Erlang/OTP, rebar3); migrations in priv/
├── web/                  # Web client served by Gateway
├── android/              # Android client (Kotlin, Gradle)
├── tools/preview/        # Zig preview decoder and Go reference decoder
├── api/
│   └── proto/            # .proto files for all services
├── migrations/
│   ├── auth/ chat/ media/ # Go service DB migrations
├── deployments/          # Compose files: base, VPS and opt-in overlays
│                         # (mtls, push, email, preview, monitoring)
├── docs/                 # API, ADRs, C4, operations and per-service docs
├── scripts/              # VPS operations, monitoring and development tools
├── tests/
│   ├── integration/      # PostgreSQL/S3-backed integration tests
│   ├── compose/          # Tests against a running Compose stack
│   └── ops/ dev/         # Tests of operations and development scripts
├── xmake.lua
├── go.mod
├── go.sum
├── README.md
├── ARCHITECTURE.md
├── CONTRIBUTING.md
└── .golangci.yml
```

Unit and fuzz tests are colocated with the packages they exercise in `*_test.go`
files; fuzz crash corpora live in package `testdata/fuzz/` directories.

---

## 11. Data Flow Examples

### 11.1 Send a Message

Implemented:

```
Client → Gateway (REST)
       → Gateway validates JWT (calls Auth via gRPC)
       → Gateway routes to Chat service (gRPC)
       → Chat validates the token in Auth and attachments in Media
       → Chat saves to PostgreSQL
       → Chat publishes to Redis Pub/Sub
       → Gateway WebSocket hubs deliver to room members
       → (push, opt-in) Chat stores a push signal in NATS JetStream
       → Auth consumes it and sends FCM pushes without message content
```

*(target)* Media scans attachments for malware after `message.sent`.

### 11.2 Upload a File

Implemented:

```
Client → Gateway (multipart upload)
       → Gateway streams to Media service
       → Media validates the token in Auth and membership in Chat
       → Media validates file type, size and content
       → Media writes a pending row, uploads to MinIO,
         re-checks membership and publishes the row
       → (previews, opt-in) Media queues an image for an isolated worker
       → Client attaches the file id to a message; Chat checks it in Media
```

*(target)* virus scan (ClamAV) and `file.scanned` approval of attachments.

---

## 12. Future Evolution

These tracks describe planning priorities, not release numbers or delivery dates.
Released features are recorded in [CHANGELOG](CHANGELOG.md); assign future
versions under [VERSIONING](VERSIONING.md) after the scope is agreed. Calls
were first released in `v0.7.0`.

| Track | Feature | Architecture direction |
|-------|---------|------------------------|
| MVP baseline | Auth, DM, channels, file upload | Implemented: four Go services and Docker Compose |
| Collaboration | Search, threads, reactions | *(target)* Add Elasticsearch, Chat sharding |
| Account and message security | E2EE DMs, TOTP 2FA | *(target)* Add Signal Protocol client lib |
| Calls | Personal audio implemented (`v0.7.0`); video and groups later | Erlang/OTP call control, WebRTC P2P with coturn (§12.1) |
| Media processing | Image previews implemented (opt-in); voice messages and waveforms later | Isolated Zig worker (§12.2) |
| Enterprise identity | SSO (SAML/OIDC) | *(target)* Extend Auth with identity providers |
| Deployment scale | Kubernetes, auto-scaling | *(target)* Helm charts, HPA, service mesh |

**Planning decision, 2026-10-31:** add the Erlang and Zig investigations below.
Both have since been implemented: Calls runs in the base stack, Zig previews as
an opt-in overlay. Auth, Chat, Media and Gateway remain on Go and were not
rewritten. The remaining gates are E4–E5 and Z4.

### 12.1 Erlang/OTP: call control

**Implemented scope.** One-to-one audio calls in direct rooms, released in
`v0.7.0` ([ADR 0005](docs/adr/0005-call-control.md)). The original candidate
scope was to start with one-to-one audio calls: invite, ring, accept,
reject, cancel, connect and end. **Erlang/OTP is the selected direction for the
new Calls control plane** (architecture owner, review of #121, 2026-11-03);
Auth, Chat, Media and Gateway stay on Go and are not rewritten. The choice
follows the domain: many independent long-lived state machines with timers, a
mailbox per call, failure isolation and supervision (an OTP process per call,
using `gen_statem`). It is not a claim about media throughput: media goes
peer-to-peer or through an SFU. A Go implementation of the same contract may
serve as a reference baseline; Erlang does not have to beat it, but it must pass
the gates recorded in [ADR 0005](docs/adr/0005-call-control.md).

Call control and media transport are separate decisions. Evaluate WebRTC
peer-to-peer with TURN versus an SFU such as self-hosted LiveKit; account for
bandwidth, network reachability, operational cost and future group calls.
Audio/video packets do not travel through the existing Chat event stream.
Define any E2EE requirement separately from transport encryption. Video,
conferences and recording require their own scope and acceptance decisions.

**Trust boundaries and recovery.** Clients send untrusted commands and connection
descriptions. Auth remains authoritative for live sessions, and Chat for room
membership. Define service identities and mTLS ACLs, media admission credentials,
their lifetime and the policy for logout or removal during an active call.
New admission must fail closed when Auth or Chat cannot authorize it. Explicitly
document the limits of server-enforced termination for the chosen media topology.

The current [WebSocket contract](docs/chat/realtime.md) accepts only client auth
frames; signaling needs a separately reviewed API and bounded messages. Redis
Pub/Sub events can be lost, so define authoritative call state, versions,
idempotent commands and reconciliation after reconnect. Decide how state is
recovered, or the call is safely ended, after process/node failure. An OTP
supervisor restart alone does not restore state; multiple instances must not
independently accept conflicting transitions for the same call.

**Budgets before a prototype.** Record numeric limits for active/ringing calls
per user and instance, participants, command rate, SDP/ICE size and count, queue
depth, setup/ringing/idle timeouts and shutdown. Specify overload responses and
bounded dependency deadlines. Queues and recovery attempts must remain bounded
during Redis, Auth, Chat or media-server outages.

Implementation and remaining rollout stages:

- [x] **E1 — design:** agree on the first client scenario, an owner able to
  maintain OTP, contracts, threat model and budgets. Write an ADR comparing Go
  and Erlang/OTP, including deployment, observability and state recovery.
- [x] **E2 — prototype and measure:** implement the call-control contract in
  Erlang/OTP, optionally with a Go reference baseline, on a disposable test stack.
  Measure setup p95/p99, throughput, errors, CPU, peak memory and recovery under
  the same workload, including a crash of one call process while other calls
  continue; report media-server and TURN costs separately. Commit fixtures and
  commands.
- [x] **E3 — acceptance:** cover malformed/oversized signaling, expired or revoked
  sessions, nonmembers, removal during a call, two devices accepting together,
  duplicate/reordered commands, lost `accepted`/`ended` events, reconnect,
  process/node restart and dependency outages. Verify bounded overload behavior.
  E1–E3 are recorded in ADR 0005; the 200-call load and crash/`kill -9`
  recovery run in `xmake ci` (`scripts/dev/calls-load`).
- [ ] **E4 — client acceptance** (#161–#163, #211): test browser and Android devices, permissions,
  background incoming calls, delayed/missed push, network loss, Wi-Fi/mobile
  handover and TURN fallback. Depend on reviewed session and push behavior;
  an open client or push PR does not satisfy this gate.
- [ ] **E5 — rollout decision** (#160; Calls is already in the base VPS
  Compose since `v0.7.0`, so this gate records evidence after the fact): record the gate results in ADR 0005. A failed
  correctness, security, SLO, resource or operability gate blocks rollout and
  requires rework; changing the runtime needs a separate explicit architecture
  decision. Add pinned tooling, CI, metrics, deployment and rollback acceptance
  in separate focused PRs before enabling calls.

### 12.2 Zig: isolated media processing

**Implemented scope.** PNG/JPEG previews run through the opt-in isolated worker
overlay. The [Media service](docs/media/files.md) validates uploads and stores private objects;
it does not decode audio/video. **Zig is the selected language
for the isolated preview decoder** (architecture owner, review of #122,
2026-11-04); it is an architecture decision, not a measured result. A Go
implementation of the same contract is the reference baseline. A 2× gain over
it is a hypothesis to measure, not a veto: missing it calls for analysis and
optimization, and Zig must pass the gates recorded in
[ADR 0006](docs/adr/0006-image-previews.md). Compare with suitable existing
image-processing tools too, using equivalent output and codec settings.

In the implemented design, Go Media owns authorization, metadata, task scheduling
and publication of results. The worker is a separate process/container receiving
a bounded job and returning a bounded preview. It gets neither Auth secrets nor unrestricted
storage access, arbitrary client paths or URLs. Media validates the result before
publishing it. Derived files remain private and use the original file's current
room-access checks; a job must not create a public or permanent access bypass.

**Budgets and failure behavior.** Before a prototype, fix numeric limits for
input bytes, decoded dimensions/pixels and memory, output bytes, CPU, elapsed
time, parallel workers, queue size and retries. The existing 10 MiB upload and
40-million-pixel header checks are upper input bounds, not sufficient decoder
memory budgets. A timeout, crash or OOM must leave the original intact and the
main upload/download service available. An unavailable preview uses a placeholder;
retries are bounded and duplicate jobs must not publish conflicting results.

Implementation and remaining rollout stages:

- [x] **Z1 — design and baseline:** specify the preview contract, codec/library
  choices, job identity, access checks, budgets and cancellation/cleanup behavior.
  Measure the Go baseline first; define the benefit required for adoption before
  comparing results. A learning experiment is not a production-performance claim.
- [x] **Z2 — isolated prototype:** benchmark the worker on the same hardware and
  synthetic image corpus, including IPC, decoding and encoding costs. Compare
  output quality, throughput, p95/p99, peak memory and maintenance/build cost;
  attribute any improvement from a different codec library separately.
- [x] **Z3 — acceptance:** cover corrupt/truncated files, excessive decoded size,
  invalid worker output, unauthorized result access, membership changes, duplicate
  jobs, a full queue, timeout/OOM/crash and S3/database outages. Commit disposable
  fixtures, behavioral regressions and fuzz targets; verify bounded recovery and
  preservation of originals.
- [ ] **Z4 — rollout decision:** record the gate results in ADR 0006. A failed
  correctness, security, resource or operability gate blocks rollout and requires
  rework; changing the language needs a separate explicit architecture decision.
  Pin compiler and native dependencies, extend CI to build/test the worker,
  verify supported deployment architectures and document rollback before
  production integration. Existing Go CI is not evidence that Zig was checked.
- [ ] **Z5 — later candidates:** reconsider waveform/audio analysis only after
  voice-message formats, permissions and storage limits are designed. Consider
  WebAssembly for a concrete browser need. Direct cgo or Erlang NIF integration
  requires separate evidence that IPC is a bottleneck and a reviewed failure
  boundary; it is not the initial design. The current [container build](Dockerfile)
  disables cgo, and a native NIF crash can terminate the whole Erlang VM.

Technical references: [OTP state machines](https://www.erlang.org/doc/system/statem.html),
[WebRTC signaling](https://webrtc.org/getting-started/peer-connections),
[TURN](https://webrtc.org/getting-started/turn-server),
[LiveKit SFU](https://docs.livekit.io/reference/internals/livekit-sfu/),
[Zig interoperability and memory](https://ziglang.org/learn/overview/),
[Erlang NIF failure boundary](https://www.erlang.org/doc/system/nif.html).
