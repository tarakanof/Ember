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
    -o /out/ember ./cmd/ember
# Pre-create the Pomodoro data dir so the distroless (no-shell) image ships a
# writable, nonroot-owned location for the SQLite stats DB.
RUN mkdir -p /out/data/var/lib/ember

FROM gcr.io/distroless/static-debian12:nonroot
COPY --from=build /out/ember /ember
COPY --from=build --chown=nonroot:nonroot /out/data/var/lib/ember /var/lib/ember
VOLUME ["/var/lib/ember"]
EXPOSE 8080
USER nonroot:nonroot
HEALTHCHECK --interval=30s --timeout=3s --start-period=5s --retries=3 \
  CMD ["/ember", "healthcheck"]
ENTRYPOINT ["/ember"]
CMD ["-config", "/etc/ember/config.json"]
