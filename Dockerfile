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

# Amazon DocumentDB only accepts TLS connections signed by the AWS RDS CA.
# Fetched here (the runtime image has no shell to chmod with): ADD from a URL
# is owner-only, so make it world-readable for the nonroot runtime user.
# Production MONGO_URI points at it: ...&tls=true&tlsCAFile=global-bundle.pem
ADD https://truststore.pki.rds.amazonaws.com/global/global-bundle.pem /out/global-bundle.pem
RUN chmod 0644 /out/global-bundle.pem

# ── stage 2: run ──────────────────────────────────────────────────────────
# distroless/static: ~2MB, CA certs + tzdata only, no shell or package manager.
# The :nonroot tag runs as uid 65532 (the equivalent of `USER node`).
FROM gcr.io/distroless/static-debian12:nonroot

ENV APP_ENV=production

# WORKDIR so the relative tlsCAFile=global-bundle.pem in MONGO_URI resolves.
WORKDIR /app

COPY --from=builder /out/analytics-service /app/analytics-service
COPY --from=builder /out/global-bundle.pem /app/global-bundle.pem

EXPOSE 4002

USER nonroot:nonroot

ENTRYPOINT ["/app/analytics-service"]
