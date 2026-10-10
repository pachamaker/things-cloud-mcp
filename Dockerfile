FROM golang:1.26.9-alpine AS builder

RUN apk add --no-cache gcc musl-dev

WORKDIR /app
COPY go.mod go.sum ./
RUN go mod download

COPY . .
ARG VERSION=dev
RUN CGO_ENABLED=1 go build -ldflags "-X main.version=${VERSION}" -o things-server ./server/

FROM alpine:latest
RUN apk add --no-cache ca-certificates
COPY --from=builder /app/things-server /usr/local/bin/things-server

RUN mkdir -p /data

EXPOSE 8080
CMD ["things-server"]
