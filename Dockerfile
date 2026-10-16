FROM golang:1.26-alpine AS build

WORKDIR /app

COPY go.mod go.sum ./
RUN go mod download

COPY . .
RUN CGO_ENABLED=0 GOOS=linux go build -ldflags="-s -w" -o bin/hasgier ./cmd/hasgier

FROM alpine:latest

RUN apk add --no-cache ca-certificates tzdata

WORKDIR /app

COPY --from=build /app/bin/hasgier .
COPY --from=build /app/internal/db/migrations ./internal/db/migrations

EXPOSE 8080

CMD ["./hasgier"]
