FROM golang:1.25.8-alpine AS builder

WORKDIR /app

COPY go.mod go.sum ./
RUN go mod download

COPY . .
RUN CGO_ENABLED=0 GOOS=linux go build -ldflags="-s -w" -o loadgen ./cmd/loadgen


FROM alpine:3.19

RUN apk add --no-cache ca-certificates tzdata

COPY --from=builder /app/loadgen /usr/local/bin/loadgen
COPY --from=builder /app/scenarios /scenarios

EXPOSE 9090

ENTRYPOINT ["loadgen"]
CMD ["--help"]
