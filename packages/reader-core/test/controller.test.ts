import assert from 'node:assert/strict';
import {test} from 'node:test';
import {ReaderController} from '../src/controller.ts';
import {ReaderError} from '../src/errors.ts';
import type {ReaderAdapter, ReaderDocument, ReaderMount, ReaderView} from '../src/ports.ts';

type Prefs = {theme: string; textSize: number; twoPages: boolean};
const defaults: Prefs = {theme: 'dark', textSize: 5, twoPages: true};
const source = {id: 'lawful-local', revision: 'immutable-fixture-v1', size: 1, async read() { return new Uint8Array([1]); }};
const location = {format: 'epub' as const, cfi: 'epubcfi(/6/2!/4/2)', progression: 0};
const container = {clientWidth: 1180, clientHeight: 820} as HTMLElement;
function deferred<T>() { let resolve!: (value: T) => void; const promise = new Promise<T>(r => {resolve = r;}); return {promise, resolve}; }
function fixture() {
  const seen: Readonly<Prefs>[] = [];
  let viewsDisposed = 0, docsDisposed = 0;
  let mount!: ReaderMount<Prefs>;
  const view: ReaderView<Prefs> = {
    async navigate() {}, async applyPreferences(p) {seen.push(p);}, async resize() {},
    getLocation() {return location;}, dispose() {viewsDisposed++;}
  };
  const document: ReaderDocument<Prefs> = {id: source.id, revision: source.revision, format: 'epub', metadata: {title: 'Actual local book'},
    toc: [{id: 'chapter-1', label: 'I', location}],
    async mount(options) {mount = options; return view;},
    async *search(_query, options) {
      for (let i = 0; i < 10; i++) { if (options.signal.aborted) throw new Error('aborted'); yield {id: String(i), excerpt: 'Actual quotation', location}; }
    }, dispose() {docsDisposed++;}
  };
  const adapter: ReaderAdapter<Prefs> = {format: 'epub', async open() {return document;}};
  const controller = new ReaderController({adapters: [adapter], preferences: defaults});
  return {controller, adapter, document, view, seen, get mount() {return mount;}, get viewsDisposed() {return viewsDisposed;}, get docsDisposed() {return docsDisposed;}};
}
test('live defaults update unoverridden fields; overrides clear on close and never mutate input', async () => {
  const f = fixture(); let publications = 0;
  const unsubscribe = f.controller.subscribe(() => publications++);
  const before = f.controller.getSnapshot(); assert.equal(before, f.controller.getSnapshot());
  await f.controller.open({source, format: 'epub', container});
  await f.controller.setOverride({textSize: 7});
  await f.controller.updatePreferences({theme: 'sepia', textSize: 2, twoPages: false});
  assert.deepEqual(f.controller.getSnapshot().preferences, {theme: 'sepia', textSize: 7, twoPages: false});
  assert.deepEqual(f.seen.at(-1), f.controller.getSnapshot().preferences);
  assert.deepEqual(defaults, {theme: 'dark', textSize: 5, twoPages: true});
  assert.ok(Object.isFrozen(f.controller.getSnapshot()));
  assert.equal(f.controller.getSnapshot(), f.controller.getSnapshot());
  await f.controller.close();
  assert.deepEqual(f.controller.getSnapshot().preferences, {theme: 'sepia', textSize: 2, twoPages: false});
  assert.deepEqual(f.controller.getSnapshot().overriddenKeys, []);
  assert.equal(f.viewsDisposed, 1); assert.equal(f.docsDisposed, 1); assert.ok(publications > 3);
  unsubscribe();
});
test('close during async open aborts the source and disposes the late document without mounting', async () => {
  const f = fixture(), pending = deferred<ReaderDocument<Prefs>>(); let signal!: AbortSignal, mounted = 0;
  f.document.mount = async () => {mounted++; return f.view;};
  f.adapter.open = async options => {signal = options.signal; return pending.promise;};
  const opening = f.controller.open({source, format: 'epub', container});
  await new Promise(resolve => setImmediate(resolve));
  await f.controller.close(); assert.ok(signal.aborted);
  pending.resolve(f.document); await opening;
  assert.equal(f.docsDisposed, 1); assert.equal(mounted, 0); assert.equal(f.controller.getSnapshot().status, 'closed');
});
test('overlapping open cannot adopt old callbacks or dispose a newer view', async () => {
  const first = fixture(), second = fixture(), pending = deferred<ReaderDocument<Prefs>>(); let calls = 0;
  first.adapter.open = async () => ++calls === 1 ? pending.promise : second.document;
  const a = first.controller.open({source, format: 'epub', container});
  await new Promise(resolve => setImmediate(resolve));
  await first.controller.open({source, format: 'epub', container});
  pending.resolve(first.document); await a;
  assert.equal(first.docsDisposed, 1); assert.equal(second.docsDisposed, 0); assert.equal(first.controller.getSnapshot().status, 'ready');
  await first.controller.close(); assert.equal(second.docsDisposed, 1);
});
test('changes arriving while mounting are applied before ready', async () => {
  const f = fixture(), pending = deferred<ReaderView<Prefs>>(); f.document.mount = async () => pending.promise;
  const opening = f.controller.open({source, format: 'epub', container});
  await new Promise(resolve => setImmediate(resolve));
  await f.controller.setOverride({theme: 'black'});
  await f.controller.updatePreferences({theme: 'light', textSize: 3, twoPages: false});
  pending.resolve(f.view); await opening;
  assert.deepEqual(f.seen.at(-1), {theme: 'black', textSize: 3, twoPages: false});
  await f.controller.close();
});
test('search limits results and is fenced by session close; stale locations are ignored', async () => {
  const f = fixture(); await f.controller.open({source, format: 'epub', container});
  const results = [];
  for await (const r of f.controller.search('quote', {signal: new AbortController().signal, limit: 2})) results.push(r);
  assert.equal(results.length, 2);
  const iter = f.controller.search('quote', {signal: new AbortController().signal, limit: 5})[Symbol.asyncIterator]();
  await iter.next(); await f.controller.close();
  await assert.rejects(iter.next());
  const snapshot = f.controller.getSnapshot(); f.mount.onLocation({...location, progression: 1});
  assert.equal(snapshot, f.controller.getSnapshot());
});
test('external search cancellation rejects a late engine result without waiting for it',async()=>{
  const f=fixture(),abort=new AbortController(),release=deferred<void>();let entered=false;
  f.document.search=async function*(){entered=true;yield {id:'first',excerpt:'Actual quotation',location};await release.promise;yield {id:'late',excerpt:'Actual quotation',location};};
  await f.controller.open({source,format:'epub',container});
  const iter=f.controller.search('quote',{signal:abort.signal,limit:5})[Symbol.asyncIterator]();
  try {
    assert.equal((await iter.next()).value?.id,'first');assert.equal(entered,true);
    const pending=iter.next();abort.abort();
    const outcome=await Promise.race([pending.then(()=> 'resolved',error=>error),new Promise(resolve=>setTimeout(()=>resolve('timeout'),100))]);
    assert.ok(outcome instanceof ReaderError);assert.equal(outcome.code,'aborted');
  }finally{release.resolve();await new Promise(resolve=>setImmediate(resolve));await f.controller.close();}
});
test('aborted search swallows iterator close errors and keeps the abort code',async()=>{
  for(const mode of ['sync','async'] as const){
    const f=fixture(),abort=new AbortController(),pending=deferred<IteratorResult<{id:string;excerpt:string;location:typeof location}>>();let calls=0,returnCalls=0;
    f.document.search=()=>({[Symbol.asyncIterator](){return {
      next:()=>++calls===1?Promise.resolve({done:false,value:{id:'first',excerpt:'Actual quotation',location}}):pending.promise,
      return:()=>{returnCalls++;if(mode==='sync')throw new Error('sync close');return Promise.reject(new Error('async close'));}
    };}});
    await f.controller.open({source,format:'epub',container});
    const iter=f.controller.search('quote',{signal:abort.signal,limit:5})[Symbol.asyncIterator]();
    try {
      assert.equal((await iter.next()).value?.id,'first');
      const late=iter.next();abort.abort();
      const outcome=await Promise.race([late.then(()=> 'resolved',error=>error),new Promise(resolve=>setTimeout(()=>resolve('timeout'),100))]);
      assert.ok(outcome instanceof ReaderError);assert.equal(outcome.code,'aborted');assert.equal(returnCalls,1);
    }finally{pending.resolve({done:true,value:undefined});await new Promise(resolve=>setImmediate(resolve));await f.controller.close();}
  }
});
test('opening errors expose codes and no arbitrary exception text; cleanup covers view failure', async () => {
  const f = fixture(); f.adapter.open = async () => {throw new Error('private URL/token must not be snapshot copy');};
  await f.controller.open({source, format: 'epub', container});
  assert.equal(f.controller.getSnapshot().error, 'render_failed');
  assert.equal(f.controller.getSnapshot().status, 'error');
  assert.ok(!JSON.stringify(f.controller.getSnapshot()).includes('private URL'));
  const g = fixture(); await g.controller.open({source, format: 'epub', container});
  g.view.dispose = () => {throw new Error('failed renderer cleanup');};
  await assert.rejects(g.controller.close()); assert.equal(g.docsDisposed, 1);
});
test('source revision mismatch refuses the document and disposes it',async()=>{
  const f=fixture();Object.defineProperty(f.document,'revision',{value:'other-revision'});
  await f.controller.open({source,format:'epub',container});
  assert.equal(f.controller.getSnapshot().status,'error');assert.equal(f.controller.getSnapshot().error,'invalid_book');assert.equal(f.docsDisposed,1);
});
test('iframe surface input is synchronous and fenced; measured fixed zoom reaches snapshot',async()=>{
  const f=fixture();let prevented=false,inputs=0;
  f.view.getZoom=()=>({scale:1.5});f.view.canTurnAtFit=()=>false;
  const off=f.controller.subscribeSurfaceInput(event=>{inputs++;if(event.kind==='key')event.preventDefault();});
  await f.controller.open({source,format:'epub',container});
  f.mount.onSurfaceInput?.({kind:'key',key:'ArrowRight',shift:false,alt:false,ctrl:false,meta:false,editable:false,preventDefault(){prevented=true;}});
  assert.ok(prevented);assert.equal(inputs,1);assert.equal(f.controller.getSnapshot().canTurnAtFit,false);assert.equal(f.controller.getSnapshot().zoom?.scale,1.5);
  await f.controller.close();f.mount.onSurfaceInput?.({kind:'tap',fraction:.9});assert.equal(inputs,1);off();
});
test('settings updates during initial apply queue behind that apply',async()=>{
  const f=fixture(),pending=deferred<void>();let entered=false,inFlight=0,peak=0;
  f.view.applyPreferences=async p=>{inFlight++;peak=Math.max(peak,inFlight);f.seen.push(p);if(!entered){entered=true;await pending.promise;}inFlight--;};
  const opening=f.controller.open({source,format:'epub',container});
  while(!entered)await new Promise(resolve=>setImmediate(resolve));
  const updating=f.controller.updatePreferences({theme:'light',textSize:3,twoPages:false});
  pending.resolve();await Promise.all([opening,updating]);assert.equal(peak,1);assert.equal(f.seen.at(-1)?.theme,'light');await f.controller.close();
});
test('authoritative catalogue omission never revives embedded file identity',async()=>{
  const f=fixture();Object.defineProperty(f.document,'metadata',{value:{title:'Embedded title',author:'Embedded author',language:'en',pageCount:40,direction:'rtl'}});
  await f.controller.open({source,format:'epub',container,metadata:{title:'Owner catalogue title'}});
  const metadata=f.controller.getSnapshot().metadata;
  assert.equal(metadata?.title,'Owner catalogue title');assert.equal(metadata?.author,undefined);assert.equal(metadata?.language,undefined);
  assert.equal(f.mount.metadata,metadata);
  assert.equal(metadata?.pageCount,40);assert.equal(metadata?.direction,'rtl');await f.controller.close();
});
test('previews use one current document, own bytes, enforce bounds and discard late cancelled results',async()=>{
  const f=fixture(),pending=deferred<import('../src/ports.ts').ReaderPageImage>();let signal!:AbortSignal,calls=0;
  Object.defineProperty(f.document,'metadata',{value:{pageCount:2}});
  const bytes=new Uint8Array([1,2,3]);
  f.document.page=async(index,options)=>{calls++;assert.equal(index,1);assert.equal(options.width,120);signal=options.signal;return calls===1?{mime:'image/png',bytes,width:120,height:180}:pending.promise;};
  await f.controller.open({source,format:'epub',container});
  const options={width:120,signal:new AbortController().signal};
  const image=await f.controller.page(1,options);assert.notEqual(image.bytes,bytes);bytes[0]=9;assert.equal(image.bytes[0],1);
  await assert.rejects(f.controller.page(2,options),{code:'invalid_location'});
  await assert.rejects(f.controller.page(0,{...options,width:2049}),{code:'limit_exceeded'});assert.equal(calls,1);
  const preview=f.controller.page(1,options);const rejected=assert.rejects(preview,{code:'aborted'});
  await f.controller.close();assert.ok(signal.aborted);await rejected;
  pending.resolve({mime:'image/png',bytes,width:120,height:180});await new Promise(resolve=>setImmediate(resolve));
  assert.equal(f.controller.getSnapshot().status,'closed');assert.equal(f.docsDisposed,1);
  await assert.rejects(f.controller.page(1,options),{code:'closed'});
});
test('gesture activity publishes changed measured zoom before forwarding and preserves no-op snapshot identity',async()=>{
  const f=fixture();let scale=1,fit:'page'|'width'|undefined='page',canTurn=true,publications=0,inputs=0;
  f.view.getZoom=()=>({scale,...(fit?{fit}:{})});f.view.canTurnAtFit=()=>canTurn;
  await f.controller.open({source,format:'epub',container});
  const oldMount=f.mount;
  f.controller.subscribe(()=>publications++);
  f.controller.subscribeSurfaceInput(()=>{
    inputs++;assert.equal(f.controller.getSnapshot().zoom?.scale,scale);
    assert.equal(f.controller.getSnapshot().zoom?.fit,fit);
    assert.equal(f.controller.getSnapshot().canTurnAtFit,canTurn);
  });
  const before=f.controller.getSnapshot();
  oldMount.onSurfaceInput?.({kind:'activity'});
  assert.equal(f.controller.getSnapshot(),before);assert.equal(publications,0);assert.equal(inputs,1);
  scale=2;fit=undefined;canTurn=false;
  oldMount.onSurfaceInput?.({kind:'activity'});
  assert.equal(publications,1);assert.equal(inputs,2);assert.ok(Object.isFrozen(f.controller.getSnapshot().zoom));
  const changed=f.controller.getSnapshot();oldMount.onSurfaceInput?.({kind:'activity'});
  assert.equal(f.controller.getSnapshot(),changed);assert.equal(publications,1);
  fit='width';canTurn=true;oldMount.onSurfaceInput?.({kind:'activity'});
  assert.equal(publications,2);assert.equal(inputs,4);
  await f.controller.open({source,format:'epub',container});
  const replacement=f.controller.getSnapshot(),inputCount=inputs;
  oldMount.onSurfaceInput?.({kind:'activity'});
  assert.equal(f.controller.getSnapshot(),replacement);assert.equal(inputs,inputCount);
  await f.controller.close();const closed=f.controller.getSnapshot();
  f.mount.onSurfaceInput?.({kind:'activity'});assert.equal(f.controller.getSnapshot(),closed);assert.equal(inputs,inputCount);
});

