FROM golang:1.25-bookworm

# Avoid interactive prompts during apt-get
ENV DEBIAN_FRONTEND=noninteractive

# Install the apt dependencies actually used by the project build and CI.
# NOTE: gcc is required by the Go race detector (-race); fuse3 is needed by
# the FUSE integration tests.
RUN apt-get update && apt-get install -y --no-install-recommends \
    gcc \
    pkg-config \
    make \
    wget \
    curl \
    git \
    fuse3 \
    sudo \
    jq \
    ca-certificates \
    && rm -rf /var/lib/apt/lists/*

# Pre-install the Go tooling used by the CI workflows so jobs don't have to
# compile them from source on every run. To bump a tool, update it here and
# push the Dockerfile — publish-runner.yml will rebuild the image.
RUN go install golang.org/x/tools/cmd/goimports@latest \
    && go install github.com/golangci/golangci-lint/cmd/golangci-lint@latest \
    && go install github.com/securego/gosec/v2/cmd/gosec@latest \
    && go install golang.org/x/vuln/cmd/govulncheck@latest

# Pre-download project Go module dependencies.
# Copying only go.mod and go.sum means this layer is only invalidated when
# dependencies actually change, keeping rebuilds fast.
WORKDIR /onedriver-deps
COPY go.mod go.sum ./
RUN go mod download