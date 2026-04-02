FROM golang:1.25-alpine AS builder
WORKDIR /app
COPY go.mod go.sum ./
RUN go mod download
COPY . .
RUN CGO_ENABLED=0 GOOS=linux go build -ldflags "-s -w" -o /rushborg-agent ./cmd/rushborg-agent

FROM alpine:3.19
RUN apk --no-cache add ca-certificates docker-cli docker-cli-compose
COPY --from=builder /rushborg-agent /usr/local/bin/rushborg-agent
ENTRYPOINT ["/usr/local/bin/rushborg-agent"]
