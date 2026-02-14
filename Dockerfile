FROM golang:1.25-alpine AS builder

WORKDIR /app

COPY go.mod go.sum ./
RUN go mod download

COPY . .

RUN CGO_ENABLED=0 go build -o blizbase .

FROM alpine:3.21

RUN apk add --no-cache ca-certificates

WORKDIR /app

COPY --from=builder /app/blizbase .
COPY pb_public ./pb_public

EXPOSE 8090

CMD ["./blizbase", "serve", "--http=0.0.0.0:8090"]
