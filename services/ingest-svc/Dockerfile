FROM golang:1.24-alpine AS builder

WORKDIR /build

COPY pkg/common ./pkg/common
COPY services/ingest-svc/ ./services/ingest-svc/

# Create a specific go workspace for this build only
RUN go work init ./pkg/common ./services/ingest-svc 

WORKDIR /build/services/ingest-svc
RUN go mod tidy
RUN CGO_ENABLED=0 go build -ldflags="-s -w" -o /ingest-svc .

FROM alpine:3.23
RUN apk add --no-cache ca-certificates
COPY --from=builder /ingest-svc /ingest-svc
ENTRYPOINT ["/ingest-svc"]