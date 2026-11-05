"""Use the planner's PR/queue comparison range for the live count ratchets."""
from contextlib import contextmanager
import io
import json
import os
from pathlib import Path
import re
import subprocess
import tempfile
from contextlib import redirect_stdout, redirect_stderr


def merge_base(root, ref):
    return subprocess.check_output(['git','merge-base','HEAD',ref],cwd=root,text=True).strip()


def historical_json(root, ref, path, optional=False):
    """Only a successful tree lookup can establish an optional file's absence."""
    tree=subprocess.run(['git','ls-tree','-z',ref,'--',path],cwd=root,text=True,capture_output=True)
    if tree.returncode:raise ValueError(f'Cannot inspect historical {path} at {ref}: {tree.stderr.strip()}')
    if not tree.stdout:
        if optional:return None
        raise ValueError(f'Required historical {path} is missing at {ref}')
    blob=subprocess.run(['git','show',ref+':'+path],cwd=root,text=True,capture_output=True)
    if blob.returncode:raise ValueError(f'Cannot read historical {path} at {ref}: {blob.stderr.strip()}')
    try:return json.loads(blob.stdout)
    except ValueError as error:raise ValueError(f'Invalid historical JSON in {path} at {ref}') from error


@contextmanager
def source_snapshot(root, ref):
    """Read immutable tracked sources using the check's installed third-party types."""
    suffixes={'.ts','.tsx','.mts','.cts','.js','.jsx','.mjs','.cjs','.json','.kt','.java','.swift','.m','.mm','.rs'}
    tree=subprocess.check_output(['git','ls-tree','-rz',ref],cwd=root)
    entries=[]
    for record in tree.split(b'\0'):
        if not record:continue
        metadata,name=record.split(b'\t',1);mode,kind,oid=metadata.split()
        path=Path(name.decode())
        if path.suffix not in suffixes:continue
        if path.is_absolute() or '..' in path.parts:raise ValueError('invalid historical source path')
        if kind!=b'blob' or mode not in (b'100644',b'100755'):
            raise ValueError(f'Historical source {path} at {ref} is not a regular blob')
        entries.append((str(path),mode,oid))
    paths=[path for path,_,_ in entries]
    with tempfile.TemporaryDirectory(prefix='ratchet-source-') as directory:
        snapshot=Path(directory)
        # Object IDs, not paths or an archive: attributes (export-ignore/subst),
        # checkout filters and the current worktree must never rewrite history.
        if entries:
            try:
                result=subprocess.run(['git','cat-file','--batch'],cwd=root,
                    input=b''.join(oid+b'\n' for _,_,oid in entries),capture_output=True,check=True)
            except subprocess.CalledProcessError as error:
                raise ValueError(f'Cannot read historical source blobs at {ref}: {error.stderr.decode(errors="replace").strip()}') from error
            blobs=io.BytesIO(result.stdout)
            for path,mode,oid in entries:
                header=blobs.readline().split()
                if len(header)!=3 or header[:2]!=[oid,b'blob'] or not header[2].isdigit():
                    raise ValueError(f'Cannot read historical source blob {path} at {ref}')
                size=int(header[2]);body=blobs.read(size)
                if len(body)!=size or blobs.read(1)!=b'\n':
                    raise ValueError(f'Incomplete historical source blob {path} at {ref}')
                destination=snapshot/path;destination.parent.mkdir(parents=True,exist_ok=True)
                destination.write_bytes(body);destination.chmod(0o755 if mode==b'100755' else 0o644)
            if blobs.read():raise ValueError('Unexpected data after historical source blobs')
        # npm resolves local workspace imports through these links. Recreate
        # only that self-contained topology, pointing at this immutable snapshot.
        package=snapshot/'package.json'
        for pattern in json.loads(package.read_text()).get('workspaces',[]) if package.exists() else []:
            for workspace in snapshot.glob(pattern):
                if not workspace.resolve().is_relative_to(snapshot):raise ValueError('workspace outside source snapshot')
                manifest=workspace/'package.json'
                if not manifest.is_file():continue
                name=json.loads(manifest.read_text()).get('name','')
                if not re.fullmatch(r'(?:@[A-Za-z0-9._-]+/)?[A-Za-z0-9._-]+',name):
                    raise ValueError('invalid snapshot workspace package name')
                link=snapshot/'node_modules'/name;link.parent.mkdir(parents=True,exist_ok=True)
                link.symlink_to(os.path.relpath(workspace,link.parent),target_is_directory=True)
        # Both scans must see the same installed third-party declarations. Without
        # React's types, for example, a local useCallback named request becomes
        # unresolved and the unchanged query scanner conservatively counts it as
        # HTTP. Never link installed workspace aliases back to current sources.
        homes={Path('.')} | {Path(p).parent for p in paths if Path(p).name=='package.json'}
        for home in homes:
            modules=root/home/'node_modules'
            if not modules.is_dir() or not (snapshot/home).is_dir():continue
            for package in modules.iterdir():
                if package.name.startswith('.'):continue
                packages=package.iterdir() if package.name.startswith('@') and package.is_dir() else [package]
                for dependency in packages:
                    target=dependency.resolve()
                    if 'node_modules' not in target.parts:continue
                    link=snapshot/home/'node_modules'/dependency.relative_to(modules)
                    if link.exists() or link.is_symlink():continue
                    link.parent.mkdir(parents=True,exist_ok=True)
                    link.symlink_to(target,target_is_directory=target.is_dir())
        yield snapshot,paths


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
    gitdir=subprocess.check_output(['git','rev-parse','--absolute-git-dir'],cwd=root,text=True).strip()
    for parent,entry in queue_increments(root,base,head):
        with source_snapshot(root,entry) as (snapshot,paths):
            # Read-only Git queries use the original object store. No checkout,
            # index update or historical checker code is executed. The explicit
            # path list belongs to this entry, never the live index.
            (snapshot/'.git').write_text('gitdir: '+gitdir+'\n')
            output=io.StringIO()
            with redirect_stdout(output),redirect_stderr(output):status=check(parent,snapshot,paths)
        if status:errors.append(f'Queued increment {parent[:12]} -> {entry[:12]} failed:\n'+output.getvalue())
    return errors


if __name__=='__main__':
    path=os.environ.get('GITHUB_EVENT_PATH')
    base,predecessor=event_bases(json.loads(Path(path).read_text()) if path else {},os.environ)
    print('base='+base);print('predecessor='+predecessor)
