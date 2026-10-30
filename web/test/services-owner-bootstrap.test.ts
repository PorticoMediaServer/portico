import test from 'node:test';
import assert from 'node:assert/strict';
import {captureServicesEntry} from '../src/services/entry.ts';
import {ServicesBootstrap} from '../src/services/bootstrap.ts';
import type {ServicesAccount} from '../src/services/session.ts';
import type {HostedOwnerPending, HostedOwnerPendingStore} from '../src/services/owner-pending.ts';

const origin='https://account.example',code='claim_'+Buffer.alloc(32,7).toString('base64url');
const request={hostedOrigin:origin,code,operation:'installed-operation',nameHint:'Actual server'};
function fixture() {
  const calls:string[]=[];let saved:HostedOwnerPending|undefined,failCookie=false;
  const store:HostedOwnerPendingStore={assertAvailable:()=>{calls.push('guard');},read:async()=>{calls.push('owner-read');return saved;},
    change:async update=>{calls.push('owner-write');saved=update(saved);}};
  const service={synchronize:async()=>{calls.push('cookie');if(failCookie)throw Error('cookie unavailable');},getSnapshot:()=>({}),accessSession:async()=>{throw Error('no session');}};
  const account={service,api:{origin,send:async()=>{throw Error('no automatic owner request');}}} as unknown as ServicesAccount;
  const boot=(capture?:{request?:typeof request})=>new ServicesBootstrap(account,()=>{throw Error('ordinary link store must not open');},undefined,undefined,{origin,capture,store:()=>store});
  return {calls,store,account,boot,get saved(){return saved;},set saved(value:HostedOwnerPending|undefined){saved=value;},set failCookie(value:boolean){failCookie=value;}};
}
test('real owner intake scrubs before generic handling and never becomes an ordinary sign-in',()=>{
  const hash='#code='+code+'&operation=installed-operation&server=Actual%20server';
  const page={href:origin+'/claim'+hash,pathname:'/claim',search:'',hash,replace(){throw Error('no native navigation');}};
  const history={state:null,replaceState:()=>{page.hash='';page.href=origin+'/claim';}};
  const result=captureServicesEntry(page,history,origin);
  assert.deepEqual(result.owner,{request});assert.equal(result.signIn,undefined);assert.equal(result.capture,undefined);assert.equal(page.hash,'');
});
test('a malformed owner fragment is scrubbed and cannot fall through to another continuation',()=>{
  const page={href:origin+'/claim#code=bad',pathname:'/claim',search:'',hash:'#code=bad',replace(){throw Error('no native navigation');}};
  const history={state:null,replaceState:()=>{page.hash='';}};
  assert.deepEqual(captureServicesEntry(page,history,origin),{owner:{}});assert.equal(page.hash,'');
});
test('protected owner restore precedes cookie work, with no code in observable state or auto review',async()=>{
  const f=fixture(),boot=f.boot({request});assert.equal(boot.ownerApproval(f.account),undefined);
  await boot.start();assert.equal(boot.getSnapshot().ownerPending,true);assert.equal(f.saved?.request.code,code);
  assert.ok(f.calls.indexOf('owner-write')<f.calls.indexOf('cookie'));assert.doesNotMatch(JSON.stringify(boot.getSnapshot()),new RegExp(code));
  const controller=boot.ownerApproval(f.account);assert.ok(controller);assert.equal(boot.ownerApproval({...f.account,service:{} as ServicesAccount['service']}),undefined);
  boot.dispose();assert.equal(boot.ownerApproval(f.account),undefined);await assert.rejects(controller.review());
  assert.equal(f.saved?.request.code,code,'document disposal retains the bounded provider-navigation journal');
});
test('a cookie retry preserves the original protected attempt and deadline',async()=>{
  const f=fixture();f.failCookie=true;const boot=f.boot({request});await boot.start();assert.equal(boot.getSnapshot().phase,'error');
  const first=f.saved;assert.ok(first);f.failCookie=false;await boot.start();assert.equal(boot.getSnapshot().phase,'ready');
  assert.equal(f.saved?.attemptId,first.attemptId);assert.equal(f.saved?.deadline,first.deadline);assert.equal(f.calls.filter(x=>x==='owner-write').length,1);boot.dispose();
});
test('malformed new capture clears an older owner attempt before any cookie work',async()=>{
  const f=fixture(),first=f.boot({request});await first.start();first.dispose();f.calls.length=0;
  const boot=f.boot({});await boot.start();assert.equal(boot.getSnapshot().invalidOwner,true);assert.equal(f.saved,undefined);assert.equal(f.calls.includes('cookie'),false);boot.dispose();
});
test('unavailable protected storage refuses before reads, cookie work or ordinary storage',async()=>{
  const f=fixture();f.store.assertAvailable=()=>{throw Error('secure context unavailable');};const boot=f.boot({request});await boot.start();
  assert.equal(boot.getSnapshot().invalidOwner,true);assert.deepEqual(f.calls,[]);boot.dispose();
});
test('expired stored owner state cannot restore account effects or refresh its local deadline',async()=>{
  const f=fixture(),first=f.boot({request});await first.start();first.dispose();f.saved={...f.saved!,deadline:Date.now()-1};f.calls.length=0;
  const boot=f.boot();await boot.start();assert.equal(boot.getSnapshot().invalidOwner,true);assert.equal(f.saved,undefined);assert.equal(f.calls.includes('cookie'),false);boot.dispose();
});
test('disposal during protected read prevents cookie work and late publication',async()=>{
  const f=fixture();let resolve!:(value:undefined)=>void;f.store.read=()=>new Promise(done=>{resolve=done;});const boot=f.boot(),flight=boot.start();
  const before=boot.getSnapshot();boot.dispose();resolve(undefined);await flight;assert.equal(boot.getSnapshot(),before);assert.equal(f.calls.includes('cookie'),false);
});
