FROM golang:1.26-alpine AS builder

WORKDIR /build

COPY go.mod go.sum* ./
RUN go mod download

COPY . .
RUN CGO_ENABLED=0 go build -ldflags="-s -w" -o klever-node-hub ./cmd/dashboard

FROM alpine:3.20

RUN apk add --no-cache ca-certificates

WORKDIR /app
COPY --from=builder /build/klever-node-hub .

EXPOSE 9443

ENTRYPOINT ["/app/klever-node-hub"]
