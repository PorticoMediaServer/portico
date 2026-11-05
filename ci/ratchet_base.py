"""Use the planner's PR/queue comparison range for the live count ratchets."""
from contextlib import contextmanager
import io
import json
import os
from pathlib import Path
import re
import subprocess
import tarfile
import tempfile


def merge_base(root, ref):
    return subprocess.check_output(['git','merge-base','HEAD',ref],cwd=root,text=True).strip()


@contextmanager
def source_snapshot(root, ref):
    """Read immutable tracked sources using the check's installed third-party types."""
    paths=subprocess.check_output(['git','ls-tree','-rz','--name-only',ref],cwd=root).decode().split('\0')
    suffixes={'.ts','.tsx','.mts','.cts','.js','.jsx','.mjs','.cjs','.json','.kt','.java','.swift','.m','.mm','.rs'}
    paths=[p for p in paths if Path(p).suffix in suffixes]
    with tempfile.TemporaryDirectory(prefix='ratchet-source-') as directory:
        snapshot=Path(directory)
        archive=subprocess.check_output(['git','archive',ref,'--',*paths],cwd=root)
        with tarfile.open(fileobj=io.BytesIO(archive)) as bundle:
            # Only regular source files; a tracked symlink cannot escape the snapshot.
            bundle.extractall(snapshot,members=[m for m in bundle.getmembers() if m.isfile()],filter='data')
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
        return base_head(event,env)[0]
    if env.get('GITHUB_EVENT_NAME')=='workflow_dispatch' and env.get('CI_FULL_PLAN')!='true':
        raise ValueError('manual ratchet check requires CI_BASE or a full inventory')
    return ''  # Nightly/full inventory enforces the checked-in upper bounds.


if __name__=='__main__':
    path=os.environ.get('GITHUB_EVENT_PATH')
    print('base='+event_base(json.loads(Path(path).read_text()) if path else {},os.environ))