for(const next of ['replacement','close'] as const)test(`delayed failed-open cleanup cannot overwrite ${next} state`,async()=>{
  const f=fixture(),replacement=fixture(),disposing=deferred<void>(),release=deferred<void>();let calls=0,retiredDisposed=0;
  f.document.mount=async()=>{throw new Error('failed old renderer');};
  f.document.dispose=async()=>{retiredDisposed++;disposing.resolve();await release.promise;};
  f.adapter.open=async()=>++calls===1?f.document:replacement.document;
  const failed=f.controller.open({source,format:'epub',container});
  await disposing.promise;
  try {
    if(next==='replacement')await f.controller.open({source,format:'epub',container});
    else await f.controller.close();
    const current=f.controller.getSnapshot();
    assert.equal(current.status,next==='replacement'?'ready':'closed');
    release.resolve();await failed;
    assert.equal(f.controller.getSnapshot(),current);
    assert.equal(f.controller.getSnapshot().error,undefined);
    assert.equal(retiredDisposed,1);
    assert.equal(replacement.docsDisposed,0);
  }finally{release.resolve();await failed;await f.controller.close();}
});

test('authority retirement aborts a pending open and disposes late documents without mounting',async()=>{
  const f=fixture(),authority=new AbortController(),pending=deferred<ReaderDocument<Prefs>>();let engineSignal!:AbortSignal,mounted=0;
  f.adapter.open=async({signal})=>{engineSignal=signal;return pending.promise;};
  f.document.mount=async()=>{mounted++;return f.view;};
  const opening=f.controller.open({source,format:'epub',container,signal:authority.signal});await new Promise(done=>setImmediate(done));
  authority.abort();assert.equal(f.controller.getSnapshot().status,'closed');assert.equal(engineSignal.aborted,true);
  pending.resolve(f.document);await opening;assert.equal(mounted,0);assert.equal(f.docsDisposed,1);
  const preaborted=fixture();await preaborted.controller.open({source,format:'epub',container,signal:authority.signal});
  assert.equal(preaborted.controller.getSnapshot().status,'closed');assert.equal(preaborted.docsDisposed,0);
});

