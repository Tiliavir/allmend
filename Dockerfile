# Front-end libraries (ADR vendor-build) — Docker Hardened Image, dev variant
# (npm). Platform-independent files, so built once on the build platform.
FROM --platform=$BUILDPLATFORM dhi.io/node:24-debian13-dev AS vendor
WORKDIR /src/tools/vendor
COPY tools/vendor/package.json tools/vendor/package-lock.json ./
RUN npm ci --ignore-scripts --no-audit --no-fund
COPY tools/vendor/build.mjs tools/vendor/entry-markmap.js ./
RUN node build.mjs

# Build — Docker Hardened Image. Cross-compiles on the build platform (pure Go,
# CGO off) instead of compiling under emulation for arm64.
FROM --platform=$BUILDPLATFORM dhi.io/golang:1.26-debian13 AS builder
ARG TARGETOS TARGETARCH
WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
COPY . .
COPY --from=vendor /src/ui/static/vendor ui/static/vendor
# -buildvcs=false: no git in the builder image.
RUN CGO_ENABLED=0 GOOS=$TARGETOS GOARCH=$TARGETARCH go build -trimpath -buildvcs=false -o /tmp/out/allmend ./cmd/allmend && mkdir -p /tmp/out/data

# Runtime — distroless, no shell. Time zone data is embedded in the binary.
FROM gcr.io/distroless/static:nonroot
WORKDIR /app
COPY --from=builder /tmp/out/allmend /app/allmend
# Writable for the nonroot user when a fresh named volume is created.
COPY --from=builder --chown=65532:65532 /tmp/out/data /app/data
ENV ALLMEND_DATA_DIR=/app/data
VOLUME /app/data
EXPOSE 8080
HEALTHCHECK --interval=30s --timeout=5s --start-period=15s --retries=3 CMD ["/app/allmend", "health"]
ENTRYPOINT ["/app/allmend"]
