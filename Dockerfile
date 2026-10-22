# One image recipe for every Go service. Build from the repository root:
#
#   docker build --build-arg SERVICE=auth -t vault-chat-auth .
#
# SERVICE is the directory name under cmd/ (auth, chat, gateway, media).
# deployments/docker-compose.yml builds all services with this file.
#
# The image contains no configuration or secrets: everything is supplied at
# run time through environment variables and mounted key files.

# Keep GO_VERSION equal to the toolchain line in go.mod. GOTOOLCHAIN=local
# below makes a Go older than the go directive fail the build instead of
# silently downloading another toolchain. Both base images are pinned so a
# rebuild produces the same image: the builder by exact Go and Alpine
# release, the runtime by digest (update the digest deliberately, e.g. from
# `docker buildx imagetools inspect gcr.io/distroless/static-debian12:nonroot`).
ARG GO_VERSION=1.27.3
ARG ALPINE_VERSION=3.24

FROM --platform=$BUILDPLATFORM golang:${GO_VERSION}-alpine${ALPINE_VERSION} AS build
ENV CGO_ENABLED=0 GOTOOLCHAIN=local
WORKDIR /src

COPY go.mod go.sum ./
RUN --mount=type=cache,target=/go/pkg/mod go mod download

COPY . .
ARG SERVICE
ARG TARGETOS
ARG TARGETARCH
RUN --mount=type=cache,target=/go/pkg/mod \
    --mount=type=cache,target=/root/.cache/go-build \
    test -n "$SERVICE" || { echo "build arg SERVICE is required" >&2; exit 1; }; \
    test -d "./cmd/$SERVICE" || { echo "unknown service: $SERVICE" >&2; exit 1; }; \
    GOOS=$TARGETOS GOARCH=$TARGETARCH go build -trimpath -ldflags="-s -w" -o /out/service "./cmd/$SERVICE" && \
    GOOS=$TARGETOS GOARCH=$TARGETARCH go build -trimpath -ldflags="-s -w" -o /out/healthcheck ./cmd/healthcheck

# Static, shell-less runtime that runs as uid/gid 65532 by default.
FROM gcr.io/distroless/static-debian12:nonroot@sha256:afa5c872c891853ca7fcf1f12c3edb23f7eeef36189728842dd51042ff57f7ab
COPY --from=build /out/service /usr/local/bin/service
COPY --from=build /out/healthcheck /usr/local/bin/healthcheck
USER nonroot:nonroot
ENTRYPOINT ["/usr/local/bin/service"]
