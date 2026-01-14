FROM golang:1.24-alpine AS builder

WORKDIR /build

COPY pkg/common ./pkg/common
COPY services/analyzer-svc/ ./services/analyzer-svc/

# Create a specific go workspace for this build only
RUN go work init ./pkg/common ./services/analyzer-svc

WORKDIR /build/services/analyzer-svc
RUN go mod tidy
RUN CGO_ENABLED=0 go build -ldflags="-s -w" -o /analyzer-svc .

FROM alpine:3.23
RUN apk add --no-cache ca-certificates
COPY --from=builder /analyzer-svc /analyzer-svc
ENTRYPOINT ["/analyzer-svc"]
