import assert from 'node:assert/strict';
import test from 'node:test';
import {SettingsWebhooksSource} from '../src/server-admin/webhooks-source.ts';
import type {WebhookTransport} from '../src/api-access.ts';
const hook = {id:'h',name:'Receiver',url:'https://receiver.invalid/events',events:['title.added'],enabled:false,waiting:0,dropped:0,createdAt:'2026-11-16T12:00:00Z'};
const draft = {name:hook.name,url:hook.url,events:hook.events};
function fixture() {
  let alive = true, lost = false;
  const calls: Array<{path:string;method?:string;body?:unknown;headers?:unknown}> = [];
  const api: WebhookTransport = {
    request: async <T>(path:string,method?:string,body?:unknown) => {
      calls.push({path,method,body});
      if (method === 'PATCH') return {...hook,...body as object} as T;
      if (path.endsWith('/secret')) return {webhook:hook,secret:'test-only-secret'} as T;
      if (path.endsWith('/test')) return {delivered:false,status:503,error:'timed_out'} as T;
      if (path.includes('/deliveries')) return {items:[{event:'test',state:'gave_up',tries:3,createdAt:hook.createdAt}]} as T;
      return {items:[hook]} as T;
    },
    requestRaw: async (path,method,options) => {calls.push({path,method,body:options?.body,headers:options?.headers});if(lost)throw new Error('lost answer');return {status:201,headers:{},body:{webhook:{...hook,enabled:true},secret:'test-only-secret'}};},
  };
  const source = new SettingsWebhooksSource(api,()=>alive,()=>{});
  return {source,calls,lose:()=>{lost=true;},recover:()=>{lost=false;},retire:()=>{alive=false;source.dispose();}};
}
test('a lost webhook create reuses the header key and keeps secrets out of snapshots',async()=>{
  const f=fixture();await f.source.load();f.lose();
  assert.equal(await f.source.create(draft),undefined);
  assert.equal(f.source.getSnapshot().uncertain,true);
  await f.source.reconcile();f.recover();
  assert.equal((await f.source.create(draft))?.secret,'test-only-secret');
  const creates=f.calls.filter(call=>call.method==='POST');
  assert.equal(creates.length,2);assert.deepEqual(creates[0].headers,creates[1].headers);
  assert.ok((creates[0].headers as Record<string,string>)['Idempotency-Key']);
  assert.deepEqual(creates[0].body,draft);
  assert.ok(!JSON.stringify(f.source.getSnapshot()).includes('test-only-secret'));
  f.retire();assert.equal(await f.source.create(draft),undefined);assert.equal(f.calls.filter(call=>call.method==='POST').length,2);
});
test('disabled transferred hooks enable directly; edits, rotation, test and log use current receipts',async()=>{
  const f=fixture();await f.source.load();
  let row=f.source.getSnapshot().list!.items[0];
  assert.equal((await f.source.update(row,{enabled:true}))?.enabled,true);
  assert.deepEqual(f.calls.find(call=>call.method==='PATCH')?.body,{enabled:true});
  // An old list row cannot issue another mutation after reload.
  const count=f.calls.length;assert.equal(await f.source.rotate(row),undefined);assert.equal(f.calls.length,count);
  row=f.source.getSnapshot().list!.items[0];assert.equal((await f.source.rotate(row))?.secret,'test-only-secret');
  row=f.source.getSnapshot().list!.items[0];assert.deepEqual(await f.source.test(row),{delivered:false,status:503,error:'timed_out'});
  row=f.source.getSnapshot().list!.items[0];await f.source.select(row,'next');
  assert.equal(f.source.getSnapshot().deliveries?.items[0].tries,3);
  assert.ok(f.calls.at(-1)?.path.endsWith('/deliveries?cursor=next'));
  f.retire();
});
