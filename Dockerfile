# One image recipe for every Go service. Build from the repository root:
#
#   docker build --build-arg SERVICE=auth -t vault-chat-auth .
#
# SERVICE is the directory name under cmd/ (auth, chat, gateway; media later).
# deployments/docker-compose.yml builds all services with this file.
#
# The image contains no configuration or secrets: everything is supplied at
# run time through environment variables and mounted key files.

# Keep in sync with the go directive in go.mod. GOTOOLCHAIN=local below makes
# a mismatch fail the build instead of silently downloading another toolchain.
ARG GO_VERSION=1.23

FROM --platform=$BUILDPLATFORM golang:${GO_VERSION}-alpine AS build
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
FROM gcr.io/distroless/static-debian12:nonroot
COPY --from=build /out/service /usr/local/bin/service
COPY --from=build /out/healthcheck /usr/local/bin/healthcheck
USER nonroot:nonroot
ENTRYPOINT ["/usr/local/bin/service"]
