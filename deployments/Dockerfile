# syntax=docker/dockerfile:1

FROM golang:alpine AS build
ENV GOTOOLCHAIN=auto
WORKDIR /src

COPY go.mod go.sum ./
RUN go mod download

COPY . .
RUN CGO_ENABLED=0 GOOS=linux go build -trimpath -ldflags="-s -w" -o /out/api ./cmd/api \
 && CGO_ENABLED=0 GOOS=linux go build -trimpath -ldflags="-s -w" -o /out/worker ./cmd/worker \
 && CGO_ENABLED=0 GOOS=linux go build -trimpath -ldflags="-s -w" -o /out/seed ./cmd/seed

FROM gcr.io/distroless/static-debian12:nonroot
COPY --from=build /out/api /bin/api
COPY --from=build /out/worker /bin/worker
COPY --from=build /out/seed /bin/seed

USER nonroot:nonroot
EXPOSE 8080
ENTRYPOINT ["/bin/api"]
