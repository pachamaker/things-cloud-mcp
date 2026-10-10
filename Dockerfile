FROM golang:1.25-alpine AS builder

RUN apk add --no-cache gcc musl-dev

WORKDIR /app
COPY go.mod go.sum ./
RUN go mod download

COPY . .
RUN CGO_ENABLED=1 go build -o things-server ./server/

FROM alpine:latest
RUN apk add --no-cache ca-certificates
COPY --from=builder /app/things-server /usr/local/bin/things-server

RUN mkdir -p /data

EXPOSE 8080
CMD ["things-server"]
