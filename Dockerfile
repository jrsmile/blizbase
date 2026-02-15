FROM golang:1.25-alpine AS builder
LABEL org.opencontainers.image.authors="JRSmile" \
      org.opencontainers.image.description="Blizbase - A WoW Guild Roster Aggregator" \
      org.opencontainers.image.licenses="MIT" \
      org.opencontainers.image.source="https://github.com/jrsmile/blizbase"

WORKDIR /app

COPY go.mod go.sum ./
RUN go mod download

COPY . .

RUN CGO_ENABLED=0 go build -o blizbase .

FROM alpine:3.21

RUN apk add --no-cache ca-certificates

WORKDIR /app
COPY --from=library/docker:latest /usr/local/bin/docker /usr/bin/docker
COPY --from=docker/compose:alpine-1.29.2 /usr/local/bin/docker-compose /usr/bin/docker-compose
COPY --from=builder /app/blizbase .
COPY pb_public ./pb_public

EXPOSE 8090

CMD ["./blizbase", "serve", "--http=0.0.0.0:8090"]
