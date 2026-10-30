import test from 'node:test';
import assert from 'node:assert/strict';
import {DraftForm, type Loaded} from '../src/server-admin/draft-form.ts';

type Value={name:string;limit:number};
type Status={revision:number};
const loaded=(name='A',limit=1,revision=1):Loaded<Value,Status>=>({value:{name,limit},status:{revision}});
function deferred<T>() {let resolve!:(value:T)=>void, reject!:(error:unknown)=>void;const promise=new Promise<T>((yes,no)=>{resolve=yes;reject=no;});return {promise,resolve,reject};}

test('quiet A/B/C conflict preserves the entire base, draft, status and failed-save acknowledgement; Save still uses rev1',async()=>{
  let remote=loaded(),failWrite=true;
  const writeError=Object.assign(new Error('write lost'),{code:'lost'}),sent:number[]=[];
  const form=new DraftForm<Value,Status>({load:async()=>remote,save:async(_draft,_base,status)=>{
    sent.push(status!.revision);
    if(failWrite)throw writeError;
    throw Object.assign(new Error('changed'),{status:409,code:'revision_conflict'});
  }});
  await form.load();form.set({name:'B'});await assert.rejects(form.save(),error=>error===writeError);
  const before=form.getSnapshot();remote=loaded('C',1,2);
  assert.deepEqual(await form.refresh(),{kind:'conflict'});assert.equal(form.getSnapshot(),before);
  failWrite=false;await assert.rejects(form.save(),/changed/);
  assert.deepEqual(sent,[1,1]);assert.equal(form.getSnapshot().status!.revision,2); // Existing explicit Save409 path owns this rebase.
  form.dispose();
});

test('quiet nonconflicting refresh takes fresh revision without treating it as an edit and preserves current edits made during I/O',async()=>{
  const read=deferred<Loaded<Value,Status>>();let quiet=false;
  const form=new DraftForm<Value,Status>({load:async()=>quiet?read.promise:loaded(),save:async()=>{}});
  await form.load();form.set({name:'B'});quiet=true;const pending=form.refresh();
  form.set({name:'typed while reading'});read.resolve(loaded('A',2,2));
  assert.deepEqual(await pending,{kind:'adopted'});
  assert.deepEqual(form.getSnapshot().saved,{name:'A',limit:2});assert.deepEqual(form.getSnapshot().draft,{name:'typed while reading',limit:2});
  assert.equal(form.getSnapshot().status!.revision,2);assert.equal(form.getSnapshot().dirty,true);
  form.discard();assert.equal(form.getSnapshot().dirty,false);form.dispose();
});

test('clean refresh stays clean and does not clear a save error',async()=>{
  let remote=loaded();const error=new Error('write failed');
  const form=new DraftForm<Value,Status>({load:async()=>remote,save:async()=>{throw error;}});
  await form.load();form.set({name:'B'});await assert.rejects(form.save());
  remote=loaded('A',2,2);assert.deepEqual(await form.refresh(),{kind:'adopted'});
  assert.equal(form.getSnapshot().saveError,error);assert.equal(form.getSnapshot().dirty,true);
  form.set({name:'A'});assert.equal(form.getSnapshot().dirty,false);
  remote=loaded('C',3,3);await form.refresh();assert.equal(form.getSnapshot().dirty,false);form.dispose();
});

test('Save admission invalidates quiet adoption even when Save completes before the quiet read',async()=>{
  const read=deferred<Loaded<Value,Status>>();let quiet=false,signal:AbortSignal|undefined;
  const form=new DraftForm<Value,Status>({load:async s=>{signal=s;return quiet?read.promise:loaded();},save:async draft=>({value:draft,status:{revision:3}})});
  await form.load();form.set({name:'B'});quiet=true;const pending=form.refresh();
  await form.save();const saved=form.getSnapshot();assert.equal(saved.status!.revision,3);assert.equal(signal!.aborted,true);
  read.resolve(loaded('A',2,2));assert.deepEqual(await pending,{kind:'deferred'});assert.equal(form.getSnapshot(),saved);form.dispose();
});

test('saving is deferred before I/O, and a failed write admission also fences the old read',async()=>{
  const read=deferred<Loaded<Value,Status>>(),write=deferred<Loaded<Value,Status>>();let reads=0;
  const form=new DraftForm<Value,Status>({load:async()=>++reads===1?loaded():read.promise,save:async()=>write.promise});
  await form.load();form.set({name:'B'});const pending=form.refresh(),saving=form.save();
  assert.deepEqual(await form.refresh(),{kind:'deferred'});assert.equal(reads,2);
  const error=new Error('failed');write.reject(error);await assert.rejects(saving,error);
  const before=form.getSnapshot();read.resolve(loaded('A',2,2));assert.deepEqual(await pending,{kind:'deferred'});assert.equal(form.getSnapshot(),before);form.dispose();
});

