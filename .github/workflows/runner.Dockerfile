FROM golang:1.25-bookworm

# Avoid interactive prompts during apt-get
ENV DEBIAN_FRONTEND=noninteractive

# Install all apt dependencies (including heavy ones like webkit and libreoffice)
RUN apt-get update && apt-get install -y --no-install-recommends \
    gcc \
    pkg-config \
    libwebkit2gtk-4.1-dev \
    libjson-glib-dev \
    make \
    wget \
    curl \
    rpm \
    libreoffice \
    git \
    fuse3 \
    sudo \
    jq \
    ca-certificates \
    && rm -rf /var/lib/apt/lists/*

# Pre-install Go test and coverage utilities
RUN go install golang.org/x/tools/cmd/goimports@latest \
    && go install github.com/rakyll/gotest@latest \
    && go install github.com/wadey/gocovmerge@latest \
    && go install github.com/golangci/golangci-lint/cmd/golangci-lint@latest \
    && go install github.com/securego/gosec/v2/cmd/gosec@latest \
    && go install golang.org/x/vuln/cmd/govulncheck@latest

# Pre-download project Go module dependencies.
# Copying only go.mod and go.sum means this layer is only invalidated when
# dependencies actually change, keeping rebuilds fast.
WORKDIR /onedriver-deps
COPY go.mod go.sum ./
RUN go mod download