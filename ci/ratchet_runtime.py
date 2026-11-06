"""Invocation-scoped exact Git sources, typed worker reuse and a hard budget."""
import copy
from contextvars import ContextVar
import hashlib
import io
import json
import os
from pathlib import Path
import select
import signal
import stat
import subprocess
import tempfile
import threading
import time

TOOL_ROOT=Path(__file__).resolve().parent.parent
SUFFIXES={'.ts','.tsx','.mts','.cts','.js','.jsx','.mjs','.cjs','.json','.kt','.java','.swift','.m','.mm','.rs'}
TYPED={'.ts','.tsx','.mts','.cts','.js','.jsx','.mjs','.cjs','.json'}
_ACTIVE=ContextVar('portico_ratchet_runtime',default=None)


def active_runtime(root):
    runtime=_ACTIVE.get();path=str(Path(root).resolve())
    return runtime if runtime and (path==str(runtime.original) or path in runtime.by_root) else None


def source_text(data, errors='strict'):
    # Match Python Path.read_text's universal-newline behavior. The JS counters
    # still receive original bytes decoded as UTF-8, like Node readFileSync.
    return data.decode('utf8',errors=errors).replace('\r\n','\n').replace('\r','\n')


class RatchetFailure(ValueError):
    pass


class Budget:
    def __init__(self, seconds=None):
        self.seconds=float(os.environ.get('PORTICO_RATCHET_BUDGET_SECONDS','180') if seconds is None else seconds)
        if not 0<self.seconds<=360:raise RatchetFailure('Invalid internal ratchet time budget (must be >0 and <=360 seconds)')
        self.start=time.monotonic();self.deadline=self.start+self.seconds

    def remaining(self):
        value=self.deadline-time.monotonic()
        if value<=0:raise RatchetFailure(f'Ratchet internal time budget exceeded ({self.seconds:g}s); no result accepted')
        return value

    def run(self, command, **kwargs):
        try:return subprocess.run(command,timeout=self.remaining(),check=True,**kwargs)
        except subprocess.TimeoutExpired as error:
            raise RatchetFailure(f'Ratchet internal time budget exceeded ({self.seconds:g}s) during {command[0]}') from error
        except subprocess.CalledProcessError as error:
            detail=error.stderr.decode(errors='replace') if isinstance(error.stderr,bytes) else error.stderr or ''
            raise RatchetFailure(f'Ratchet command failed ({command[0]}): {detail.strip()[:3000]}') from error

    def arm(self):
        self.previous=None
        if threading.current_thread() is threading.main_thread():
            self.previous=(signal.getsignal(signal.SIGALRM),signal.getitimer(signal.ITIMER_REAL),time.monotonic())
            def expired(*_):raise RatchetFailure(f'Ratchet internal time budget exceeded ({self.seconds:g}s); no result accepted')
            signal.signal(signal.SIGALRM,expired)
            prior=self.previous[1][0]
            signal.setitimer(signal.ITIMER_REAL,min(self.remaining(),prior) if prior>0 else self.remaining())

    def disarm(self):
        if self.previous:
            handler,(remaining,interval),started=self.previous
            signal.setitimer(signal.ITIMER_REAL,0);signal.signal(signal.SIGALRM,handler)
            if remaining>0:signal.setitimer(signal.ITIMER_REAL,max(.000001,remaining-(time.monotonic()-started)),interval)


