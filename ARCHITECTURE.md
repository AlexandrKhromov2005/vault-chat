# Vault Chat — Architecture

> Corporate team messenger with security-first design.

---

## 1. Architectural Style

**Microservices** with API Gateway pattern.

### Rationale

| Criterion | Decision |
|-----------|----------|
| Bounded Contexts | Each service owns exactly one domain: auth, chat, media, presence. |
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

```
                    ┌─────────────┐
    Clients  ──────▶│   Gateway   │◀───── mTLS
    (Web/Mobile)    │  (Public)   │
                    └──────┬──────┘
                           │ gRPC / NATS
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
| Security | TLS termination, CORS, request size limits |

### 2.2 Auth Service (`cmd/auth/`)

**Responsibility:** Identity, access control, sessions.

| Aspect | Detail |
|--------|--------|
| Features | Registration, login (password + TOTP), JWT issuance/refresh, RBAC, org/team management |
| DB | PostgreSQL (`auth_db`) — users, credentials (hashed), sessions, roles, orgs |
| Cache | Redis — revoked token blacklist, session metadata |
| Crypto | Argon2id for passwords, RS256 for JWT, TOTP for 2FA |
| API | gRPC internally; REST via Gateway |

### 2.3 Chat Service (`cmd/chat/`)

**Responsibility:** Messaging, rooms, threads, history, search.

| Aspect | Detail |
|--------|--------|
| Features | Direct messages, channels (public/private), threads, message history with pagination, full-text search, reactions, edits, deletes |
| DB | PostgreSQL (`chat_db`) — messages, rooms, memberships, threads |
| Cache | Redis — recent message lists, unread counters, presence cache |
| Real-time | WebSocket hub (via Gateway) + Redis Pub/Sub for multi-instance broadcast |
| Search | PostgreSQL `tsvector` (MVP) → Elasticsearch (future) |
| API | gRPC internally; WebSocket for real-time; REST via Gateway |

### 2.4 Media Service (`cmd/media/`)

**Responsibility:** File uploads, downloads, avatars, attachments.

| Aspect | Detail |
|--------|--------|
| Features | Upload (multipart), download, image resizing/thumbnails, virus scanning (ClamAV), retention policies |
| Storage | MinIO (S3-compatible) for blob storage |
| DB | PostgreSQL (`media_db`) — file metadata, ownership, retention rules |
| Cache | Redis — presigned URL cache |
| Security | File type validation, size limits, malware scanning, access control via Auth service |

---

## 3. Inter-Service Communication

### 3.1 Synchronous (gRPC)

Used for request/response operations requiring immediate consistency.

**Implementation:** the official [`google.golang.org/grpc`](https://pkg.go.dev/google.golang.org/grpc) (grpc-go) library for servers and clients; `.proto` sources in `api/proto/`, code generated with [`buf`](https://buf.build). Connect-RPC was considered and rejected: grpc-go is the reference implementation with the most mature interceptor / mTLS / load-balancing ecosystem, and the team already knows it.

| Call | From | To | Purpose |
|------|------|-----|---------|
| `ValidateToken` | Gateway | Auth | JWT verification on every request |
| `GetUser` | Chat | Auth | Resolve sender info for messages |
| `CheckPermission` | Chat | Auth | Verify room membership / admin rights |
| `GetFileACL` | Gateway | Media | Verify download permissions |

### 3.2 Asynchronous (NATS / Redis Streams)

Used for events, notifications, decoupled side effects.

| Event | Publisher | Subscribers | Purpose |
|-------|-----------|-------------|---------|
| `user.registered` | Auth | Chat, Media | Pre-provision user workspace |
| `message.sent` | Chat | Media (scan), Notification | Trigger file scan, send push |
| `file.scanned` | Media | Chat | Approve/reject attachment in message |
| `user.presence_changed` | Gateway | Chat | Update online status |

### 3.3 Service Mesh (Future)

For now: direct gRPC with mTLS certificates. Future: Linkerd or Istio for automatic mTLS, retries, circuit breakers.

---

## 4. Data Architecture

### 4.1 Database per Service

| Service | Primary DB | Purpose |
|---------|-----------|---------|
| Auth | PostgreSQL | Users, passwords, sessions, orgs, roles |
| Chat | PostgreSQL | Messages, rooms, memberships, threads |
| Media | PostgreSQL | File metadata, retention rules |

**Rule:** No service queries another service's database directly. All data access via API.

### 4.2 Caching Strategy (Redis)

| Key Pattern | TTL | Purpose |
|-------------|-----|---------|
| `token:blacklist:<jti>` | until expiry | Revoked JWT tokens |
| `session:<id>` | 24h | Active sessions |
| `room:recent:<room_id>` | 1h | Last 50 messages (pagination cache) |
| `unread:<user_id>` | 5m | Unread message counters |
| `presence:<user_id>` | 2m | Online/away/last seen |
| `rate:<ip>` | 1m | Rate limit buckets |

### 4.3 Blob Storage (MinIO)

| Bucket | Content | Lifecycle |
|--------|---------|-----------|
| `vault-chat-avatars` | User/org avatars | Keep forever |
| `vault-chat-attachments` | Message attachments | 90 days default, configurable |
| `vault-chat-exports` | Data export archives | 7 days |

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

- **AuthN:** JWT (RS256, short-lived access tokens + long-lived refresh tokens). Optional TOTP 2FA.
- **AuthZ:** RBAC with permissions: `message:read`, `message:write`, `room:admin`, `org:owner`, etc.
- **Service-to-service:** mTLS with internal CA; each service has its own certificate.

### 5.3 Encryption

| Layer | Method | Notes |
|-------|--------|-------|
| Transport | TLS 1.3 | Client ↔ Gateway, Gateway ↔ Services |
| Service mesh | mTLS | Service ↔ Service |
| Database | AES-256 | PostgreSQL transparent data encryption |
| Backup | AES-256-GCM | Encrypted before upload to backup storage |
| End-to-end (DMs) | Signal Protocol / Double Ratchet | Optional; server sees only ciphertext |

### 5.4 Audit & Compliance

- All admin actions logged to immutable audit log (append-only table).
- Message edits/deletes soft-deleted with tombstones (compliance retention).
- GDPR: data export API, right to erasure (hard delete with 30-day grace).

---

## 6. High Availability & Resilience

### 6.1 Deployment Topology

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
| Circuit Breaker | `github.com/sony/gobyence` | Fail fast when downstream is unhealthy |
| Retry + Backoff | gRPC interceptor with exponential backoff | Transient network failures |
| Timeout | Context deadlines per request | Prevent cascading hangs |
| Health Checks | `/health` and `/ready` probes | Kubernetes / orchestrator decisions |
| Graceful Degradation | Chat works without Media (attachments show as placeholders) | Partial outages |

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

### 7.3 Tracing

- OpenTelemetry / Jaeger for distributed tracing.
- Trace spans: Gateway → Auth (validate) → Chat (save) → Redis (publish).

---

## 8. CI/CD Pipeline (Local)

All verification runs locally via `xmake`. No cloud runners.

```
Developer Machine:
┌──────────────────────────────────────────────────────┐
│  git push → pre-push hook → xmake ci                 │
│  ├─ gofmt / goimports                                 │
│  ├─ go vet                                            │
│  ├─ golangci-lint                                     │
│  ├─ go test -race -cover (unit tests)                 │
│  ├─ go test -fuzz (fuzz tests, 30s)                   │
│  ├─ coverage >= 70% check                             │
│  └─ go build (all services)                           │
└──────────────────────────────────────────────────────┘
         │
         ▼
   PR created → code review (1 approve) → merge to main
