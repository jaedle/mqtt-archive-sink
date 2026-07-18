FROM golang:1.26 AS build
WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
COPY . .
ARG TARGETOS TARGETARCH
ARG VERSION=dev
RUN CGO_ENABLED=0 GOOS=$TARGETOS GOARCH=$TARGETARCH go build -ldflags="-s -w -X main.version=${VERSION}" -o /mqtt-archive-sink ./cmd/mqtt-archive-sink
RUN CGO_ENABLED=0 GOOS=$TARGETOS GOARCH=$TARGETARCH go build -ldflags="-s -w -X main.version=${VERSION}" -o /mqtt-archive-mcp ./cmd/mqtt-archive-mcp

FROM scratch AS mcp
COPY --from=build /etc/ssl/certs/ca-certificates.crt /etc/ssl/certs/
COPY --from=build /mqtt-archive-mcp /mqtt-archive-mcp
VOLUME /var/lib/mqtt-archive
HEALTHCHECK --interval=60s --timeout=5s CMD ["/mqtt-archive-mcp", "health"]
ENTRYPOINT ["/mqtt-archive-mcp"]

# The sink stays the last stage so an untargeted build keeps yielding it.
FROM scratch AS sink
COPY --from=build /etc/ssl/certs/ca-certificates.crt /etc/ssl/certs/
COPY --from=build /mqtt-archive-sink /mqtt-archive-sink
VOLUME /var/lib/mqtt-archive
HEALTHCHECK --interval=60s --timeout=5s CMD ["/mqtt-archive-sink", "health"]
ENTRYPOINT ["/mqtt-archive-sink"]
