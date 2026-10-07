#!/usr/bin/env bash
set -euo pipefail
release=/opt/sub2api/releases/sub2api-v0.2.13-fastest.6-20261007
exec > >(tee "$release/build.log") 2>&1
export PATH=/usr/local/go/bin:$PATH
export GOMAXPROCS=2 GOMEMLIMIT=900MiB CGO_ENABLED=0
cd "$release/source/backend"
go version
python3 "$release/migration-preflight.py"
nice -n 10 go test -trimpath -p 1 ./internal/repository ./internal/service -run 'TestSchedulerCachePreferred|TestSchedulerMetadataAccount|TestFilterSchedulerCredentials|TestFastestFailover' -count=1
nice -n 10 go test -trimpath -p 1 ./internal/repository -run '^TestSchedulerCachePreferred' -count=10
GOOS=linux GOARCH=amd64 nice -n 10 go build -p 2 -tags embed -ldflags="-s -w -X main.Version=0.2.13-fastest.6 -X main.Commit=3040209f-custom6 -X main.Date=$(date -u +%Y-%m-%dT%H:%M:%SZ) -X main.BuildType=release" -trimpath -o "$release/sub2api" ./cmd/server
sha256sum "$release/sub2api" > "$release/sub2api.sha256"
cd "$release"
docker build --network=none -t sub2api:0.2.13-fastest.6 -f Dockerfile.runtime .
docker image inspect sub2api:0.2.13-fastest.6 --format '{{.Id}}' > image.id
touch BUILD_SUCCESS