class GitBlobs:
    def __init__(self, root, budget):
        self.root=Path(root);self.budget=budget;self.trees={};self.blobs={}
        self.stats={'treeReads':0,'blobReads':0,'blobHits':0}
        self.algorithm=self.git(['rev-parse','--show-object-format']).decode().strip()
        if self.algorithm not in {'sha1','sha256'}:raise RatchetFailure('Unknown Git object format')

    def git(self, args):
        return self.budget.run(['git',*args],cwd=self.root,capture_output=True).stdout

    def tree(self, ref):
        tree=self.git(['rev-parse','--verify',ref+'^{tree}']).decode().strip()
        if tree not in self.trees:
            entries={}
            for record in self.git(['ls-tree','-rz',tree]).split(b'\0'):
                if not record:continue
                metadata,name=record.split(b'\t',1);mode,kind,oid=metadata.split()
                path=name.decode()
                if Path(path).is_absolute() or '..' in Path(path).parts:raise RatchetFailure('Invalid historical source path')
                entries[path]=(mode.decode(),kind.decode(),oid.decode())
            self.trees[tree]=entries;self.stats['treeReads']+=1
        return tree,self.trees[tree]

    def load(self, oids):
        wanted=list(dict.fromkeys(oids));missing=[oid for oid in wanted if oid not in self.blobs]
        self.stats['blobHits']+=len(wanted)-len(missing)
        if missing:
            answer=self.budget.run(['git','cat-file','--batch'],cwd=self.root,
                input=''.join(oid+'\n' for oid in missing).encode(),capture_output=True).stdout
            stream=io.BytesIO(answer)
            for oid in missing:
                header=stream.readline().split()
                if len(header)!=3 or header[:2]!=[oid.encode(),b'blob'] or not header[2].isdigit():
                    raise RatchetFailure(f'Cannot read exact historical blob {oid}')
                size=int(header[2]);data=stream.read(size)
                if len(data)!=size or stream.read(1)!=b'\n':raise RatchetFailure('Truncated historical source blob')
                self.blobs[oid]=data;self.stats['blobReads']+=1
            if stream.read():raise RatchetFailure('Unexpected historical blob output')
        return {oid:self.blobs[oid] for oid in wanted}

    def json(self, ref, path, optional=False):
        _,entries=self.tree(ref)
        if path not in entries:
            if optional:return None
            raise RatchetFailure(f'Required historical {path} missing at {ref}')
        mode,kind,oid=entries[path]
        if kind!='blob' or mode not in {'100644','100755'}:raise RatchetFailure(f'Historical {path} is not a regular blob')
        data=self.load([oid])[oid]
        try:return json.loads(data)
        except (json.JSONDecodeError,UnicodeError) as error:raise RatchetFailure(f'Invalid historical JSON {path} at {ref}') from error

    def oid(self, data):
        return hashlib.new(self.algorithm,b'blob '+str(len(data)).encode()+b'\0'+data).hexdigest()


class SourceView:
    def __init__(self, identity, paths, records, root):
        self.identity=identity;self.paths=paths;self.records=records;self.root=Path(root)

    def typed_sources(self):
        return {path:{'oid':oid,'text':data.decode('utf8',errors='replace')}
                for path,(_,oid,data) in self.records.items() if Path(path).suffix in TYPED}


class TypedWorker:
    def __init__(self, root, kind, budget, scratch, dependency_root=None):
        self.root=Path(root);self.dependency_root=Path(dependency_root or root);self.kind=kind;self.budget=budget
        self.error=(Path(scratch)/(kind+'-stderr.log')).open('w+')
        self.process=subprocess.Popen(['node',str(TOOL_ROOT/'ci/typed_ratchet.mjs')],cwd=TOOL_ROOT,
            stdin=subprocess.PIPE,stdout=subprocess.PIPE,stderr=self.error,text=True,start_new_session=True)
        os.set_blocking(self.process.stdin.fileno(),False)
        self.stats={}

    def scan(self, view, paths, configs=None, project_files=None):
        payload={'root':str(self.root),'dependencyRoot':str(self.dependency_root),'kind':self.kind,'files':paths,'projectFiles':project_files or paths,'sources':view.typed_sources(),'configs':configs or []}
        try:
            request=(json.dumps(payload,separators=(',',':'))+'\n').encode();sent=0
            while sent<len(request):
                _,ready,_=select.select([],[self.process.stdin],[],self.budget.remaining())
                if not ready:raise RatchetFailure('Ratchet internal time budget exceeded sending typed sources')
                try:sent+=os.write(self.process.stdin.fileno(),request[sent:sent+65536])
                except BlockingIOError:continue
            response=bytearray()
            while b'\n' not in response:
                ready,_,_=select.select([self.process.stdout],[],[],self.budget.remaining())
                if not ready:raise RatchetFailure('Ratchet internal time budget exceeded waiting for typed worker; no result accepted')
                chunk=os.read(self.process.stdout.fileno(),65536)
                if not chunk:
                    self.error.flush();self.error.seek(0)
                    raise RatchetFailure('Typed ratchet worker exited without a result: '+self.error.read(4000))
                response.extend(chunk)
                if len(response)>67108864:raise RatchetFailure('Typed ratchet result exceeds the internal response bound')
            if response.split(b'\n',1)[1]:raise RatchetFailure('Unexpected extra typed worker output')
            answer=json.loads(response)
            if answer.get('ok') is not True:raise RatchetFailure('Typed ratchet worker failed: '+str(answer.get('error','unknown'))[:6000])
            self.stats=answer['stats'];return answer
        except (OSError,ValueError) as error:
            self.close()
            if isinstance(error,RatchetFailure):raise
            raise RatchetFailure(f'Typed ratchet transport failed: {error}') from error

    def close(self):
        process=self.process
        if process.poll() is None:
            try:process.stdin.close();process.wait(timeout=min(2,max(.05,self.budget.deadline-time.monotonic())))
            except (OSError,subprocess.TimeoutExpired):
                try:os.killpg(process.pid,signal.SIGKILL)
                except ProcessLookupError:pass
                process.wait()
        for stream in [process.stdout,process.stdin,self.error]:
            if stream and not stream.closed:stream.close()


