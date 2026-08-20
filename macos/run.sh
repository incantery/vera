#!/bin/sh
# Build Vera.app and launch it. Vera Core must be running separately:
#   go run ./cmd/vera2            (real mind; needs an API key)
#   go run ./cmd/vera2 --echo     (no key; answers by repeating)
set -e
cd "$(dirname "$0")"
xcodebuild -project Vera.xcodeproj -target Vera -configuration Debug build -quiet
pkill -x Vera 2>/dev/null || true
open build/Debug/Vera.app
