# Private room attachments

Media owns a PostgreSQL metadata database and a private S3 bucket. Auth verifies
access tokens; Chat authorizes the current room member. Neither client-supplied
actor IDs nor knowledge of a file UUID grant access.

## HTTP API

1. Upload one multipart field named `file` to
   `POST /api/v1/rooms/{room_id}/files` with a bearer token. A complete upload
   returns `201` and `file_id`, `room_id`, `owner_id`, `filename`, `content_type`,
   `size`, `created_at`. Do not retry an ambiguous upload automatically: upload
   itself has no idempotency key.
2. Send `attachment_ids: ["<file_id>"]` alongside `client_message_id` and optional
   `text` to the existing message endpoint. Up to five distinct files are allowed;
   each must belong to this room and have been uploaded by the sender. Empty text
   is allowed with attachments; whitespace-only text is rejected.
3. Read metadata at `GET /api/v1/files/{file_id}` and bytes at
   `GET /api/v1/files/{file_id}/content`. Every new request checks current
   membership. Removing a member blocks subsequent downloads; an already started
   download is not revoked midway.

For example, after obtaining a token and room ID:

```sh
curl --fail -H "Authorization: Bearer $TOKEN" \
  -F 'file=@notes.txt' "http://localhost:8080/api/v1/rooms/$ROOM_ID/files"
```

Files are immutable. Message retries compare exact text and the ordered list of
attachment IDs; changing either returns `409`. References survive history,
catch-up and `message.created` WebSocket events. Text-only JSON responses omit
`attachment_ids` for compatibility. Media downtime blocks new attachment sends,
while ordinary text messages remain available.

## Bounds and storage lifecycle

The maximum file size is 10 MiB. Metadata names are valid UTF-8, at most 255 bytes,
without path separators or control characters; object keys are server-generated
UUIDs. The allowlist uses content bytes: PNG, JPEG, PDF and UTF-8 plain text.
Images require a valid header and at most 40 million pixels. Active HTML/SVG roots
are rejected. Signature/header validation is not antivirus scanning or full file
sanitization; clients must not render untrusted files inline.

Downloads set `Content-Disposition: attachment`, `X-Content-Type-Options: nosniff`
and `Cache-Control: private, no-store`. No public or presigned object URL is
returned. A bucket policy makes Media startup fail; the dedicated development
bucket is created private. For another S3 provider, configure private ACLs and
public-access blocking at the provider as well.

Media buffers at most 10 MiB of content per upload, permits 16 concurrent uploads,
uses 64 KiB data frames and a 30-second RPC budget. Gateway permits eight uploads
per instance and applies its usual authentication, IP/user rate limits, CORS and
request budget (10 seconds by default). Only the upload route has the larger body
allowance; ordinary JSON keeps its existing limit. Oversized files return `413`;
invalid/unsupported files and extra multipart parts return `400`.

Before writing a blob, Media inserts a pending row. It publishes the row only
after S3 succeeds and Chat confirms membership again. Pending rows are invisible.
Known failures clean up both stores; uncertain publication outcomes keep the blob
so a committed row cannot point at deleted content. A worker runs every five
minutes, reclaiming at most 100 pending uploads older than an hour. Completed
files are retained, including files not yet attached to a message. Quotas,
retention, deletion and malware scanning remain future work.

## Development stack

Compose starts four applications, three independent PostgreSQL instances, Redis
and S3. Chat dials Media lazily, so startup dependencies do not form a cycle:
Chat starts before Media; Gateway waits for Media. Local `go run` addresses and
credentials are listed in `.env.example`. To run Media against Compose's database,
run it inside Compose; its PostgreSQL port is not published.

`Dockerfile.minio` builds official source commit
[`7aac2a2c5b7c`](https://github.com/minio/minio/tree/7aac2a2c5b7c) via a pinned
Go module version and includes its AGPL license. This is a development fixture:
[community MinIO is source-only and no longer maintained](https://github.com/minio/minio#readme).
A maintained S3 provider is required before production. Internal gRPC and the
local S3 endpoint use plaintext; mTLS and production secret management are not
part of this block.

## Verification

```sh
go test -race ./...
VAULT_CHAT_TEST_MEDIA_DATABASE_URL='<dedicated postgres URL>' \
VAULT_CHAT_TEST_MEDIA_S3_ENDPOINT=127.0.0.1:9000 \
  go test -race ./internal/media/storage
VAULT_CHAT_COMPOSE_GATEWAY_URL=http://127.0.0.1:8080 \
  go test -tags compose -count=1 ./tests/compose
```

The S3 integration fixture uses `minioadmin` development credentials. The Compose
scenario covers upload, attachment-only send, retries, WebSocket, history,
download, outsider denial, foreign room/uploader rejection, removal of a member,
unsupported content and files above the limit.
