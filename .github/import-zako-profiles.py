"""Import inert strategy data from a local Zako checkout at the pinned release.

Usage: python .github/import-zako-profiles.py /path/to/ZakoProxyCLI
No upstream scripts are executed. Existing preset IDs and model lists must match.
"""
import hashlib
import json
from pathlib import Path
import subprocess
import sys

REVISION = '94fb746c4f70f07ef0cf2e26397e2e29dd7b6299'  # v0.14.0
SOURCE = '3249afce6b853ff2f2133e016b223db2cf38b087'
TARGET = Path(__file__).resolve().parents[1] / 'backend/internal/service/zako_profiles'


def main():
    upstream = Path(sys.argv[1]).resolve()

    def read(name):
        return subprocess.check_output(['git', '-C', str(upstream), 'show', f'{REVISION}:{name}'])

    def data(name):
        return json.loads(read(name))

    current = json.loads((TARGET / 'catalog.json').read_text('utf-8'))
    default = next(p for p in current['profiles'] if p['id'] == 'zako-default')
    profiles = [default]
    instructions = {default['text_hash']: current['instructions'][default['text_hash']]}
    adapter = read('profiles/dsh-purge/api-adapter.md').decode('utf-8')
    templates = data('profiles/coldcoffee-full/templates.json')
    texts = {t['id']: read(f"profiles/coldcoffee-full/{t['id']}.md").decode('utf-8') for t in templates}
    outputs = data('profiles/coldcoffee-full/SOURCE.json')['outputs']
    for template_id, text in texts.items():
        expected = outputs[template_id + '.md']
        assert len(text.encode('utf-8')) == expected['bytes']
        assert hashlib.sha256(text.encode('utf-8')).hexdigest() == expected['sha256']

    def store(text):
        digest = hashlib.sha256(text.encode('utf-8')).hexdigest()
        instructions[digest] = text
        return digest

    def profile(profile_id, name, models, template, explicit=False):
        text = texts[template['id']]
        deepseek = len(models) > 0 and (models[0] == 'deepseek' or models[0].startswith('deepseek-'))
        if deepseek:
            text += '\n\n' + adapter
        entry = dict(id=profile_id, name=name, models=models, bytes=len(text.encode('utf-8')),
                     text_hash=store(text), metadata=dict(
                         source_url=f"https://github.com/3641397194-wq/gpt6-Astra/blob/{SOURCE}/{template['source_path']}",
                         source_revision=SOURCE, variant=template['id'], validation='unverified'))
        if explicit:
            entry['template'] = True
            entry['deepseek_text_hash'] = store(text + '\n\n' + adapter)
        profiles.append(entry)

    historical = data('profiles/coldcoffee-full/legacy.json')
    expected = {p['id']: p['models'] for p in current['profiles'] if p['id'] != 'zako-default' and not p.get('template')}
    assert {p['id']: p['models'] for p in historical} == expected, 'Preset IDs/model lists changed'
    for old in historical:
        family = old['models'][0].split('-')[0]
        seat = {'gpt': 'codex', 'glm': 'glm53'}.get(family, family)
        template = next(t for t in templates if t['id'] == seat)
        profile(old['id'], f"{template['name']} · {old['models'][0]}", old['models'], template)
    for template in templates:
        profile('coldcoffee@' + template['id'], template['name'], [], template, explicit=True)
    assert len(profiles) == 58 and len({p['id'] for p in profiles}) == 58
    (TARGET / 'catalog.json').write_text(json.dumps(dict(profiles=profiles, instructions=instructions), ensure_ascii=False, indent=2) + '\n', encoding='utf-8', newline='\n')
    (TARGET / 'coldcoffee-full.SOURCE.json').write_bytes(read('profiles/coldcoffee-full/SOURCE.json'))
    (TARGET / 'licenses/third_party__coldcoffee-full__LICENSE').write_bytes(read('third_party/coldcoffee-full/LICENSE').rstrip(b'\r\n') + b'\n')
    print(f'Imported {len(historical)} presets and {len(templates)} templates at {REVISION}; default preserved.')


if __name__ == '__main__':
    main()
