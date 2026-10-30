import test from 'node:test';
import assert from 'node:assert/strict';
import {ServerOperationsService,parseOperationPanel} from '../src/server-operations.ts';
const scope={serverId:'server',viewerId:'owner'} as any;
const panel={name:'memory',observedAt:'2026-09-05T12:00:00Z',freshUntil:'2026-09-05T12:00:30Z',memory:{heapObjectsBytes:1024,runtimeReservedBytes:4096}};
const response=(value=panel,serverId='server',viewerFence='a'.repeat(64))=>({scope:{serverId,viewerFence},panel:value});
const fake=(fn:()=>Promise<unknown>)=>({requestBounded:fn}) as any;
test('strict measurement projection rejects invented health and malformed observations',()=>{
 assert.equal(parseOperationPanel(panel,'memory').memory?.heapObjectsBytes,1024);
 for(const bad of [{...panel,memory:{heapObjectsBytes:-1,runtimeReservedBytes:4096}},{...panel,memory:{heapObjectsBytes:5000,runtimeReservedBytes:4096}},{...panel,freshUntil:panel.observedAt},{...panel,name:'database'},{...panel,memory:undefined,database:{readable:true}}])assert.throws(()=>parseOperationPanel(bad,'memory'));
});
test('a panel reads only what was asked for: a field it does not know is ignored and never shown',()=>{
 assert.deepEqual(parseOperationPanel({...panel,database:{readable:true},futureField:{health:'great'}},'memory'),panel);
 const times={observedAt:panel.observedAt,freshUntil:panel.freshUntil};
 assert.deepEqual(parseOperationPanel({name:'database',...times,database:{readable:true,latencyMs:3},memory:panel.memory},'database'),{name:'database',...times,database:{readable:true}});
 assert.throws(()=>parseOperationPanel({name:'database',...times,database:{readable:false}},'database'));
});
test('a build detail that cannot be read is shown as not recorded; the platform it was built for is still required',()=>{
 const times={observedAt:panel.observedAt,freshUntil:panel.freshUntil};
 const build={version:'1.2.0',buildId:'b1',sourceDigest:'abc',builtAt:'2026-09-01T00:00:00Z',targetOS:'linux',targetArch:'amd64',goVersion:'go1.25'};
 assert.deepEqual(parseOperationPanel({name:'build',...times,build:{...build,version:'1.2.0 (nightly build!)',builtAt:'last week',channel:'beta'}},'build').build,{...build,version:null,builtAt:null});
 assert.throws(()=>parseOperationPanel({name:'build',...times,build:{...build,targetOS:''}},'build'));
 assert.throws(()=>parseOperationPanel({name:'build',...times},'build'));
});
test('failed panel refresh retains old observation without marking it current',async()=>{
 let fails=false;const service=new ServerOperationsService({scope,api:fake(async()=>{if(fails)throw new Error('PRIVATE');return response()})});
 await service.refresh('memory');assert.equal(service.getSnapshot().memory.phase,'ready');fails=true;await service.refresh('memory');assert.equal(service.getSnapshot().memory.phase,'error');assert.equal(service.getSnapshot().memory.panel?.memory?.heapObjectsBytes,1024);assert.ok(!service.getSnapshot().memory.error?.includes('PRIVATE'));service.dispose();
});
test('revocation clears every panel and prevents late sibling completion',async()=>{
 let finish!:(v:unknown)=>void;const service=new ServerOperationsService({scope,api:{requestBounded:async(path:string)=>{if(path.endsWith('build'))throw Object.assign(new Error('denied'),{status:401});return await new Promise(resolve=>finish=resolve)}} as any});
 const pending=service.refresh('memory');await service.refresh('build');finish(response());await pending;assert.equal(service.getSnapshot().memory.panel,null);assert.equal(service.getSnapshot().memory.phase,'access-denied');service.dispose();
});
test('wrong server, changed viewer fence and disposed server completion are discarded',async()=>{
 let value=response();const service=new ServerOperationsService({scope,api:fake(async()=>value)});await service.refresh('memory');value=response(panel,'server','b'.repeat(64));await service.refresh('memory');assert.equal(service.getSnapshot().memory.panel,null);service.dispose();
 const wrong=new ServerOperationsService({scope,api:fake(async()=>response(panel,'other'))});await wrong.refresh('memory');assert.equal(wrong.getSnapshot().memory.panel,null);wrong.dispose();
 let finish!:(v:unknown)=>void;const old=new ServerOperationsService({scope,api:fake(()=>new Promise(resolve=>finish=resolve))});const pending=old.refresh('memory');old.dispose();finish(response());await pending;assert.equal(old.getSnapshot().memory.panel,null);
});
test('deadline bounds uncooperative transport and preserves healthy sibling panel',async()=>{
 const service=new ServerOperationsService({scope,timeoutMs:10,api:{requestBounded:async(path:string)=>path.endsWith('memory')?response():new Promise(()=>{})} as any});await service.refresh('memory');await service.refresh('database');assert.equal(service.getSnapshot().database.phase,'error');assert.equal(service.getSnapshot().memory.phase,'ready');service.dispose();
});
