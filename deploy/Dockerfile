# Server image: static Go binary with the embedded web UI.
FROM golang:1.27-alpine AS build
WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
COPY . .
ARG VERSION=0.1.0
RUN CGO_ENABLED=0 go build -ldflags "-s -w -X main.version=${VERSION}" -o /qdnder ./cmd/qdnder

FROM alpine:3.21
RUN apk add --no-cache ca-certificates tzdata
COPY --from=build /qdnder /usr/local/bin/qdnder
ENV QDNDER_ADDR=0.0.0.0:8090 QDNDER_DATA=/data
VOLUME /data
EXPOSE 8090
ENTRYPOINT ["qdnder", "server"]