test('retiring an old authority cannot close its replacement; current retirement clears cached view and local overrides',async()=>{
  const f=fixture(),old=new AbortController(),current=new AbortController();let added=0,removed=0;
  const add=old.signal.addEventListener.bind(old.signal),remove=old.signal.removeEventListener.bind(old.signal);
  old.signal.addEventListener=(...args:Parameters<AbortSignal['addEventListener']>)=>{added++;return add(...args);};old.signal.removeEventListener=(...args:Parameters<AbortSignal['removeEventListener']>)=>{removed++;return remove(...args);};
  await f.controller.open({source,format:'epub',container,signal:old.signal});
  await f.controller.open({source,format:'epub',container,signal:current.signal});const ready=f.controller.getSnapshot();
  assert.equal(added,1);assert.equal(removed,1);old.abort();assert.equal(f.controller.getSnapshot(),ready);
  await f.controller.setOverride({textSize:7});current.abort();assert.equal(f.controller.getSnapshot().status,'closed');assert.deepEqual(f.controller.getSnapshot().overriddenKeys,[]);
  await new Promise(done=>setImmediate(done));assert.equal(f.viewsDisposed,2);assert.equal(f.docsDisposed,2);
});

test('contents commands resolve only the captured document identity before navigating',async()=>{
  const f=fixture(),calls:number[]=[],navigated:unknown[]=[];
  const resolved={format:'epub' as const,cfi:'epubcfi(/6/2!/4/2)',progression:0};
  f.document.resolveContents=async(index,signal)=>{assert.equal(signal.aborted,false);calls.push(index);return resolved;};
  f.view.navigate=async target=>{navigated.push(target);};
  await f.controller.open({source,format:'epub',container});
  await assert.rejects(f.controller.navigateContents({sourceId:'another-source',sourceRevision:source.revision,index:0}),{code:'invalid_source'});
  await assert.rejects(f.controller.navigateContents({sourceId:source.id,sourceRevision:'another-revision',index:0}),{code:'invalid_source'});
  await assert.rejects(f.controller.navigateContents({sourceId:source.id,sourceRevision:source.revision,index:-1}),{code:'invalid_location'});
  assert.deepEqual(calls,[]);assert.deepEqual(navigated,[]);
  await f.controller.navigateContents({sourceId:source.id,sourceRevision:source.revision,index:0});
  assert.deepEqual(calls,[0]);assert.deepEqual(navigated,[{kind:'location',location:resolved}]);await f.controller.close();
});

