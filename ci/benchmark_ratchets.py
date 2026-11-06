#!/usr/bin/env python3
"""Measure actual queue history with current checkers and the existing install.

Run through portico-heavy. --verify additionally compares every counter result
with a new, uncached compiler/runtime per tree, outside the measured gate time.
No historical checkout, archive, dependency install or node_modules link is used.
"""
import argparse
import hashlib
import json
from pathlib import Path
import subprocess
import tempfile
import time

import client_api_census as census
import engineering_rules as engineering
from ratchet_base import queue_increments
from ratchet_runtime import RatchetRuntime, TOOL_ROOT


def digest(value):
    return hashlib.sha256(json.dumps(value,sort_keys=True,separators=(',',':')).encode()).hexdigest()


def benchmark(base, head, predecessor, report, verify=False, expected=None):
    def git(*args):return subprocess.check_output(['git',*args],cwd=TOOL_ROOT,text=True).strip()
    base=git('rev-parse',base+'^{commit}');head=git('rev-parse',head+'^{commit}')
    predecessor=git('rev-parse',(predecessor or head+'^')+'^{commit}')
    data={'base':base,'head':head,'predecessor':predecessor,'checkerCommit':git('rev-parse','HEAD'),
          'dependencyRoot':str(TOOL_ROOT),'verifyRequested':verify}
    with tempfile.TemporaryDirectory(prefix='ratchet-benchmark-') as directory:
        root=Path(directory)
        subprocess.run(['git','init','-q',directory],check=True)
        (root/'.git/objects/info/alternates').write_text(git('rev-parse','--path-format=absolute','--git-common-dir')+'/objects\n')
        subprocess.run(['git','update-ref','refs/heads/ratchet',head],cwd=root,check=True)
        subprocess.run(['git','symbolic-ref','HEAD','refs/heads/ratchet'],cwd=root,check=True)
        increments=queue_increments(root,base,head);data['increments']=increments
        if expected is not None and len(increments)!=expected:raise ValueError(f'Expected {expected} actual entries, found {len(increments)}')
        refs=[base,*[entry for _,entry in increments]];cached={}
        with RatchetRuntime(root,dependency_root=TOOL_ROOT) as runtime:
            view=runtime.historical(head);args=['--base',base,'--predecessor',predecessor,'--queue-head',head]
            began=time.monotonic()
            data['censusStatus']=census.main(args,root=view.root)
            data['censusSeconds']=time.monotonic()-began
            data['engineeringStatus']=engineering.main(args,root=view.root)
            data['gateSeconds']=time.monotonic()-began
            data['metrics']=runtime.metrics()
            if data['censusStatus'] or data['engineeringStatus']:raise ValueError('Measured queue gate failed; no successful timing claimed')
            for ref in refs:
                view=runtime.historical(ref)
                cached[ref]={'census':digest(runtime.census(view)),
                             'engineering':digest(engineering.source_counts(view.root,view.paths))}
            data['cachedTreeDigests']=cached
        if verify:
            data['uncachedTrees']={}
            for ref in refs:
                began=time.monotonic()
                with RatchetRuntime(root,dependency_root=TOOL_ROOT) as runtime:
                    view=runtime.historical(ref)
                    cold={'census':digest(runtime.census(view)),
                          'engineering':digest(engineering.source_counts(view.root,view.paths))}
                    if cold!=cached[ref]:raise ValueError(f'Cached/uncached counter mismatch at {ref}')
                    data['uncachedTrees'][ref]={'digests':cold,'seconds':time.monotonic()-began}
                print('Uncached parity '+ref,flush=True)
    report=Path(report);report.parent.mkdir(parents=True,exist_ok=True)
    with tempfile.NamedTemporaryFile('w',dir=report.parent,delete=False) as output:
        temporary=Path(output.name);json.dump(data,output,indent=2);output.write('\n')
    temporary.replace(report)
    print(json.dumps({'entries':len(increments),'gateSeconds':data['gateSeconds'],'statuses':[data['censusStatus'],data['engineeringStatus']],
                      'verifiedTrees':len(data.get('uncachedTrees',{})),'report':str(report)}))
    return data


if __name__=='__main__':
    parser=argparse.ArgumentParser(description=__doc__)
    for argument in ['base','head','report']:parser.add_argument('--'+argument,required=True)
    parser.add_argument('--predecessor');parser.add_argument('--expect-entries',type=int);parser.add_argument('--verify',action='store_true')
    args=parser.parse_args()
    benchmark(args.base,args.head,args.predecessor,args.report,args.verify,args.expect_entries)
