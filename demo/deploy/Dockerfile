# bot image (docs/internals/architecture.md).
#
# Default build: pure Go, CGO disabled, no native libraries required.
# ORT build (ML guard + local ONNX embeddings): pass -tags ort AND enable cgo,
# because internal/ort then links libonnxruntime.so and libtokenizers.a. That
# variant also needs libonnxruntime.so copied into the runtime stage and
# KIT_ORT_LIB pointing at it.

FROM golang:1.24-bookworm AS builder
WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
COPY . .
ARG BUILD_TAGS=""
ARG CGO=0
WORKDIR /src/demo
RUN CGO_ENABLED=${CGO} go build -tags "${BUILD_TAGS}" -trimpath -o /out/kit-bot ./cmd/bot

FROM debian:bookworm-slim
RUN useradd --system --uid 10001 --create-home kit \
    && mkdir -p /etc/kit
COPY --from=builder /out/kit-bot /usr/local/bin/kit-bot
COPY demo/policy.yaml /etc/kit/policy.yaml
# For the ORT build, also copy the shared library and set KIT_ORT_LIB:
#   COPY --from=builder /src/libonnxruntime.so /usr/lib/libonnxruntime.so
#   ENV KIT_ORT_LIB=/usr/lib/libonnxruntime.so
ENV KIT_POLICY=/etc/kit/policy.yaml
USER kit
EXPOSE 8080
ENTRYPOINT ["/usr/local/bin/kit-bot"]
