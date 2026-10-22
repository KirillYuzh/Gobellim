FROM golang:1.26-bookworm AS builder

WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
COPY . .
RUN CGO_ENABLED=0 go build -trimpath -o /out/app ./cmd/lite-demo \
    && CGO_ENABLED=0 go build -trimpath -o /out/mockd ./cmd/mockd

FROM debian:bookworm-slim

RUN apt-get update \
    && apt-get install -y --no-install-recommends ca-certificates curl \
    && rm -rf /var/lib/apt/lists/* \
    && useradd --system --uid 10001 --create-home app

COPY --from=builder /out/app /usr/local/bin/app
COPY --from=builder /out/mockd /usr/local/bin/mockd
COPY policy.json /policy.json

USER app
EXPOSE 8080
ENTRYPOINT ["/usr/local/bin/app"]
