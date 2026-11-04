"""Immutable #837 versus #837+#825, native full-order shard 3; never merge."""
import hashlib
import json
import os
from pathlib import Path
import subprocess

root = Path.cwd()
out = Path(os.environ['RUNNER_TEMP']) / 'mac837-evidence'
out.mkdir()
source = subprocess.check_output(['git', 'rev-parse', 'HEAD'], text=True).strip()
(out / 'source-sha.txt').write_text(source + '\n')
(out / 'source-archive-sha256.txt').write_text(hashlib.sha256(subprocess.check_output(['git', 'archive', source])).hexdigest() + '\n')
replace = {}


def instrument(relative, edit):
    original = root / relative
    target = out / original.name
    target.write_text(edit(original.read_text()))
    replace[str(original)] = str(target)


def stream(text):
    # Diagnostic only: expose the actual continuation witness and owners of
    # global slot debt. Do not change admission, deadlines or returned errors.
    text = text.replace('"io"', '"io"\n"os"', 1)
    old = 'if s == nil || s.client != c || s.key != mapKey {'
    witness = '''
        if s != nil { fmt.Fprintf(os.Stderr, "MAC837_BUDGET old=%d new=%d sameClient=%t root=%q\\n", s.request.Limit, r.Limit, s.client == c, r.Root) }
'''
    if old in text:
        text = text.replace(old, old + witness, 1)
    old = '''case inventoryStreams.slots <- struct{}{}:
			default:
'''
    assert old in text
    text = text.replace(old, old + '''
                for _, held := range inventoryStreams.byID { fmt.Fprintf(os.Stderr, "MAC837_SLOT root=%q initial=%d\\n", held.request.Root, held.request.Limit) }
''', 1)
    return text


def coverage(text):
    text = text.replace('"context"', '"context"\n"log"', 1)
    old = 'func (s *Service) excludeDirectory(ctx context.Context, job, relative, reason string) error {'
    assert old in text
    return text.replace(old, old + '\nlog.Printf("MAC837_EXCLUDE path=%q reason=%q", relative, reason)', 1)


instrument('server/internal/storage/inventory_page_stream.go', stream)
instrument('server/internal/ingestion/coverage.go', coverage)
overlay = out / 'overlay.json'
overlay.write_text(json.dumps({'Replace': replace}))
tags = subprocess.check_output(['python3', 'scripts/release-build-tags.py', '--capability'], text=True).strip()
helper = Path(os.environ['RUNNER_TEMP']) / 'portico-server'
subprocess.run(['go', 'build', '-C', 'server', '-overlay', str(overlay), '-tags', tags, '-o', str(helper), './cmd/server'], check=True)
os.environ['PORTICO_PLAYBACK_TEST_HELPER'] = str(helper)
packages = subprocess.check_output(['go', 'list', '-tags', tags, './...'], cwd=root / 'server', text=True).splitlines()
selected = [p for i, p in enumerate(packages, 1) if i % 8 == 3]
(out / 'packages.txt').write_text('\n'.join(selected) + '\n')
with (out / 'mac-3.jsonl').open('w') as output, (out / 'mac-3.stderr').open('w') as stderr:
    result = subprocess.run(['go', 'test', '-C', 'server', '-overlay', str(overlay), '-tags', tags, '-json', '-timeout', '60m', *selected], stdout=output, stderr=stderr)
(out / 'exit.txt').write_text(str(result.returncode) + '\n')
raise SystemExit(result.returncode)