test('a retired or caller-aborted contents resolution never reaches the mounted view',async()=>{
  for(const retire of ['close','caller'] as const){
    const f=fixture(),pending=deferred<import('../src/ports.ts').ReaderLocation>(),caller=new AbortController();let reads=0,moves=0;
    f.document.resolveContents=async()=>{reads++;return pending.promise;};
    f.view.navigate=async()=>{moves++;};
    await f.controller.open({source,format:'epub',container});
    const command=f.controller.navigateContents({sourceId:source.id,sourceRevision:source.revision,index:0},caller.signal);
    const rejected=assert.rejects(command,{code:'aborted'});
    await new Promise(done=>setImmediate(done));assert.equal(reads,1);
    if(retire==='close')await f.controller.close();else caller.abort();
    pending.resolve({format:'epub',cfi:'epubcfi(/6/2!/4/2)',progression:0});
    await rejected;assert.equal(moves,0);await f.controller.close();
  }
});

test('a delayed outline does not block first-page readiness or search', async () => {
  const f = fixture(), outline = deferred<readonly import('../src/ports.ts').ReaderTocItem[]>();
  const expected = f.document.toc;
  Object.defineProperty(f.document, 'toc', {value: [], configurable: true});
  let signal: AbortSignal | undefined;
  f.document.loadToc = async active => {signal = active; return outline.promise;};
  await f.controller.open({source, format: 'epub', container});
  assert.equal(f.controller.getSnapshot().status, 'ready');
  assert.deepEqual(f.controller.getSnapshot().toc, []);
  const hits = [];
  for await (const hit of f.controller.search('Actual', {limit: 2, signal: new AbortController().signal})) hits.push(hit);
  assert.ok(hits.length > 0);
  outline.resolve(expected); await new Promise(resolve => setImmediate(resolve));
  assert.deepEqual(f.controller.getSnapshot().toc, expected);
  await f.controller.close(); assert.equal(signal?.aborted, true);
});

