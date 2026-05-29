# syntax=docker/dockerfile:1.7

FROM --platform=$BUILDPLATFORM golang:1.26-alpine AS build
RUN apk add --no-cache git
WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
COPY . .
ARG TARGETOS TARGETARCH
RUN CGO_ENABLED=0 GOOS=$TARGETOS GOARCH=$TARGETARCH \
    go build -trimpath -buildvcs=true -ldflags="-s -w" \
    -o /out/awtrix-ai-status ./cmd/awtrix-ai-status
# Pre-create the Pomodoro data dir so the distroless (no-shell) image ships a
# writable, nonroot-owned location for the SQLite stats DB.
RUN mkdir -p /out/data/var/lib/awtrix-ai-status

FROM gcr.io/distroless/static-debian12:nonroot
COPY --from=build /out/awtrix-ai-status /awtrix-ai-status
COPY --from=build --chown=nonroot:nonroot /out/data/var/lib/awtrix-ai-status /var/lib/awtrix-ai-status
VOLUME ["/var/lib/awtrix-ai-status"]
EXPOSE 8080
USER nonroot:nonroot
HEALTHCHECK --interval=30s --timeout=3s --start-period=5s --retries=3 \
  CMD ["/awtrix-ai-status", "healthcheck"]
ENTRYPOINT ["/awtrix-ai-status"]
CMD ["-config", "/etc/awtrix-ai-status/config.json"]