class RatchetRuntime:
    def __init__(self, root, seconds=None, dependency_root=None):
        self.original=Path(root).resolve();self.budget=Budget(seconds)
        self.dependency_root=Path(dependency_root or self.original).resolve()
        self.git=GitBlobs(self.original,self.budget);self.views={};self.by_root={};self.native={};self.workers={};self.results={};self.rule_counts={}
        self.initial_head=self.git.git(['rev-parse','--verify','HEAD^{commit}']).decode().strip()
        self.object_store=Path(self.git.git(['rev-parse','--path-format=absolute','--git-common-dir']).decode().strip())/'objects'
        self.scratch=tempfile.TemporaryDirectory(prefix='portico-ratchet-',dir=os.environ.get('TMPDIR'))
        self.stats={'nativeAnalysed':0,'nativeReused':0,'materializedFiles':0}

    @staticmethod
    def bound_files(exceptions):
        return {path for entry in exceptions.values()
            for hashes in [*entry.get('sourceHashes',{}).values(),*(pin.get('sourceHashes',{}) for pin in entry.get('additionalSites',{}).values())]
            for path in hashes}

    def historical(self, ref):
        commit=self.git.git(['rev-parse','--verify',ref+'^{commit}']).decode().strip()
        tree,entries=self.git.tree(ref)
        if commit in self.views:return self.views[commit]
        exceptions=self.git.json(ref,'ci/client-api-exceptions.json',optional=True) or {}
        bound=self.bound_files(exceptions)
        wanted={p:record for p,record in entries.items() if Path(p).suffix in SUFFIXES or p in bound}
        if not bound<=entries.keys():raise RatchetFailure('Missing bound historical source file')
        for path,(mode,kind,_) in wanted.items():
            if mode not in {'100644','100755'} or kind!='blob':raise RatchetFailure(f'Historical source {path} is not a regular blob; symlinks are forbidden')
        data=self.git.load(record[2] for record in wanted.values())
        records={p:(mode,oid,data[oid]) for p,(mode,_,oid) in wanted.items()}
        view=self.materialize('git-'+commit,list(records),records,commit);self.views[commit]=view;return view

    def current(self, root=None, paths=None):
        root=Path(root or self.original).resolve()
        if str(root) in self.by_root:return self.by_root[str(root)]
        if self.git.git(['rev-parse','--verify','HEAD^{commit}']).decode().strip()!=self.initial_head:
            raise RatchetFailure('Git HEAD changed during the ratchet invocation')
        paths=paths if paths is not None else self.git.budget.run(['git','ls-files','-z'],cwd=root,capture_output=True).stdout.decode().split('\0')
        paths=[p for p in paths if p]
        # Local checks historically accept a newly written ledger/evidence
        # before `git add`. Counted product sources still come only from the
        # index; immutable historical views never see these working files.
        for path in ['ci/client-api-approvals.json','ci/client-api-detection-additions.json']:
            if (root/path).exists() or (root/path).is_symlink():paths.append(path)
        approval_file=root/'ci/client-api-approvals.json'
        if approval_file.is_file():
            approvals=json.loads(approval_file.read_bytes())
            paths.extend(e['spanEvidence'] for e in approvals.values() if isinstance(e,dict) and isinstance(e.get('spanEvidence'),str))
        paths=list(dict.fromkeys(paths))
        try:exceptions=json.loads((root/'ci/client-api-exceptions.json').read_bytes())
        except FileNotFoundError:exceptions={}
        bound=self.bound_files(exceptions);records={}
        for path in paths:
            if Path(path).suffix not in SUFFIXES and path not in bound:continue
            relative=Path(path)
            if relative.is_absolute() or '..' in relative.parts:raise RatchetFailure('Invalid current source path')
            descriptors=[]
            try:
                flags=os.O_RDONLY|os.O_DIRECTORY|os.O_NOFOLLOW
                descriptors.append(os.open(root,flags))
                for part in relative.parts[:-1]:descriptors.append(os.open(part,flags,dir_fd=descriptors[-1]))
                descriptors.append(os.open(relative.parts[-1],os.O_RDONLY|os.O_NOFOLLOW|os.O_NONBLOCK,dir_fd=descriptors[-1]))
                info=os.fstat(descriptors[-1])
                if not stat.S_ISREG(info.st_mode):raise RatchetFailure(f'Current source is not a regular file: {path}')
                chunks=[]
                while chunk:=os.read(descriptors[-1],131072):chunks.append(chunk)
                data=b''.join(chunks)
            except OSError as error:raise RatchetFailure(f'Cannot read regular current source without symlinks: {path}: {error}') from error
            finally:
                for descriptor in reversed(descriptors):os.close(descriptor)
            records[path]=('100755' if info.st_mode&0o111 else '100644',self.git.oid(data),data)
        if not bound<=records.keys():raise RatchetFailure('Missing bound current source file')
        identity='working-'+hashlib.sha256(json.dumps([(p,v[0],v[1]) for p,v in sorted(records.items())]).encode()).hexdigest()
        if identity not in self.views:self.views[identity]=self.materialize(identity,paths,records,self.initial_head)
        return self.views[identity]

    def materialize(self, identity, paths, records, head):
        self.budget.remaining()
        destination=Path(self.scratch.name)/identity;destination.mkdir()
        for path,(mode,_,data) in records.items():
            self.budget.remaining()
            at=destination/path;at.parent.mkdir(parents=True,exist_ok=True);at.write_bytes(data)
            at.chmod(0o755 if mode=='100755' else 0o644);self.stats['materializedFiles']+=1
        # Isolated read-only metadata gives each historical entry its own HEAD
        # ancestry. Pointing every view at the final checkout's Git directory
        # would incorrectly validate review receipts against a later entry.
        self.budget.run(['git','init','-q',str(destination)],capture_output=True)
        (destination/'.git/objects/info/alternates').write_text(str(self.object_store)+'\n')
        self.budget.run(['git','update-ref','refs/heads/ratchet',head],cwd=destination,capture_output=True)
        self.budget.run(['git','symbolic-ref','HEAD','refs/heads/ratchet'],cwd=destination,capture_output=True)
        view=SourceView(identity,paths,records,destination);self.by_root[str(destination)]=view
        return view

    def worker(self, kind):
        if kind not in self.workers:self.workers[kind]=TypedWorker(self.original,kind,self.budget,self.scratch.name,self.dependency_root)
        return self.workers[kind]

    def ensure_head(self):
        if self.git.git(['rev-parse','--verify','HEAD^{commit}']).decode().strip()!=self.initial_head:
            raise RatchetFailure('Git HEAD changed during the ratchet invocation; no result accepted')

    def census(self, view, files=None):
        from client_api_census import production,native_sites,identify_sites
        key=('census',view.identity,None if files is None else tuple(sorted(files)))
        if key in self.results:return copy.deepcopy(self.results[key])
        if files is not None and ('census',view.identity,None) in self.results:
            return copy.deepcopy([s for s in self.results[('census',view.identity,None)] if s['file'] in files])
        all_paths=sorted(p for p in view.paths if production(p));paths=all_paths if files is None else [p for p in all_paths if p in files]
        suffixes={'.ts','.tsx','.js','.jsx','.mjs','.cjs'}
        js=[p for p in paths if Path(p).suffix in suffixes];project_files=[p for p in all_paths if Path(p).suffix in suffixes]
        sites=self.worker('census').scan(view,js,project_files=project_files)['sites'] if js else []
        for path in paths:
            if path in js:continue
            if path not in view.records:raise RatchetFailure('Missing counted source '+path)
            _,oid,data=view.records[path];native_key=(path,oid)
            if native_key not in self.native:
                self.native[native_key]=native_sites(path,source_text(data));self.stats['nativeAnalysed']+=1
            else:self.stats['nativeReused']+=1
            sites.extend(copy.deepcopy(self.native[native_key]))
        self.results[key]=identify_sites(sites)
        return copy.deepcopy(self.results[key])

    def query(self, view, paths, configs):
        key=('query',view.identity,tuple(paths),tuple(configs))
        if key not in self.results:self.results[key]=self.worker('query').scan(view,paths,configs)['reads'] if paths else {}
        return copy.deepcopy(self.results[key])

    def metrics(self):
        return {**self.git.stats,**self.stats,'elapsedSeconds':time.monotonic()-self.budget.start,
                'typed':{kind:worker.stats for kind,worker in self.workers.items()}}

    def close(self):
        try:
            for worker in self.workers.values():worker.close()
        finally:self.scratch.cleanup()

    def __enter__(self):self.budget.arm();self.token=_ACTIVE.set(self);return self
    def __exit__(self,*_):
        try:self.close()
        finally:_ACTIVE.reset(self.token);self.budget.disarm()
