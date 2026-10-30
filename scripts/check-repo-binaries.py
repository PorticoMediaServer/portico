#!/usr/bin/env python3
"""Reject built binaries and unapproved large tracked files in one pass."""
import argparse,hashlib,json,subprocess,sys
from pathlib import Path
LIMIT=1048576
GENERATED={'packages/contracts/src/schemas.ts','server/receiver/cast/contracts.js','server/api/portico.openapi.json','server/internal/persistence/migrations/0001_baseline.sql','server/internal/mediaexec/fonts/NotoSansCJKjp-Regular.otf'}
def error(root,name,exceptions):
 p=root/name
 if not p.is_file():return None
 with p.open('rb') as f:magic=f.read(4)
 if magic in [bytes.fromhex(v) for v in ['7f454c46','cffaedfe','cefaedfe','feedfacf','feedface','cafebabe']] or magic.startswith(b'MZ'):return 'built executable in Git: '+name
 limit=2097152 if name=='packages/contracts/src/types.ts' else LIMIT
 size=p.stat().st_size
 if size<=limit or name.startswith(('fixtures/','third_party/')) or name in GENERATED:return None
 entry=exceptions.get(name,{})
 if entry.get('bytes')==size and entry.get('sha256')==hashlib.sha256(p.read_bytes()).hexdigest():return None
 return f'file exceeds Git size limit: {name} ({size} bytes; limit {limit})'
def main():
 ap=argparse.ArgumentParser();ap.add_argument('--staged',action='store_true');a=ap.parse_args();root=Path(__file__).resolve().parents[1]
 command=['git','diff','--cached','--name-only','--diff-filter=AM','-z'] if a.staged else ['git','ls-files','-z']
 names=subprocess.check_output(command,cwd=root).decode().split('\0');policy=root/'ci/large-file-exceptions.json';exceptions=json.loads(policy.read_text())['files'] if policy.exists() else {}
 errors=[message for name in names if name for message in [error(root,name,exceptions)] if message]
 for message in errors:print(message,file=sys.stderr)
 if errors:return 1
 print(f'Binary/size policy passed for {sum(bool(n) for n in names)} tracked paths.')
 return 0
if __name__=='__main__':raise SystemExit(main())
