#!/usr/bin/env python3
"""Move only engine Actions payload caches after a verified OCI round-trip."""
import argparse
import json
from pathlib import Path
import subprocess

import importlib.util
spec = importlib.util.spec_from_file_location('cache_oci', Path(__file__).with_name('engine-cache-oci.py'))
OCI = importlib.util.module_from_spec(spec); spec.loader.exec_module(OCI)


def select(target, rows):
    kind = 'sources' if target in ('windows-x64', 'linux-arm64') else 'stages'
    prefix = ('engine-downloads-v2-' if kind == 'sources' else 'engine-dependencies-v2-') + target + '-'
    owned = [row for row in rows if row['key'].startswith(prefix)]
    main = [row for row in owned if row['ref'] == 'refs/heads/main']
    return dict(target=target, kind=kind, prefix=prefix,
                selected=max(main, key=lambda row: row['created_at']) if main else None, obsolete=owned)


def cleanup(selection, receipt, remove):
    if (receipt.get('saved') is not True or receipt.get('roundTripVerified') is not True or
            receipt.get('target') != selection['target'] or receipt.get('kind') != selection['kind'] or
            not receipt.get('reference', '').startswith('ghcr.io/porticomediaserver/ffmpeg-payload-cache-' + selection['target'] + '@sha256:')):
        raise ValueError('verified target-specific OCI replacement required before deleting Actions caches')
    for row in selection['obsolete']:
        if (not row['key'].startswith(selection['prefix']) or type(row['id']) is not int or row['id'] <= 0):
            raise ValueError('cache cleanup escaped selected engine namespace')
    deleted = []
    for row in selection['obsolete']:
        remove(row['id']); deleted.append(row['id'])
    return deleted


if __name__ == '__main__':
    p = argparse.ArgumentParser(description=__doc__); p.add_argument('action', choices=('plan', 'cleanup', 'budget'))
    p.add_argument('--target', choices=OCI.TARGETS); p.add_argument('--work', type=Path, required=True)
    p.add_argument('--output', type=Path); p.add_argument('--transport', type=Path)
    a = p.parse_args(); OCI.guard(write=True); a.work.mkdir(parents=True, exist_ok=True)
    selection_file = a.work / 'cache-selection.json'
    if a.action in ('plan', 'budget'):
        pages = json.loads(subprocess.check_output(['gh', 'api', '--paginate', '--slurp',
            'repos/PorticoMediaServer/portico/actions/caches?per_page=100']))
        rows = [row for page in pages for row in page['actions_caches']]
        if a.action == 'budget':
            engine = [r for r in rows if r['key'].startswith(('engine-dependencies-', 'engine-downloads-', 'engine-prefix-proof-'))]
            report = dict(engineActionsBytes=sum(r['size_in_bytes'] for r in engine),
                          repositoryActionsBytes=sum(r['size_in_bytes'] for r in rows), engineCacheIds=[r['id'] for r in engine])
            (a.work / 'cache-budget.json').write_text(json.dumps(report, indent=2) + '\n')
            if report['engineActionsBytes'] >= 1024**3: raise ValueError('engine Actions caches must stay below 1 GiB')
            print(json.dumps(report)); raise SystemExit(0)
        if a.target is None: p.error('migration target required')
        value = select(a.target, rows)
        selection_file.write_text(json.dumps(value, indent=2) + '\n')
        if a.output:
            with a.output.open('a') as stream:
                stream.write('available=' + str(value['selected'] is not None).lower() + '\nkind=' + value['kind'] + '\n')
                stream.write('key=' + (value['selected']['key'] if value['selected'] else 'no-retained-engine-cache') + '\n')
        print(json.dumps(value))
    else:
        value = json.loads(selection_file.read_text())
        def remove(identifier):
            subprocess.run(['gh', 'api', '--method', 'DELETE', f'repos/PorticoMediaServer/portico/actions/caches/{identifier}'], check=True)
        deleted = cleanup(value, json.loads(a.transport.read_text()), remove)
        (a.work / 'cache-cleanup.json').write_text(json.dumps(dict(target=a.target, deletedActionsCacheIds=deleted), indent=2) + '\n')
