#!/usr/bin/env bash
set -euo pipefail
release=/opt/sub2api/releases/sub2api-v0.2.13-fastest.4-20261006
exec > >(tee "$release/build.log") 2>&1
export PATH=/usr/local/go/bin:$PATH
export GOPROXY=https://proxy.golang.org,direct
export GOSUMDB=sum.golang.org
export GOMAXPROCS=2
export GOMEMLIMIT=900MiB
cd "$release/source/backend"
go version
python3 "$release/migration-preflight.py"
nice -n 10 go test -p 2 ./internal/service ./internal/repository ./internal/handler/admin ./internal/handler/dto ./internal/server/middleware -run 'TestFastestFailover|TestGetGroupModelAccountQuality|TestGroupEntityToService_PreservesMessagesDispatchModelConfig|TestAPIKeyRepository_GetByKeyForAuth_PreservesMessagesDispatchModelConfig_SQLite|TestOpenAIGatewayService_SelectAccountWithScheduler' -count=1
nice -n 10 go test -p 2 -tags unit ./internal/service ./internal/repository -run 'TestFastestFailover|TestGetGroupModelAccountQuality|TestGroupEntityToService_PreservesMessagesDispatchModelConfig|TestAPIKeyRepository_GetByKeyForAuth_PreservesMessagesDispatchModelConfig_SQLite|TestGatewayService_selectAccountWithMixedScheduling' -count=1
python3 "$release/source/deploy/fastest4-quality-check.py" "$release/source/backend/internal/repository/usage_log_repo.go"
CGO_ENABLED=0 GOOS=linux GOARCH=amd64 nice -n 10 go build -p 2 -tags embed -ldflags="-s -w -X main.Version=0.2.13-fastest.4 -X main.Commit=3040209f-custom4 -X main.Date=$(date -u +%Y-%m-%dT%H:%M:%SZ) -X main.BuildType=release" -trimpath -o "$release/sub2api" ./cmd/server
sha256sum "$release/sub2api" > "$release/sub2api.sha256"
cd "$release"
docker build --network=none -t sub2api:0.2.13-fastest.4 -f Dockerfile.runtime .
docker image inspect sub2api:0.2.13-fastest.4 --format '{{.Id}}' > image.id
touch BUILD_SUCCESS
