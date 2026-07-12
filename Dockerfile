FROM golang:1.26 AS build
WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
COPY . .
ARG TARGETOS TARGETARCH
RUN CGO_ENABLED=0 GOOS=$TARGETOS GOARCH=$TARGETARCH go build -ldflags='-s -w' -o /mqtt-archive-sink .

FROM scratch
COPY --from=build /mqtt-archive-sink /mqtt-archive-sink
VOLUME /var/lib/mqtt-archive
HEALTHCHECK --interval=60s --timeout=5s CMD ["/mqtt-archive-sink", "health"]
ENTRYPOINT ["/mqtt-archive-sink"]
