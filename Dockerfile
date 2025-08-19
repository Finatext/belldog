# syntax=docker/dockerfile:1
FROM golang:1.25 AS build
WORKDIR /src
# pre-copy/cache go.mod for pre-downloading dependencies and only redownloading
# them in subsequent builds if they change.
COPY go.mod go.sum ./
RUN go mod download && go mod verify
COPY . ./
# -ldflags to reduce binary size.
# `-tags lambda.norpc` to reduce binary size: https://docs.aws.amazon.com/lambda/latest/dg/go-image.html#go-image-provided
RUN CGO_ENABLED=0 GOOS=linux go build -v -tags lambda.norpc -ldflags '-w -s' -o /usr/local/bin/app github.com/Finatext/belldog/cmd/lambda

FROM ubuntu:24.04 AS collector
RUN apt-get update && apt-get install -y curl unzip
ARG TARGETARCH
# SHA256 sum is from the GitHub release page.
RUN if [ "$TARGETARCH" = "amd64" ]; then \
      SHA256_SUM=40f4fd1638c167804e92ff8b056166c3cb7ea1c6439d80a926c264ad265826eb; \
      curl --silent --show-error --fail --connect-timeout 3 --max-time 10 --retry 3 --location --output opentelemetry-collector-layer.zip \
            https://github.com/open-telemetry/opentelemetry-lambda/releases/download/layer-collector%2F0.15.0/opentelemetry-collector-layer-amd64.zip; \
      echo "${SHA256_SUM}  opentelemetry-collector-layer.zip" | sha256sum -c -; \
    elif [ "$TARGETARCH" = "arm64" ]; then \
      SHA256_SUM=aa56a60a0df483408a7f2497950fed1a6eb837fed01f49a17221926a05ad7442; \
      curl --silent --show-error --fail --connect-timeout 3 --max-time 10 --retry 3 --location --output opentelemetry-collector-layer.zip \
            https://github.com/open-telemetry/opentelemetry-lambda/releases/download/layer-collector%2F0.15.0/opentelemetry-collector-layer-arm64.zip; \
      echo "${SHA256_SUM}  opentelemetry-collector-layer.zip" | sha256sum -c -; \
    else \
      echo "Unsupported architecture: $TARGETARCH"; \
      exit 1; \
    fi
RUN unzip opentelemetry-collector-layer.zip -d /opt

FROM public.ecr.aws/lambda/provided:al2023
COPY --from=build /usr/local/bin/app .
COPY --from=collector /opt/extensions/collector /opt/extensions/collector
ADD otel-collector.yaml /opt/collector-config/config.yaml
ENTRYPOINT ["./app"]
