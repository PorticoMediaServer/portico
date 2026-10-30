import test,{type TestContext} from 'node:test';
import assert from 'node:assert/strict';
import {SubtitleService} from '../src/subtitles.ts';
import {serviceI18n,setServiceI18n} from '../src/presentation/service-text.ts';

const catalog={version:1,itemId:'item',revision:1,canShare:false,sources:[{id:'source',durationUs:'100000000',inventoryRevision:1,available:true,timingKnown:true}],resources:[],discovered:[],provider:{id:'opensubtitles',enabled:false,reason:'provider_not_configured'}};
const plan={version:1,sessionId:'session',generation:1,sourceId:'source',revision:1,catalogRevision:1,renderer:'external_text',mode:'off',offAvailable:true,offsetUs:'0',selected:null,resources:[],discovered:[]};
const settle=async()=>{for(let i=0;i<30;i++)await Promise.resolve();};
const target={itemId:'item',sessionId:'session',generation:1};

// Exercise the service's public catalogue port, independently of translated copy.
function messages(t:TestContext){
 const previous=serviceI18n();setServiceI18n({...previous,t:id=>id,has:()=>true});t.after(()=>setServiceI18n(previous));
}

test('RN-05: the initial subtitle plan retries with polling disabled and recovers the menu',async t=>{
 t.mock.timers.enable({apis:['setTimeout','setInterval']});messages(t);let plans=0;
 const service=new SubtitleService({request:async<T,>(path:string)=>{if(path.includes('/playback/')){if(++plans===1)throw Object.assign(new Error('offline'),{status:503});return plan as T;}return catalog as T;}},target,()=> 'operation');
 service.setPolling(false);
 try{
  service.start();await settle();assert.equal(plans,1);assert.equal(service.getSnapshot().plan,null);assert.equal(service.getSnapshot().canRetry,true);
  t.mock.timers.tick(499);await settle();assert.equal(plans,1);
  t.mock.timers.tick(1);await settle();assert.equal(plans,2);assert.equal(service.getSnapshot().plan?.sessionId,'session');assert.equal(service.getSnapshot().suppressed,false);assert.equal(service.getSnapshot().error,null);
  t.mock.timers.tick(60000);await settle();assert.equal(plans,2,'recovery never enables periodic catalogue polling');
 }finally{service.stop();}
});

test('RN-05: initial recovery is bounded to three attempts; an explicit recovery starts a new burst',async t=>{
 t.mock.timers.enable({apis:['setTimeout','setInterval']});messages(t);let plans=0,offline=true;
 const service=new SubtitleService({request:async<T,>(path:string)=>{if(path.includes('/playback/')){plans++;if(offline)throw Object.assign(new Error('offline'),{status:503});return plan as T;}return catalog as T;}},target,()=> 'operation');service.setPolling(false);
 try{
  service.start();await settle();t.mock.timers.tick(500);await settle();t.mock.timers.tick(1000);await settle();assert.equal(plans,3);
  t.mock.timers.tick(60000);await settle();assert.equal(plans,3);
  offline=false;await service.refresh();assert.equal(plans,4);assert.ok(service.getSnapshot().plan);
 }finally{service.stop();}
});

test('RN-05: denied scopes never retry and retirement cancels scheduled recovery',async t=>{
 t.mock.timers.enable({apis:['setTimeout','setInterval']});messages(t);
 for(const status of [403,503]){
  let plans=0;
  const service=new SubtitleService({request:async<T,>(path:string)=>{if(path.includes('/playback/')){plans++;throw Object.assign(new Error('failed'),{status});}return catalog as T;}},target,()=> 'operation');service.setPolling(false);
  try{
   service.start();await settle();if(status===503)service.stop();
   t.mock.timers.tick(60000);await settle();assert.equal(plans,1);assert.equal(service.getSnapshot().plan,null);
   if(status===403)assert.equal(service.getSnapshot().canRetry,false);
  }finally{service.stop();}
 }
});

test('RN-05: concurrent refreshes share reads and late responses cannot publish after stop',async t=>{
 t.mock.timers.enable({apis:['setTimeout','setInterval']});messages(t);let resolve!:(value:unknown)=>void,calls=0;
 const pending=new Promise(done=>{resolve=done;});
 const service=new SubtitleService({request:<T,>(path:string)=>{calls++;return (path.includes('/playback/')?pending:Promise.resolve(catalog)) as Promise<T>;}},target,()=> 'operation');service.setPolling(false);
 service.start();const first=service.refresh(),second=service.refresh();assert.equal(first,second);await settle();assert.equal(calls,2);
 service.stop();resolve(plan);await first;assert.equal(service.getSnapshot().plan,null);assert.equal(service.getSnapshot().catalog,null);t.mock.timers.tick(60000);await settle();assert.equal(calls,2);
});

test('RN-05: explicit final or recovery-first error policies never automatically retry',async t=>{
 t.mock.timers.enable({apis:['setTimeout','setInterval']});messages(t);
 for(const policy of [{retryable:false},{retry:'never'},{retry:'after_refresh'},{retry:'after_reauth'}]){
  let plans=0;
  const service=new SubtitleService({request:async<T,>(path:string)=>{if(path.includes('/playback/')){plans++;throw Object.assign(new Error('final'),{status:503,...policy});}return catalog as T;}},target,()=> 'operation');service.setPolling(false);
  try{service.start();await settle();assert.equal(plans,1);t.mock.timers.tick(60000);await settle();assert.equal(plans,1,'explicit final answer must not automatically retry');}finally{service.stop();}
 }
});

test('RN-05: server retry floors in seconds or milliseconds postpone automatic recovery',async t=>{
 t.mock.timers.enable({apis:['setTimeout','setInterval']});messages(t);
 for(const directed of [{retryAfterSeconds:5},{retryAfterMs:5000}]){
  let plans=0;
  const service=new SubtitleService({request:async<T,>(path:string)=>{if(path.includes('/playback/')){plans++;throw Object.assign(new Error('wait'),{status:429,retryable:true,...directed});}return catalog as T;}},target,()=> 'operation');service.setPolling(false);
  try{service.start();await settle();assert.equal(plans,1);t.mock.timers.tick(4999);await settle();assert.equal(plans,1);t.mock.timers.tick(1);await settle();assert.equal(plans,2);}finally{service.stop();}
 }
});

test('RN-05: a long retry floor is chunked without overflowing or bypassing retirement',async t=>{
 t.mock.timers.enable({apis:['setTimeout','setInterval']});messages(t);let plans=0;
 const service=new SubtitleService({request:async<T,>(path:string)=>{if(path.includes('/playback/')){plans++;throw Object.assign(new Error('wait'),{status:429,retryAfterMs:2147484647});}return catalog as T;}},target,()=> 'operation');service.setPolling(false);
 try{service.start();await settle();t.mock.timers.tick(2147483647);await settle();assert.equal(plans,1);service.stop();t.mock.timers.tick(1000);await settle();assert.equal(plans,1);}finally{service.stop();}
});
