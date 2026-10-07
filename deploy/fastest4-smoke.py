import hashlib
import pathlib
import re
import subprocess

release = pathlib.Path('/opt/sub2api/releases/sub2api-v0.2.13-fastest.4-20261006')
dist = release / 'source/backend/internal/web/dist'
index_asset = re.search(r'src="(/assets/index-[^"]+\.js)"', (dist / 'index.html').read_text()).group(1)
accounts_asset, = (dist / 'assets').glob('AccountsView-*.js')
assert b'scheduling_preferred' in accounts_asset.read_bytes()


def fetch(url, expected_status=200):
    response = subprocess.run(
        ['curl', '--silent', '--show-error', '--max-time', '30', '--write-out', '\n%{http_code}', url],
        stdout=subprocess.PIPE, check=True
    )
    body, status = response.stdout.rsplit(b'\n', 1)
    assert int(status) == expected_status, (url, status)
    print(f'{expected_status} {url}')
    return body


for origin in ('http://127.0.0.1:8080', 'https://yqdtokenfree.ccwu.cc'):
    fetch(origin + '/health')
    for route in ('/login', '/admin/accounts'):
        assert index_asset.encode() in fetch(origin + route)
    fetch(origin + '/v1/models', 401)
    fetch(origin + '/api/v1/admin/accounts', 401)
    for asset in (dist / index_asset.lstrip('/'), accounts_asset):
        remote = fetch(origin + '/assets/' + asset.name)
        digest = hashlib.sha256(remote).hexdigest()
        assert digest == hashlib.sha256(asset.read_bytes()).hexdigest(), asset.name
        print(f'SHA256 matched {asset.name}: {digest}')
print('Smoke checks passed; no authenticated gateway request was sent.')
