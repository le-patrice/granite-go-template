# syntax=docker/dockerfile:1

FROM golang:1.26-alpine AS build
ENV GOTOOLCHAIN=local
WORKDIR /src

COPY go.mod go.sum ./
RUN go mod download

COPY . .
RUN CGO_ENABLED=0 GOOS=linux go build -trimpath -ldflags="-s -w" -o /out/api ./cmd/api \
 && CGO_ENABLED=0 GOOS=linux go build -trimpath -ldflags="-s -w" -o /out/worker ./cmd/worker \
 && CGO_ENABLED=0 GOOS=linux go build -trimpath -ldflags="-s -w" -o /out/seed ./cmd/seed

# Migration image: `docker build --target migrate`. Runs goose against /migrations.
FROM golang:1.26-alpine AS goose
ENV GOTOOLCHAIN=local
ARG GOOSE_VERSION=v3.29.0
RUN CGO_ENABLED=0 go install -tags='no_clickhouse no_libsql no_mssql no_mysql no_sqlite3 no_vertica no_ydb' -trimpath -ldflags="-s -w" github.com/pressly/goose/v3/cmd/goose@${GOOSE_VERSION}

FROM alpine:3.24 AS migrate
RUN adduser -D -u 65532 nonroot
COPY --from=goose /go/bin/goose /bin/goose
COPY db/migrations /migrations
USER nonroot:nonroot
ENTRYPOINT ["/bin/goose"]

# Runtime image: the default (last) target.
FROM gcr.io/distroless/static-debian12:nonroot
COPY --from=build /out/api /bin/api
COPY --from=build /out/worker /bin/worker
COPY --from=build /out/seed /bin/seed

USER nonroot:nonroot
EXPOSE 8080
ENTRYPOINT ["/bin/api"]