test('a replaced document cannot publish its delayed outline or location', async () => {
  const f = fixture(), next = fixture(), outline = deferred<readonly import('../src/ports.ts').ReaderTocItem[]>();
  f.document.loadToc = async () => outline.promise;
  await f.controller.open({source, format: 'epub', container});
  f.adapter.open = async () => next.document;
  await f.controller.open({source, format: 'epub', container});
  const snapshot = f.controller.getSnapshot();
  outline.resolve([{id: 'old', label: 'Old document', location}]);
  await new Promise(resolve => setImmediate(resolve));
  assert.equal(f.controller.getSnapshot(), snapshot);
  await f.controller.close();
});

test('outline failure from a closed document cannot replace the closed snapshot', async () => {
  const f = fixture(); let reject!: (error: unknown) => void;
  f.document.loadToc = () => new Promise((_, failed) => {reject = failed;});
  await f.controller.open({source, format: 'epub', container});
  await new Promise(resolve => setImmediate(resolve));
  await f.controller.close(); const closed = f.controller.getSnapshot();
  reject(new ReaderError('invalid_book')); await new Promise(resolve => setImmediate(resolve));
  assert.equal(f.controller.getSnapshot(), closed);
});

test('held page keys never queue behind the original in-flight turn', async () => {
  const f = fixture(), pending = deferred<void>();let calls = 0;
  f.view.navigate = async () => {calls++;await pending.promise;};
  await f.controller.open({source, format: 'epub', container});
  const first = f.controller.navigate({kind: 'next'});
  await Promise.resolve();
  await Promise.all([f.controller.navigate({kind: 'next'}), f.controller.navigate({kind: 'previous'}), f.controller.navigate({kind: 'next'})]);
  assert.equal(calls, 1);pending.resolve();await first;
  await f.controller.navigate({kind: 'next'});assert.equal(calls, 2);await f.controller.close();
});

test('the original mount policy methods are captured before adapter IO, without changing source retirement', async () => {
  const f = fixture(), pending = deferred<ReaderDocument<Prefs>>();let reduced = false, stops = 0;
  f.adapter.open = async () => pending.promise;
  const policy = {reduced: () => reduced, subscribe: (_changed: () => void) => () => {stops++;}};
  const opening = f.controller.open({source, format: 'epub', container, motion: policy});
  policy.reduced = () => true;policy.subscribe = () => () => {stops += 100;};
  pending.resolve(f.document);await opening;
  assert.equal(f.mount.motion!.reduced(), false);reduced = true;assert.equal(f.mount.motion!.reduced(), true);
  f.mount.motion!.subscribe(() => {})();assert.equal(stops, 1);
  const old = f.mount;let turns = 0;f.controller.subscribeSurfaceInput(input => {if (input.kind === 'turned') turns++;});
  old.onSurfaceInput?.({kind: 'turned'});assert.equal(turns, 1);
  await f.controller.close();old.onSurfaceInput?.({kind: 'turned'});assert.equal(turns, 1);
});
