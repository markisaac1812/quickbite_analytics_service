# ── stage 1: build ────────────────────────────────────────────────────────
# Go compiles to a single native binary, so (unlike the Node services) the
# runtime image needs no language runtime and no dependencies — just the binary.
FROM golang:1.26-alpine AS builder

WORKDIR /src

# Download modules first so this layer is cached until go.mod/go.sum change
# (the Go equivalent of copying package*.json before `npm ci`).
COPY go.mod go.sum ./
RUN go mod download

COPY . .

# CGO_ENABLED=0 -> fully static binary (no libc needed at runtime).
# -trimpath + -ldflags="-s -w" -> no local paths / debug symbols, smaller binary.
RUN CGO_ENABLED=0 GOOS=linux go build -trimpath -ldflags="-s -w" \
    -o /out/analytics-service ./cmd/api

# ── stage 2: run ──────────────────────────────────────────────────────────
# distroless/static: ~2MB, CA certs + tzdata only, no shell or package manager.
# The :nonroot tag runs as uid 65532 (the equivalent of `USER node`).
FROM gcr.io/distroless/static-debian12:nonroot

COPY --from=builder /out/analytics-service /analytics-service

EXPOSE 4002

USER nonroot:nonroot

ENTRYPOINT ["/analytics-service"]
