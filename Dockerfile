FROM golang:1.25-alpine AS builder

RUN apk add --no-cache git

WORKDIR /app

COPY go.mod go.sum ./
RUN go mod download

COPY . .

ARG COMMIT_HASH=dev
ARG BUILD_DATE=unknown
ARG VERSION=dev

RUN CGO_ENABLED=0 GOOS=linux go build \
  -ldflags="-X 'main.CommitHash=${COMMIT_HASH}' -X 'main.BuildDate=${BUILD_DATE}' -X 'main.Version=${VERSION}'" \
  -o /center ./cmd/center

FROM alpine:latest

RUN apk add --no-cache ca-certificates tzdata

RUN mkdir -p /var/lib/autopeer-center

COPY --from=builder /center /usr/local/bin/center
COPY migrations /migrations

VOLUME ["/var/lib/autopeer-center"]

EXPOSE 8080

CMD ["center"]
