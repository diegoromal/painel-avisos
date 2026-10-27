#!/bin/sh
# Gera os binários em dist/ (Windows e Linux, amd64). Requer Go 1.26+.
set -e
cd "$(dirname "$0")"
VER=$(grep -m1 'var version' main.go | cut -d'"' -f2)
mkdir -p dist
CGO_ENABLED=0 GOOS=windows GOARCH=amd64 go build -trimpath -ldflags "-s -w" -o dist/painel-avisos.exe .
CGO_ENABLED=0 GOOS=linux   GOARCH=amd64 go build -trimpath -ldflags "-s -w" -o dist/painel-avisos-linux-amd64 .
( cd dist && sha256sum painel-avisos.exe painel-avisos-linux-amd64 > SHA256SUMS.txt )
echo "versão $VER"; ls -la dist