```

---

## 9. Technology Stack

| Layer | Technology |
|-------|-----------|
| Language | Go 1.23+ |
| API (external) | HTTP/2, REST, WebSocket |
| API (internal) | gRPC (grpc-go) + Protocol Buffers (codegen via buf) |
| Message Bus | NATS (or Redis Streams as fallback) |
| Primary DB | PostgreSQL 16+ |
| Cache | Redis 7+ |
| Blob Storage | MinIO (S3-compatible) |
| Auth | JWT (RS256), Argon2id, TOTP |
| Encryption | TLS 1.3, mTLS, AES-256 |
| Container | Docker, Docker Compose (local), Kubernetes (future) |
| Observability | slog, Prometheus, Grafana, Jaeger |
| Build | xmake |

---

## 10. Project Structure

```
vault-chat/
├── cmd/
│   ├── gateway/          # API Gateway entry point
│   ├── auth/             # Auth service entry point
│   ├── chat/             # Chat service entry point
│   └── media/            # Media service entry point
├── internal/
│   ├── gateway/
│   │   ├── handler/      # HTTP handlers
│   │   ├── middleware/   # Auth, rate limit, logging
│   │   ├── ratelimit/    # Redis token bucket
│   │   ├── response/     # JSON responses, gRPC → HTTP error mapping
│   │   └── router/       # Route definitions
│   ├── auth/
│   │   ├── service/      # Business logic
│   │   ├── repository/   # DB operations
│   │   ├── handler/      # gRPC handlers
│   │   └── domain/       # User, Session, Org models
│   ├── chat/
│   │   ├── service/
│   │   ├── repository/
│   │   ├── handler/
│   │   └── domain/       # Message, Room, Membership
│   ├── media/
│   │   ├── service/
│   │   ├── repository/
│   │   ├── handler/
│   │   └── domain/       # File, Upload
│   └── shared/
│       ├── logger/       # slog wrapper
│       ├── config/       # Env/config parsing
│       ├── grpc/         # gRPC client/server helpers
│       ├── jwt/          # JWT utilities
│       ├── requestid/    # Correlation id across HTTP and gRPC
│       ├── validator/    # Input validation
│       └── errors/       # Domain errors
├── api/
│   └── proto/            # .proto files for all services
├── migrations/
│   ├── auth/             # Auth DB migrations
│   ├── chat/             # Chat DB migrations
│   └── media/            # Media DB migrations
├── deployments/
│   ├── docker-compose.yml
│   └── k8s/              # Kubernetes manifests (future)
├── docs/
│   ├── ARCHITECTURE.md
│   └── api/              # OpenAPI specs
├── tests/
│   ├── integration/      # Docker-based integration tests
│   └── fuzz/             # Fuzz test corpus
├── xmake.lua
├── go.mod
├── go.sum
├── README.md
├── CONTRIBUTING.md
├── LICENSE
└── .golangci.yml
```

---

## 11. Data Flow Examples

### 11.1 Send a Message

```
Client → Gateway (WebSocket/REST)
       → Gateway validates JWT (calls Auth via gRPC)
       → Gateway routes to Chat service (gRPC)
       → Chat saves to PostgreSQL
       → Chat publishes to Redis Pub/Sub
       → Gateway WebSocket hubs broadcast to room members
       → (async) Chat emits `message.sent` to NATS
       → Media subscriber scans attachments
       → Notification subscriber sends push/email
```

### 11.2 Upload a File

```
Client → Gateway (multipart upload)
       → Gateway streams to Media service
       → Media validates file type & size
       → Media uploads to MinIO
       → Media saves metadata to PostgreSQL
       → Media emits `file.uploaded` to NATS
       → Chat subscriber updates message with file reference
       → (async) Media runs virus scan (ClamAV)
       → Media emits `file.scanned` → Chat approves/rejects
```

---

## 12. Future Evolution

| Phase | Feature | Architecture Change |
|-------|---------|---------------------|
| v0.1 (MVP) | Auth, DM, channels, file upload | 4 services, Docker Compose |
| v0.2 | Search, threads, reactions | Add Elasticsearch, Chat sharding |
| v0.3 | E2EE DMs, TOTP 2FA | Add Signal Protocol client lib |
| v0.4 | Voice messages, video calls | Add WebRTC service |
| v0.5 | Enterprise SSO (SAML/OIDC) | Extend Auth with identity providers |
| v1.0 | Kubernetes, auto-scaling | Helm charts, HPA, service mesh |
