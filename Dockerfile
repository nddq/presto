# syntax=docker/dockerfile:1.7

# ---- Build stage ----
FROM golang:1.26-alpine AS build
WORKDIR /src

# Prime the module cache first so repeated builds with no dep changes are fast.
# go.sum is copied alongside go.mod so module downloads are verified against
# checksums instead of silently trusting whatever the proxy returns.
COPY go.mod go.sum* ./
RUN go mod download

# Copy sources and build a static, stripped binary.
COPY . .
RUN --mount=type=cache,target=/root/.cache/go-build \
    CGO_ENABLED=0 GOOS=linux go build \
        -trimpath \
        -ldflags="-s -w" \
        -o /out/presto \
        ./cmd/presto

# ---- Runtime stage ----
FROM gcr.io/distroless/static-debian12:nonroot

COPY --from=build /out/presto /usr/local/bin/presto

EXPOSE 8080

# Default configuration — override via env vars in the deployment.
ENV PRESTO_LISTEN_ADDR=:8080 \
    PRESTO_STORE_PATH=/var/lib/presto/library.prfp \
    PRESTO_MAX_UPLOAD_BYTES=10485760

ENTRYPOINT ["/usr/local/bin/presto"]
CMD ["serve"]
