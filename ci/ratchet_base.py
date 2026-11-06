"""Use the planner's PR/queue comparison range for the live count ratchets."""
from contextlib import contextmanager
import io
import json
import os
from pathlib import Path
import re
import subprocess
from ratchet_runtime import RatchetRuntime, active_runtime
from contextlib import redirect_stdout, redirect_stderr


def merge_base(root, ref):
    return subprocess.check_output(['git','merge-base','HEAD',ref],cwd=root,text=True).strip()


def historical_json(root, ref, path, optional=False):
    runtime=active_runtime(root)
    if runtime:return runtime.git.json(ref,path,optional=optional)
    with RatchetRuntime(root) as runtime:return runtime.git.json(ref,path,optional=optional)


@contextmanager
def source_snapshot(root, ref):
    """Exact blobs, invocation-scoped caches; never archives, filters or links."""
    runtime=active_runtime(root)
    if runtime:
        view=runtime.historical(ref);yield view.root,view.paths
    else:
        with RatchetRuntime(root) as runtime:
            view=runtime.historical(ref);yield view.root,view.paths


def event_base(event, env):
    # Reuse exactly the planner's queue logic. merge_group.base_sha can be the
    # previous queued group's head, so compare all carried changes with main.
    from plan import base_head
    if env.get('GITHUB_EVENT_NAME') in {'pull_request','merge_group'} or env.get('CI_BASE'):
        base,head=base_head(event,env)
        if env.get('GITHUB_EVENT_NAME') not in {'pull_request','merge_group'} and base==head:
            raise ValueError('live ratchet comparison requires an earlier base, not the checked-out head')
        return base
    if env.get('GITHUB_EVENT_NAME')=='push':raise ValueError('push ratchet check requires an explicit CI_BASE')
    if env.get('GITHUB_EVENT_NAME')=='workflow_dispatch' and env.get('CI_FULL_PLAN')!='true':
        raise ValueError('manual ratchet check requires CI_BASE or a full inventory')
    return ''  # Nightly/full inventory enforces the checked-in upper bounds.


def event_bases(event, env):
    aggregate=event_base(event,env)  # Validates checkout, group SHAs and ancestry.
    predecessor=event['merge_group']['base_sha'] if env.get('GITHUB_EVENT_NAME')=='merge_group' else ''
    return aggregate,predecessor if predecessor!=aggregate else ''


def queue_increments(root, base, head):
    """Every first-parent queue entry, not a net comparison with its final tree."""
    actual=subprocess.check_output(['git','rev-parse','HEAD'],cwd=root,text=True).strip()
    if not re.fullmatch(r'[a-f0-9]{40}',head) or actual!=head:
        raise ValueError('queue history head does not match checkout')
    cursor=merge_base(root,base)
    history=subprocess.check_output(['git','log','--first-parent','--reverse','--format=%H %P',cursor+'..'+head],cwd=root,text=True)
    increments=[]
    for line in history.splitlines():
        entry,*parents=line.split()
        if not parents or parents[0]!=cursor:
            raise ValueError('cannot establish every first-parent queue boundary')
        increments.append((cursor,entry));cursor=entry
    if cursor!=head:raise ValueError('incomplete queue history')
    return increments


def check_queue(root, base, head, check):
    errors=[]
    for parent,entry in queue_increments(root,base,head):
        with source_snapshot(root,entry) as (snapshot,paths):
            output=io.StringIO()
            with redirect_stdout(output),redirect_stderr(output):status=check(parent,snapshot,paths)
        if status:errors.append(f'Queued increment {parent[:12]} -> {entry[:12]} failed:\n'+output.getvalue())
    return errors


if __name__=='__main__':
    path=os.environ.get('GITHUB_EVENT_PATH')
    base,predecessor=event_bases(json.loads(Path(path).read_text()) if path else {},os.environ)
    print('base='+base);print('predecessor='+predecessor)
