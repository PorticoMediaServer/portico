"""Private process-only strace census and serving-worker identity checks."""
import collections,http.client,json,os,pathlib,re,socket,time

class Control(http.client.HTTPConnection):
    def __init__(self,path):super().__init__('localhost',timeout=10);self.path=str(path)
    def connect(self):self.sock=socket.socket(socket.AF_UNIX);self.sock.settimeout(self.timeout);self.sock.connect(self.path)
def control(root,action):
    c=Control(pathlib.Path(root)/'control.sock')
    try:
        c.request('POST','/'+action);r=c.getresponse();assert r.status==200
        return json.loads(r.read())
    finally:c.close()
def stat(pid):
    fields=pathlib.Path(f'/proc/{pid}/stat').read_text().rsplit(')',1)[1].split()
    ticks=os.sysconf('SC_CLK_TCK')
    return {'pid':pid,'parent':int(fields[1]),'startTicks':int(fields[19]),'userSeconds':int(fields[11])/ticks,'systemSeconds':int(fields[12])/ticks,'waitedChildUserSeconds':int(fields[13])/ticks,'waitedChildSystemSeconds':int(fields[14])/ticks}
def worker_identity(supervisor,worker,binary):
    identity=stat(worker);pid=worker;chain=[]
    while pid!=supervisor and pid>1:
        chain.append(pid);pid=stat(pid)['parent']
    assert pid==supervisor,'control PID is outside the owned process tree'
    assert pathlib.Path(f'/proc/{worker}/exe').resolve()==pathlib.Path(binary).resolve(),'wrong worker executable'
    assert b'PORTICO_SUPERVISED_WORKER=1' in pathlib.Path(f'/proc/{worker}/environ').read_bytes().split(b'\0'),'not supervised worker'
    assert worker!=supervisor,'worker attribution fell back to parent'
    return identity

def union(intervals):
    total=0.;last=None
    for start,end in sorted(intervals):
        if end<=start:continue
        if last is None:last=[start,end]
        elif start<=last[1]:last[1]=max(last[1],end)
        else:total+=last[1]-last[0];last=[start,end]
    return total+(last[1]-last[0] if last else 0)

LINE=re.compile(r'^(\d+)\s+(\d+\.\d+)\s+(.*)$')
EXEC=re.compile(r'^execve(?:at)?\([^\"]*"([^\"]+)".*= 0(?:\s|$)')
DURATION=re.compile(r'<([\d.]+)>$')
CLONE=re.compile(r'^((?:clone3?|fork|vfork)\(.*) = (\d+)\s')
def native_census(path,begin,end):
    # Never emit arguments, paths, environment or raw trace lines.
    exe={};born={};intervals=[];starts=[];rows=collections.defaultdict(lambda:{'starts':0,'lifetimeSeconds':0.,'execSyscallSeconds':0.,'birthSyscallSeconds':0.})
    incomplete=0;unfinished={}
    def close(pid,at):
        if pid not in exe:return
        name,first=exe.pop(pid)
        if name not in ('ffprobe','ffmpeg','bwrap','portico-server'):name='other'
        a,b=max(first,begin),min(at,end)
        if b>a:
            rows[name]['lifetimeSeconds']+=b-a
            if name in ('ffprobe','ffmpeg'):intervals.append((a,b))
    for line in pathlib.Path(path).read_text(errors='replace').splitlines():
        m=LINE.match(line)
        if not m:continue
        pid,at,body=int(m[1]),float(m[2]),m[3].rstrip()
        if '<unfinished ...>' in body:
            unfinished[pid]=(at,body.replace('<unfinished ...>',''));continue
        if body.startswith('<... ') and ' resumed>' in body:
            prior=unfinished.pop(pid,None)
            if prior is None:incomplete+=1;continue
            at,prefix=prior;body=prefix+body.split(' resumed>',1)[1]
        duration=DURATION.search(body);elapsed=float(duration[1]) if duration else 0.
        if c:=CLONE.match(body):
            if 'CLONE_THREAD' not in c[1]:born[int(c[2])]=elapsed
        if x:=EXEC.match(body):
            close(pid,at)
            name=pathlib.Path(x[1]).name
            # Qualified engine names end in ffprobe/ffmpeg, not arbitrary args.
            if name not in ('ffprobe','ffmpeg','bwrap','portico-server'):name='other'
            exe[pid]=(name,at)
            if begin<=at<=end:
                rows[name]['starts']+=1
                rows[name]['execSyscallSeconds']+=elapsed
                rows[name]['birthSyscallSeconds']+=born.pop(pid,0.)
                if name in ('ffprobe','ffmpeg'):starts.append((max(at,begin),min(at+elapsed,end)))
        if body.startswith('+++ exited') or body.startswith('+++ killed') or body.startswith('exit_group('):close(pid,at)
    for pid in list(exe):close(pid,end)
    assert not incomplete, 'unmatched resumed process syscall'
    return {'windowSeconds':end-begin,'programs':dict(rows),'nativeLifetimeUnionSeconds':union(intervals),'nativeExecSyscallUnionSeconds':union(starts),'unmatchedResumed':incomplete,'limitations':['strace process syscalls only; native lifetime is occupancy, not removable startup cost.','Exec syscall union excludes loader work after exec returns and sandbox setup before native exec.','CPU from worker /proc includes only that process plus already-waited children; live descendant CPU is separate.']}

def partition(report):
    names=report['names'];ix={n:1<<i for i,n in enumerate(names)}
    parts=collections.Counter()
    for mask,nanos in report['masks'].items():
        m=int(mask)
        # A real scheduler wait while a quantum runs is not scanner idle.
        if m&ix['pacing']:name='pacing'
        elif m&ix['title_wait']:name='title_wait'
        elif not m&ix['quantum'] and m&ix['scheduler_wait']:name='scheduler_wait_without_quantum'
        elif m&ix['quantum'] and m&ix['probe']:name='probe_call'
        elif m&ix['quantum'] and m&ix['writer_begin']:name='writer_begin_overlap_quantum'
        elif m&ix['quantum']:name='other_quantum'
        else:name='loop_or_unassigned'
        parts[name]+=nanos
    assert sum(parts.values())==report['windowNanos'],'partition does not conserve wall time'
    return {k:v/1e9 for k,v in parts.items()}

def tree_cpu(worker):
    pending=[worker];seen=set();nodes=[]
    while pending:
        pid=pending.pop()
        if pid in seen:continue
        seen.add(pid)
        try:
            node=stat(pid);nodes.append(node)
            for children in pathlib.Path(f'/proc/{pid}/task').glob('*/children'):
                try:pending.extend(map(int,children.read_text().split()))
                except (FileNotFoundError,ProcessLookupError):pass
        except (FileNotFoundError,ProcessLookupError):pass
    assert any(n['pid']==worker for n in nodes),'serving worker disappeared'
    keys=['userSeconds','systemSeconds','waitedChildUserSeconds','waitedChildSystemSeconds']
    return {'worker':next(n for n in nodes if n['pid']==worker),'liveProcesses':len(nodes),'treeAccountedCPUSeconds':sum(n[k] for n in nodes for k in keys),'limitations':['Non-atomic /proc tree snapshot: live own CPU plus already-waited descendant CPU, including non-decoder children.']}
