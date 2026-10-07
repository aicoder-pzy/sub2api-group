#!/usr/bin/env bash
set -euo pipefail
umask 077
release=/opt/sub2api/releases/sub2api-v0.2.13-fastest.4-20261006
cd /opt/sub2api
test -f "$release/BUILD_SUCCESS"
test ! -e "$release/DEPLOY_SUCCESS"
test ! -e "$release/docker-compose.before.yml"
exec > >(tee "$release/deploy.log") 2>&1
test "$(docker inspect --format '{{.Config.Image}}' sub2api)" = sub2api:0.2.13-fastest.3
test "$(docker image inspect --format '{{.Id}}' sub2api:0.2.13-fastest.4)" = "$(cat "$release/image.id")"
sha256sum -c "$release/sub2api.sha256"
docker inspect --format '{{.Id}}' sub2api-postgres sub2api-redis > "$release/dependencies.before"
cp -p docker-compose.yml "$release/docker-compose.before.yml"
cp -p .env "$release/env.before"
docker exec sub2api-postgres pg_dump -U sub2api -d sub2api -Fc > "$release/postgres.before.dump"
docker exec -i sub2api-postgres pg_restore --list < "$release/postgres.before.dump" > "$release/postgres.before.list"
tar -czf "$release/app-data.before.tar.gz" -C /opt/sub2api data
(cd "$release" && sha256sum docker-compose.before.yml env.before postgres.before.dump app-data.before.tar.gz > backups.sha256)
rollback() {
    status=$?
    trap - ERR
    echo "Deployment failed; restoring application image 0.2.13-fastest.3"
    cp -p "$release/docker-compose.before.yml" docker-compose.yml
    docker compose up -d --no-deps --pull never --wait --wait-timeout 120 sub2api || true
    exit "$status"
}
trap rollback ERR
python3 - <<'PY'
from pathlib import Path
compose = Path('docker-compose.yml')
source = compose.read_text()
old = 'sub2api:0.2.13-fastest.3'
assert source.count(old) == 1, 'Unexpected Compose image configuration'
compose.write_text(source.replace(old, 'sub2api:0.2.13-fastest.4'))
PY
docker compose config --quiet
docker compose up -d --no-deps --pull never --wait --wait-timeout 120 sub2api
curl --fail --silent --show-error --max-time 15 http://127.0.0.1:8080/health
docker inspect --format '{{.Id}}' sub2api-postgres sub2api-redis > "$release/dependencies.after"
cmp "$release/dependencies.before" "$release/dependencies.after"
for container in sub2api sub2api-postgres sub2api-redis; do
    test "$(docker inspect --format '{{.State.Health.Status}}' "$container")" = healthy
done
test "$(docker inspect --format '{{.Image}}' sub2api)" = "$(cat "$release/image.id")"
docker exec sub2api /app/sub2api -version 2>&1 | tee "$release/version.log"
grep -F '0.2.13-fastest.4' "$release/version.log"
trap - ERR
date --iso-8601=seconds > "$release/DEPLOY_SUCCESS"
docker compose ps
