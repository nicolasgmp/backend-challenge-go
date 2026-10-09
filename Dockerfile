FROM golang:1.27.1-alpine AS build
WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
COPY cmd ./cmd
COPY internal ./internal
RUN CGO_ENABLED=0 go build -trimpath -o /out/server ./cmd/server \
 && CGO_ENABLED=0 go build -trimpath -o /out/queues ./cmd/queues

FROM alpine:3.22.1
RUN adduser -D -u 10001 wallet
COPY --from=build /out/server /out/queues /app/
USER wallet
EXPOSE 8080
ENTRYPOINT ["/app/server"]
