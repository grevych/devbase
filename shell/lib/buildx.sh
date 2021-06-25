#!/usr/bin/env bash
BUILDX_BINARY_URL="https://github.com/docker/buildx/releases/download/v0.5.1/buildx-v0.5.1.linux-amd64"

echo "👉 Installing Buildx"
curl --output docker-buildx \
  --silent --show-error --location --fail --retry 3 \
  "$BUILDX_BINARY_URL"

mkdir -p ~/.docker/cli-plugins
mv docker-buildx ~/.docker/cli-plugins/
chmod a+x ~/.docker/cli-plugins/docker-buildx

# Taken from setup-qemu Github Action
echo "💎 Installing QEMU static binaries..."
docker run --rm --privileged tonistiigi/binfmt:latest --install all

echo "🛒 Extracting available platforms..."
docker run --rm --privileged tonistiigi/binfmt:latest

# Take from setup-buildx Github Action
echo "🔨 Creating a new builder instance"
docker buildx create --use

echo "🏃 Booting builder"
docker buildx inspect --bootstrap
