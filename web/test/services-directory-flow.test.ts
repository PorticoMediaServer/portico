import test from 'node:test';
import assert from 'node:assert/strict';
import {ServicesDirectoryFlow, type DirectoryItem} from '../src/services/directory-flow.ts';
import {accountDirectoryFailure, directoryFailurePhase} from '../src/services/directory-errors.ts';
import {RouteError} from '@core/route-identity.ts';
import {ApiError} from '@core/index.ts';

const server: DirectoryItem={id:'srv_'+'a'.repeat(43),name:'Original server',baseUrl:'https://server.example',publicKey:'p'.repeat(43),policyRevision:1,secureConnection:'ready',endpointVerified:true,lastSeenAt:'2026-10-25T12:00:00Z',presence:{lastSeenAt:'2026-10-25T12:00:00Z'},ownedByMe:false,role:'member',status:'active'};
const other: DirectoryItem={...server,id:'srv_'+'b'.repeat(43),name:'Other server',baseUrl:'https://other.example',publicKey:'o'.repeat(43)};
const directory=(items: DirectoryItem[])=>({items,version:'1',watch:'1',membershipEnding:undefined});
const deferred=<T,>()=>{let resolve!:(value:T)=>void;const promise=new Promise<T>(done=>{resolve=done;});return{promise,resolve};};
type Admission={id:string;dispose:()=>void};
function fixture(items=[server],autoOpen=false){
 let current=true,reads=0,now=1000,disposed=0;
 const admissions:string[]=[];
 let read=async()=>directory(items),admit=async(chosen:DirectoryItem):Promise<Admission>=>({id:chosen.id,dispose:()=>{disposed++;}});
 const flow=new ServicesDirectoryFlow<Admission>(directory(items),{current:()=>current,read:()=>{reads++;return read();},admit:chosen=>{admissions.push(chosen.id);return admit(chosen);},empty:()=>{},now:()=>now},autoOpen);
 return{flow,admissions,now:(value:number)=>{now=value;},get reads(){return reads;},get disposed(){return disposed;},retire:()=>{current=false;},read:(value:typeof read)=>{read=value;},admit:(value:typeof admit)=>{admit=value;}};
}
test('opening proves the exact fresh server and admits it in place, never by navigation',async()=>{
 const f=fixture();await f.flow.open(server,f.flow.getSnapshot());
 const state=f.flow.getSnapshot();assert.equal(state.phase,'opened');assert.equal(state.admitted?.id,server.id);assert.deepEqual(f.admissions,[server.id]);f.flow.dispose();
});
test('retired original account during directory read cannot admit',async()=>{
 const f=fixture(),gate=deferred<ReturnType<typeof directory>>();f.read(()=>gate.promise);
 const pending=f.flow.open(server,f.flow.getSnapshot());f.retire();gate.resolve(directory([server]));await pending;
 assert.deepEqual(f.admissions,[]);f.flow.dispose();
});
test('changed pin or ambiguous identity refuses rather than adopting current directory successor',async()=>{
 for(const items of [[{...server,publicKey:'q'.repeat(43)}],[server,server]]){
  const f=fixture();f.read(async()=>directory(items));await f.flow.open(server,f.flow.getSnapshot());assert.deepEqual(f.admissions,[]);assert.equal((f.flow.getSnapshot().error as {code:string}).code,'identity_mismatch');assert.equal(f.flow.getSnapshot().waitingId,undefined);f.flow.dispose();
 }
});
test('a late admission after disposal is released and never published',async()=>{
 const f=fixture(),gate=deferred<Admission>();let released=0;f.admit(()=>gate.promise);
 const pending=f.flow.open(server,f.flow.getSnapshot());await new Promise(resolve=>setImmediate(resolve));f.flow.dispose();
 gate.resolve({id:server.id,dispose:()=>{released++;}});await pending;assert.equal(released,1);assert.notEqual(f.flow.getSnapshot().phase,'opened');
});
test('old rendered actions cannot start after a fresh directory publication',async()=>{
 const f=fixture(),old=f.flow.getSnapshot();await f.flow.refresh(old);await f.flow.open(server,old);assert.deepEqual(f.admissions,[]);f.flow.dispose();
});
// H01/H11 (28 Oct): an idle tab must never wander to another server or to a server's sign-in page.
test('H01/H11 idle tab: waiting retries only the chosen server and lands it in place',async()=>{
 const f=fixture([server,other]);let down=true;
 f.admit(async chosen=>{if(down)throw new RouteError('route_unavailable','The server did not answer.');return {id:chosen.id,dispose:()=>{}};});
 await f.flow.open(server,f.flow.getSnapshot());
 let state=f.flow.getSnapshot();assert.equal(state.waitingId,server.id);assert.equal(state.status,'unreachable');
 for(let i=0;i<4;i++){f.flow.automatic();await new Promise(resolve=>setImmediate(resolve));await new Promise(resolve=>setImmediate(resolve));}
 assert.ok(f.admissions.length>=2);assert.ok(f.admissions.every(id=>id===server.id),'only the chosen server is retried');
 assert.equal(f.flow.getSnapshot().status,'waiting');
 down=false;f.flow.automatic();await new Promise(resolve=>setImmediate(resolve));await new Promise(resolve=>setImmediate(resolve));
 state=f.flow.getSnapshot();assert.equal(state.phase,'opened');assert.equal(state.admitted?.id,server.id);f.flow.dispose();
});
test('nothing waits without a chosen server, and a person choosing is never taken anywhere unasked',async()=>{
 const f=fixture([server]);f.flow.automatic();f.flow.automaticInitial();await new Promise(resolve=>setImmediate(resolve));assert.deepEqual(f.admissions,[]);f.flow.dispose();
 const first=fixture([server],true);first.flow.automaticInitial();await new Promise(resolve=>setImmediate(resolve));await new Promise(resolve=>setImmediate(resolve));
 assert.deepEqual(first.admissions,[server.id]);first.flow.dispose();
 const two=fixture([server,other],true);two.flow.automaticInitial();await new Promise(resolve=>setImmediate(resolve));assert.deepEqual(two.admissions,[]);two.flow.dispose();
});
test('a refusal, a changed identity or a server needing an update is not retried',async()=>{
 for(const failure of [new ApiError(403,'access_refused','access_refused',false),new RouteError('api_level_mismatch','update'),new RouteError('clocks_disagree','clock')]){
  const f=fixture();f.admit(async()=>{throw failure;});await f.flow.open(server,f.flow.getSnapshot());
  assert.equal(f.flow.getSnapshot().waitingId,undefined);const count=f.admissions.length;f.flow.automatic();await new Promise(resolve=>setImmediate(resolve));
  assert.equal(f.admissions.length,count);f.flow.dispose();
 }
 assert.equal(directoryFailurePhase(new RouteError('api_level_mismatch','update'),false),'update');
});
test('cancelling an admitted opening releases it and returns to the list',async()=>{
 const f=fixture();await f.flow.open(server,f.flow.getSnapshot());f.flow.cancelOpening(f.flow.getSnapshot());
 assert.equal(f.disposed,1);assert.equal(f.flow.getSnapshot().phase,'idle');assert.equal(f.flow.getSnapshot().admitted,undefined);f.flow.dispose();
});
test('a removed or left server disappears at once and the account is read again',async()=>{
 const f=fixture([server,other]);f.read(async()=>directory([other]));f.flow.forget(server.id,f.flow.getSnapshot());
 assert.deepEqual(f.flow.getSnapshot().items.map(item=>item.id),[other.id]);await new Promise(resolve=>setImmediate(resolve));assert.equal(f.reads,1);f.flow.dispose();
});
test('automatic account retry respects Retry-After floor and only source-known failures classify as account outage',async()=>{
 const f=fixture();f.read(async()=>{throw new ApiError(429,'rate_limited','rate_limited',true,700);});await f.flow.open(server,f.flow.getSnapshot(),true);
 assert.equal(f.flow.retryDelay(),700);assert.equal(f.flow.getSnapshot().seconds,700);assert.equal(f.flow.getSnapshot().status,'account-waiting');
 assert.equal(directoryFailurePhase(new RouteError('route_unavailable','server'),false),'unreachable');
 assert.equal(directoryFailurePhase(accountDirectoryFailure(new TypeError('network')),false),'account-waiting');
 assert.equal(directoryFailurePhase(accountDirectoryFailure(new ApiError(403,'forbidden','forbidden',false)),false),undefined);f.flow.dispose();
});
test('the connecting grace lasts40 seconds from this opening and refresh never restarts it',async()=>{
 const f=fixture();assert.equal(f.flow.inConnectingGrace(),true);
 f.now(40999);assert.equal(f.flow.inConnectingGrace(),true);
 f.now(41000);assert.equal(f.flow.inConnectingGrace(),false);
 await f.flow.refresh(f.flow.getSnapshot());assert.equal(f.flow.inConnectingGrace(),false);
 f.now(90000);assert.equal(f.flow.inConnectingGrace(),false);f.flow.dispose();
});
