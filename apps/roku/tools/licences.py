#!/usr/bin/env python3
"""The Roku app's open-source licences, as the other apps list theirs (packages/client-core
src/licences): source/licences.json, read by Settings > This device > Open-source licenses.
What the Roku package holds that others wrote: the Manrope font and the Lucide icons (drawn into
the pictures by tools/make-assets.py); everything else is Portico's own, under the GPL.
Run with --check to fail when the list is older than the files it is made from (tools/test.sh)."""
import hashlib, json, os, sys

APP = os.path.abspath(os.path.join(os.path.dirname(__file__), '..'))
ROOT = os.path.abspath(os.path.join(APP, '..', '..'))
OUT = os.path.join(APP, 'source', 'licences.json')
INPUTS = ['LICENSE', 'apps/roku/fonts/OFL-Manrope.txt', 'packages/design/LICENSE-LUCIDE']
COMPONENTS = [
    # (id, name, licence, group, url, input, copyright taken from the text's head)
    ('portico', 'Portico apps', 'GPL-3.0-or-later', 'portico', 'https://github.com/PorticoMediaServer/portico', 'LICENSE', False),
    ('icons:lucide', 'Lucide icons', 'ISC', 'fonts', 'https://lucide.dev', 'packages/design/LICENSE-LUCIDE', True),
    ('font:manrope', 'Manrope', 'OFL-1.1', 'fonts', 'https://github.com/googlefonts/manrope', 'apps/roku/fonts/OFL-Manrope.txt', True),
]


def read(path):
    with open(os.path.join(ROOT, path), encoding='utf-8') as f:
        return f.read()


def clean(text):
    """Line endings and trailing spaces, and blank lines at either end: nothing else changes."""
    lines = [line.rstrip() for line in text.replace('\r\n', '\n').replace('\r', '\n').lstrip('﻿').split('\n')]
    return '\n'.join(lines).strip('\n')


def split_copyright(text):
    """The Copyright lines in a notice's head (before its first long line), and the text without them;
    a title line ("ISC License") stays with the text."""
    head, kept, body = [], [], False
    for line in clean(text).split('\n'):
        if not body and line.lower().startswith('copyright'):
            head.append(line.strip())
            continue
        if len(line) > 60:
            body = True
        kept.append(line)
    return head, clean('\n'.join(kept)).replace('\n\n\n', '\n\n')


def build():
    texts, components = [], []
    for cid, name, licence, group, url, path, own_copyright in COMPONENTS:
        copyright, text = split_copyright(read(path)) if own_copyright else ([], clean(read(path)))
        # ISC notices keep their heading ("ISC License") with the text; only the first block counts
        text = text.split('\n---\n')[0].strip('\n')
        if text not in texts:
            texts.append(text)
        entry = {'id': cid, 'name': name, 'licence': licence, 'group': group, 'url': url, 'text': texts.index(text)}
        if copyright:
            entry['copyright'] = copyright
        components.append(entry)
    inputs = {path: hashlib.sha256(read(path).encode('utf-8')).hexdigest() for path in INPUTS}
    return {'schema': 1, 'client': 'roku', 'sources': [{'id': 'portico', 'url': 'https://github.com/PorticoMediaServer/portico'}],
            'components': components, 'texts': texts, 'inputs': inputs}


def main():
    data = json.dumps(build(), ensure_ascii=False, indent=1) + '\n'
    if '--check' in sys.argv:
        old = open(OUT, encoding='utf-8').read() if os.path.exists(OUT) else ''
        if old != data:
            sys.exit('source/licences.json is out of date: run python3 tools/licences.py')
        print('licences: up to date')
        return
    with open(OUT, 'w', encoding='utf-8') as f:
        f.write(data)


main()
