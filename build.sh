#!/usr/bin/env bash
set -euo pipefail

echo "📦 Downloading CLI dependencies..."
go mod tidy

echo "🔨 Building swiftdeploy binary..."
go build -ldflags="-s -w" -o swiftdeploy .

echo "✅ swiftdeploy binary ready."
echo "   Run: ./swiftdeploy --help"
