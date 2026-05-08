FROM golang:1.26-alpine AS build

WORKDIR /src
COPY go.mod ./
COPY cmd ./cmd

RUN CGO_ENABLED=0 GOOS=linux go build -trimpath -ldflags="-s -w" -o /out/awtrix-ai-status ./cmd/awtrix-ai-status

FROM scratch

COPY --from=build /out/awtrix-ai-status /awtrix-ai-status
COPY config.example.json /config/config.json

EXPOSE 8080
ENTRYPOINT ["/awtrix-ai-status"]
CMD ["-config", "/config/config.json"]
