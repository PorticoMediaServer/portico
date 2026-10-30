import test from 'node:test';
import assert from 'node:assert/strict';
import {getEventListeners} from 'node:events';
import {playCatalogueQuerySelection,enqueueCatalogueQuerySelection} from '../src/catalogue-query-selection.ts';
import {saveQueueAsPlaylist} from '../src/playback-v1/queue.ts';
import {AccountNotificationsClient,runAccountExport} from '../src/account-notifications.ts';

const tick=()=>new Promise<void>(resolve=>setImmediate(resolve));
function withoutDOMException(t:import('node:test').TestContext) {
  const original=Object.getOwnPropertyDescriptor(globalThis,'DOMException');
  Object.defineProperty(globalThis,'DOMException',{configurable:true,writable:true,value:undefined});
  t.after(()=>{if(original)Object.defineProperty(globalThis,'DOMException',original);else Reflect.deleteProperty(globalThis,'DOMException');});
}

test('retired catalogue selections reject portably without global DOMException',async t=>{
 withoutDOMException(t);let calls=0;const lifetime={signal:new AbortController().signal,isCurrent:()=>false};
 await assert.rejects(playCatalogueQuerySelection({playSegments:async()=>{calls++;}} as never,{} as never,lifetime),{name:'AbortError'});
 await assert.rejects(enqueueCatalogueQuerySelection({enqueueSegments:async()=>{calls++;}} as never,{} as never,'end',lifetime),{name:'AbortError'});
 assert.equal(calls,0);
});

test('queue-save polling abort settles and removes its timer/listener without global DOMException',async t=>{
 withoutDOMException(t);t.mock.timers.enable({apis:['setTimeout']});let calls=0;
 const signal=new AbortController(),client={saveAsPlaylist:async()=>{calls++;return{playlistId:'pl1',revision:'1',entries:1,state:'saving' as const};}};
 const pending=saveQueueAsPlaylist(client,'q','name',{key:'key',signal:signal.signal}).then(()=>undefined,e=>e);
 await tick();assert.equal(getEventListeners(signal.signal,'abort').length,1);signal.abort();
 assert.equal((await pending)?.name,'AbortError');assert.equal(getEventListeners(signal.signal,'abort').length,0);
 t.mock.timers.tick(10000);await tick();assert.equal(calls,1);
 // Cancellation can also happen in onProgress before the wait listener is installed.
 const early=new AbortController();
 await assert.rejects(saveQueueAsPlaylist(client,'q','name',{key:'key',signal:early.signal,onProgress:()=>early.abort()}),{name:'AbortError'});
 assert.equal(getEventListeners(early.signal,'abort').length,0);
});

test('account export cancellation checks and polling sleep work without global DOMException',async t=>{
 withoutDOMException(t);t.mock.timers.enable({apis:['setTimeout']});let calls=0;
 const client=new AccountNotificationsClient(async<T>()=>{calls++;return{id:'export_1',state:'pending',expiresAt:'2099-01-01T00:00:00Z'} as T;});
 const stopped=new AbortController();stopped.abort();
 await assert.rejects(runAccountExport(client,'operation_0123456789abcdef',{signal:stopped.signal}),{name:'AbortError'});assert.equal(calls,0);
 const waiting=new AbortController(),pending=runAccountExport(client,'operation_0123456789abcdef',{signal:waiting.signal}).then(()=>undefined,e=>e);
 await tick();assert.equal(calls,1);assert.equal(getEventListeners(waiting.signal,'abort').length,1);
 waiting.abort();assert.equal((await pending)?.name,'AbortError');assert.equal(getEventListeners(waiting.signal,'abort').length,0);
 t.mock.timers.tick(60000);await tick();assert.equal(calls,1);
});
