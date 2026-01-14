FROM golang:1.24-alpine AS builder

WORKDIR /build

COPY pkg/common ./pkg/common
COPY services/processor-svc/ ./services/processor-svc/

# Create a specific go workspace for this build only
RUN go work init ./pkg/common ./services/processor-svc

WORKDIR /build/services/processor-svc
RUN go mod tidy
RUN CGO_ENABLED=0 go build -ldflags="-s -w" -o /processor-svc .

FROM alpine:3.23
RUN apk add --no-cache ca-certificates
COPY --from=builder /processor-svc /processor-svc
ENTRYPOINT ["/processor-svc"]
