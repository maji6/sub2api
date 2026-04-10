ARG GOLANG_IMAGE=golang:1.26.2-alpine
ARG ALPINE_IMAGE=alpine:3.21

FROM ${GOLANG_IMAGE} AS builder

WORKDIR /app

RUN apk add --no-cache git ca-certificates tzdata

COPY go.mod go.sum ./
RUN go mod download

COPY . .

RUN CGO_ENABLED=0 GOOS=linux go build -trimpath -ldflags="-s -w" -o /app/sub2api-audit ./cmd/sub2api-audit

FROM ${ALPINE_IMAGE}

RUN apk add --no-cache ca-certificates tzdata && rm -rf /var/cache/apk/*

RUN addgroup -g 1000 sub2api-audit && \
    adduser -u 1000 -G sub2api-audit -s /bin/sh -D sub2api-audit

WORKDIR /app

COPY --from=builder --chown=sub2api-audit:sub2api-audit /app/sub2api-audit /app/sub2api-audit

RUN mkdir -p /app/data && chown -R sub2api-audit:sub2api-audit /app/data

USER sub2api-audit

EXPOSE 8087

HEALTHCHECK --interval=30s --timeout=10s --start-period=10s --retries=3 \
    CMD wget -q -T 5 -O /dev/null http://localhost:${SUB2API_AUDIT_SERVER_PORT:-8087}/healthz || exit 1

CMD ["/app/sub2api-audit"]
