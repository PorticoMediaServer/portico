#!/usr/bin/env python3
"""Drive the production confirmed web owner move on mini-2. No manual claim protocol."""
import argparse
import hashlib
import json
import os
from pathlib import Path
import subprocess


def private(path):
    st = path.lstat()
    if path.is_symlink() or not path.is_file() or st.st_uid != os.getuid() or st.st_mode & 0o777 != 0o600:
        raise SystemExit('Private input must be an operator-owned regular 0600 file')
    return json.loads(path.read_text())


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument('--credentials', type=Path, required=True)
    parser.add_argument('--hosted-credentials', type=Path, required=True)
    parser.add_argument('--report', type=Path, required=True)
    parser.add_argument('--execute', action='store_true')
    args = parser.parse_args()
    if not args.execute:
        print('PLAN: mini-2 browser; normal Local owner sign-in; Settings keep-people move; signed Hosted owner approval; protected same-tab return. No network.')
        return
    if 'CI' in os.environ or 'GITHUB_ACTIONS' in os.environ:
        parser.error('Claim setup is prohibited in CI')
    if args.report.exists():
        parser.error('Use a new report path; preserve previous attempt evidence')
    cfg = private(args.credentials)
    hosted = private(args.hosted_credentials)
    server_id = cfg.get('fixtureStateV3', {}).get('serverId')
    if not isinstance(server_id, str) or not server_id:
        parser.error('Fresh server identity receipt required before browser setup')
    profile = '/home/justin/.local/share/portico-testing/P2-53-owner-web-' + hashlib.sha256(server_id.encode()).hexdigest()[:12]
    payload = json.dumps({'owner': cfg['owner'], 'hosted': {'username': hosted['username'], 'password': hosted['password']}}).encode()
    # The operator first establishes the verified mini-2 localhost49300 tunnel.
    # Only source is copied; credential bytes travel through encrypted stdin.
    target = 'justin@100.113.197.98'
    remote = '/home/justin/astra-scratch/P2-53'
    subprocess.run(['ssh', '-oBatchMode=yes', target, 'mkdir', '-p', remote], check=True)
    subprocess.run(['scp', '-q', str(Path(__file__).with_name('web-owner-move.mjs')), target + ':' + remote + '/web-owner-move.mjs'], check=True)
    command = ('PORTICO_PLAYWRIGHT_MODULE=' + remote + '/.scratch/browser/node_modules/playwright-core/index.mjs '
               'PORTICO_OWNER_PROFILE=' + profile + ' '
               'node ' + remote + '/web-owner-move.mjs --execute')
    try:
        result = subprocess.run(['ssh', '-oBatchMode=yes', target, command], input=payload, stdout=subprocess.PIPE, stderr=subprocess.PIPE, timeout=300)
    except subprocess.TimeoutExpired:
        raise SystemExit('Owner browser deadline exceeded; preserve profile and inspect actual operation before retrying') from None
    # Browser stderr may include proof-bearing navigation. Never print or persist it.
    try:
        receipt = json.loads(result.stdout)
    except (ValueError, UnicodeDecodeError):
        receipt = {'passed': False, 'phase': 'browser-prerequisite-or-startup', 'exit': result.returncode}
    args.report.parent.mkdir(parents=True, exist_ok=True)
    fd = os.open(args.report, os.O_CREAT | os.O_EXCL | os.O_WRONLY, 0o600)
    with os.fdopen(fd, 'w') as out:
        json.dump(receipt, out, indent=2)
    print(json.dumps(receipt))
    if result.returncode or not receipt.get('passed'):
        raise SystemExit(1)


if __name__ == '__main__':
    main()
