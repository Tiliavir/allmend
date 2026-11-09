# Build — Docker Hardened Image.
FROM dhi.io/golang:1.26-debian13 AS builder
WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
COPY . .
# -buildvcs=false: no git in the builder image.
RUN CGO_ENABLED=0 GOOS=linux go build -trimpath -buildvcs=false -o /tmp/out/allmend ./cmd/allmend && mkdir -p /tmp/out/data

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
