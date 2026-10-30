"""Bound changed source payload size and catch private key material without printing it."""
import json
import hashlib
from pathlib import Path
import re
from plan import ROOT, command

def check(paths):
    if not paths: paths=[p for p in command(['git','ls-files','-z']).split('\0') if p]
    import subprocess
    subprocess.run(['sh','scripts/check-repo-binaries.sh'],cwd=ROOT,check=True)
    exemptions=json.loads((ROOT/'ci/source-guard-exceptions.json').read_text())['files']
    bad=[]
    for name in paths:
        file=ROOT/name
        if not file.is_file(): continue
        if re.search(rb'-----BEGIN (?:RSA |EC |OPENSSH |DSA )?PRIVATE KEY-----',file.read_bytes()):
            if exemptions.get(name,{}).get('sha256')!=hashlib.sha256(file.read_bytes()).hexdigest(): bad.append((name,'private key material'))
    if bad: raise ValueError(str(bad))
if __name__=='__main__': check([])
