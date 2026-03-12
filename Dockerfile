FROM golang:1.26-alpine AS builder

ARG VERSION=dev
ARG GIT_COMMIT=unknown
ARG BUILD_TIME=unknown

WORKDIR /build

COPY go.mod go.sum* ./
RUN go mod download

COPY . .
RUN CGO_ENABLED=0 go build -ldflags="-s -w \
    -X github.com/CTJaeger/KleverNodeHub/internal/version.Version=${VERSION} \
    -X github.com/CTJaeger/KleverNodeHub/internal/version.GitCommit=${GIT_COMMIT} \
    -X github.com/CTJaeger/KleverNodeHub/internal/version.BuildTime=${BUILD_TIME}" \
    -o klever-node-hub ./cmd/dashboard

FROM alpine:3.20

RUN apk add --no-cache ca-certificates

WORKDIR /app
COPY --from=builder /build/klever-node-hub .

EXPOSE 9443

ENTRYPOINT ["/app/klever-node-hub"]