for(const action of ['discard','load','dispose'] as const)test(`${action} invalidates a late quiet read without adopting it`,async()=>{
  const read=deferred<Loaded<Value,Status>>();let reads=0;
  const form=new DraftForm<Value,Status>({load:async()=>++reads===2?read.promise:loaded(),save:async()=>{}});
  await form.load();form.set({name:'B'});const pending=form.refresh();
  if(action==='load')await form.load();else form[action]();
  const before=form.getSnapshot();read.resolve(loaded('A',2,2));assert.deepEqual(await pending,{kind:'deferred'});assert.equal(form.getSnapshot(),before);form.dispose();
});

test('a newer quiet read supersedes the first; edit returning to base does not hide invalidation',async()=>{
  const first=deferred<Loaded<Value,Status>>(),second=deferred<Loaded<Value,Status>>();let reads=0;
  const form=new DraftForm<Value,Status>({load:async()=>++reads===1?loaded():reads===2?first.promise:second.promise,save:async()=>{}});
  await form.load();const a=form.refresh(),b=form.refresh();second.resolve(loaded('C',3,3));assert.deepEqual(await b,{kind:'adopted'});
  const before=form.getSnapshot();first.resolve(loaded('B',2,2));assert.deepEqual(await a,{kind:'deferred'});assert.equal(form.getSnapshot(),before);form.dispose();
});

test('unloaded quiet refresh uses the actual read result: failure retains original error and absence uses unavailable',async()=>{
  const error=Object.assign(new Error('busy'),{retryAfterSeconds:7200});let mode:'fail'|'absent'|'value'='fail';
  const form=new DraftForm<Value,Status>({load:async()=>{if(mode==='fail')throw error;return mode==='absent'?null:loaded();},save:async()=>{}});
  const before=form.getSnapshot();const answer=await form.refresh();assert.equal(answer.kind,'unreadable');if(answer.kind==='unreadable')assert.equal(answer.error,error);
  assert.equal(form.getSnapshot(),before);mode='absent';assert.deepEqual(await form.refresh(),{kind:'adopted'});assert.equal(form.getSnapshot().unavailable,true);
  mode='value';await form.refresh();form.set({name:'B'});mode='absent';const edited=form.getSnapshot();assert.deepEqual(await form.refresh(),{kind:'conflict'});assert.equal(form.getSnapshot(),edited);form.dispose();
});

test('an explicit load in flight defers quiet adoption, including replacement of an already loaded form',async()=>{
 let read=deferred<Loaded<Value,Status>>(),reads=0,waiting=true;
 const form=new DraftForm<Value,Status>({load:async()=>{reads++;return waiting?read.promise:loaded();},save:async()=>{}});
 const initial=form.load();assert.deepEqual(await form.refresh(),{kind:'deferred'});assert.equal(reads,1);read.resolve(loaded());await initial;
 read=deferred();const replacement=form.load();assert.deepEqual(await form.refresh(),{kind:'deferred'});assert.equal(reads,2);read.resolve(loaded());await replacement;
 waiting=false;assert.deepEqual(await form.refresh(),{kind:'adopted'});form.dispose();
});

test('a field first edited during the quiet read is checked for conflict against the original base',async()=>{
 const read=deferred<Loaded<Value,Status>>();let quiet=false;
 const form=new DraftForm<Value,Status>({load:async()=>quiet?read.promise:loaded(),save:async()=>{}});
 await form.load();quiet=true;const pending=form.refresh();form.set({name:'B'});const before=form.getSnapshot();
 read.resolve(loaded('C',1,2));assert.deepEqual(await pending,{kind:'conflict'});assert.equal(form.getSnapshot(),before);form.dispose();
});


test('buffered widget edit admission cancels quiet adoption without publication or command reset', async () => {
  let reads = 0, finish!: (value: {value: {name: string}; status: {revision: number}}) => void;
  let signal: AbortSignal | undefined;
  const form = new DraftForm<{name: string}, {revision: number}>({
    load: async active => {
      signal = active;
      if (++reads === 2) return new Promise(resolve => {finish = resolve;});
      return {value: {name: reads === 1 ? 'A' : 'C'}, status: {revision: reads === 1 ? 1 : 2}};
    }, save: async draft => ({value: draft, status: {revision: 3}}),
  });
  await form.load();const original = form.getSnapshot();let publications = 0;
  form.subscribe(() => {publications++;});
  const pending = form.refresh();
  // SettingText keeps B private until blur. Focus/input admission must fence
  // the read before that unfinished edit has reached the canonical form.
  const widgetDraft = 'B';
  const invalidate = form.invalidateQuiet;invalidate();
  assert.equal(signal?.aborted, true);assert.equal(form.getSnapshot(), original);assert.equal(publications, 0);
  finish({value: {name: 'C'}, status: {revision: 2}});
  assert.deepEqual(await pending, {kind: 'deferred'});assert.equal(form.getSnapshot(), original);assert.equal(publications, 0);assert.equal(widgetDraft, 'B');
  form.set({name: widgetDraft});
  assert.deepEqual(await form.refresh(), {kind: 'conflict'});assert.equal(form.getSnapshot().draft?.name, 'B');assert.equal(form.getSnapshot().status?.revision, 1);
  form.discard();assert.deepEqual(await form.refresh(), {kind: 'adopted'});assert.equal(form.getSnapshot().saved?.name, 'C');assert.equal(form.getSnapshot().status?.revision, 2);
  form.dispose();
});
