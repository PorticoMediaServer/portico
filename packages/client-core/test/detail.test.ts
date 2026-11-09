import {wireFixture} from './wire-fixture.ts';
import test from 'node:test';
import assert from 'node:assert/strict';
import {HttpLocalApi, type LocalSession, type OutboxEntry} from '../src/index.ts';
import { DetailService, parseCreditPage, type DetailApi, type PersonalState } from '../src/detail.ts';
import {withQueries} from './fixtures/viewer-queries.ts';
const scope = { serverId: 'server', viewerId: JSON.stringify(['local','account','profile']) };
const target = { libraryId: 'library', itemId: 'item' };
const personal = (revision = 0): PersonalState => ({ watchlisted: false, favorite: false, rating: null, revision,watched:false,progressSeconds:0,lastPlayedAt:'',status:'current',conflicts:[] });
function data(itemId = 'item', state = personal()): any {
  return { scope: { serverId: 'server', libraryId: 'library', itemId, viewerFence: 'fence' }, revision: { catalog: 1, viewer: state.revision }, item: { id: itemId, libraryId: 'library', title: 'Server title', kind: 'movie', duration: 100, progressSeconds: 30, available: true, overview: 'Full server description', sources: [{ id: 'source', container: 'mp4', videoCodec: 'h264', audioCodec: 'aac', width: 1920, height: 1080 }] }, personal: state,
    actions: [{ id: 'watchlist', labelKey: 'action.watchlist', enabled: true }, { id: 'favorite', labelKey: 'action.favorite', enabled: true }, { id: 'rating', labelKey: 'action.rating', enabled: true, min: 0, max: 10, step: 1 }, { id: 'resume', labelKey: 'action.resume', enabled: true, playback: { itemId, startSeconds: 30 } }, { id: 'start_over', labelKey: 'action.start_over', enabled: true, playback: { itemId, startSeconds: 0 } }],
    metadata: { status: 'available', ratings: [{ provider: 'tmdb', value: 7.8, max: 10, votes: 20, sourceUrl: 'https://www.themoviedb.org/movie/123', observedAt: '2026-09-04T00:00:00Z' }], genres: [{ id: '18', name: 'Drama', provider: 'tmdb' }], credits: [{ id: '1', name: 'Actor', role: 'Role', department: 'Acting', provider: 'tmdb' }] } };
}
function receipt(body: any, state: PersonalState, itemId = 'item'): any { return { operationId: body.operationId, serverId: 'server', libraryId: 'library', itemId, viewerFence: 'fence', personal: state }; }
function deferred<T>() { let resolve!: (v: T) => void; const promise = new Promise<T>(r => { resolve = r; }); return { promise, resolve }; }
const tick = () => new Promise(resolve => setTimeout(resolve, 0));
function setup(handler: (path: string, method?: string, body?: any, signal?: AbortSignal) => Promise<any>, timeoutMs = 1000) {
  let ids = 0;
  const api: DetailApi = { request: <T>(p: string, m?: string, b?: unknown, s?: AbortSignal) => handler(p,m,b,s).then(v=>wireFixture(v,{'duration': 'durationMs', 'progressSeconds': 'progressMs', 'startSeconds': 'startMs', 'positionSeconds': 'positionMs'})) as Promise<T> };
  return new DetailService(withQueries({ api, scope, timeoutMs, requestId: async () => `12345678-1234-4123-8123-${String(++ids).padStart(12,'0')}` }));
}

test('detail preserves authoritative content, attribution, personal values and exact playback actions', async () => {
  const service = setup(async () => data()); await service.select(target);
  const state = service.getSnapshot(); assert.equal(state.phase, 'ready');
  assert.deepEqual(state.data!.personal, personal());
  assert.deepEqual(state.data!.metadata, data().metadata);
  assert.deepEqual(state.data!.actions.find(a => a.id === 'resume')!.playback, { itemId: 'item', startSeconds: 30 });
  assert.deepEqual(state.data!.actions.find(a => a.id === 'start_over')!.playback, { itemId: 'item', startSeconds: 0 });
  assert.throws(() => { (state.data!.metadata.credits as any).push({}); }, TypeError);
});

test('item change cancels and fences late detail even if request ignores abort', async () => {
  const old = deferred<any>(); let signal: AbortSignal | undefined;
  const service = setup(async (p,_m,_b,s) => { if (p.includes('/item/')) { signal = s; return old.promise; } return data('new-item'); });
  const pending = service.select(target); await service.select({ ...target, itemId: 'new-item' }); await pending;
  assert.equal(signal!.aborted, true); old.resolve(data()); await tick();
  assert.equal(service.getSnapshot().data!.scope.itemId, 'new-item');
});

test('adjacent duplicate mutation reuses one operation while alternating intents retain order', async () => {
  let state = personal(); const writes: any[] = []; const first = deferred<any>();
  const service = setup(async (_p,m,b) => { if (m === 'GET') return data('item',state); writes.push(b); if (writes.length === 1) return first.promise; assert.equal(b.expectedRevision,state.revision); state = { ...state, favorite: b.favorite, revision: state.revision+1 }; return receipt(b,state); });
  await service.select(target);
  const one = service.mutate({ action: 'favorite', value: true });
  const duplicate = service.mutate({ action: 'favorite', value: true }); assert.equal(one,duplicate);
  const two = service.mutate({ action: 'favorite', value: false });
  const three = service.mutate({ action: 'favorite', value: true }); await tick();
  assert.equal(writes.length,1); assert.equal(service.getSnapshot().data!.personal.favorite,false);
  state = { ...state, favorite: true, revision: 1 }; first.resolve(receipt(writes[0],state));
  await Promise.all([one,two,three]);
  assert.deepEqual(writes.map(w => w.favorite),[true,false,true]);
  assert.deepEqual(writes.map(w => w.expectedRevision),[0,1,2]);
  assert.equal(service.getSnapshot().data!.personal.revision,3);
  assert.equal(service.getSnapshot().pending.length,0);
});

test('timed-out mutation retries exact operation and expected revision, never a new ambiguous set', async () => {
  const writes: any[] = []; const service = setup(async (_p,m,b) => { if (m==='GET') return data(); writes.push(b); if (writes.length===1) return new Promise(()=>{}); return receipt(b,{...personal(1),watchlisted:true}); },5);
  await service.select(target); await service.mutate({action:'watchlist',value:true});
  assert.equal(service.getSnapshot().pending[0].phase,'retry-required');
  assert.equal(service.getSnapshot().mutationError!.code,'timeout');
  await service.retryMutation(); assert.deepEqual(writes[0],writes[1]);
  assert.equal(service.getSnapshot().data!.personal.watchlisted,true);
});

test('late receipt with older personal revision cannot roll back a newer authoritative read', async () => {
  const mutation = deferred<any>(); let body: any, current = personal();
  const service = setup(async (_p,m,b) => { if(m==='GET') return data('item',current); body=b; return mutation.promise; });
  await service.select(target); const pending=service.mutate({action:'favorite',value:true}); await tick();
  current={...personal(3),favorite:false,rating:4}; await service.refresh();
  mutation.resolve(receipt(body,{...personal(1),favorite:true})); await pending;
  assert.deepEqual(service.getSnapshot().data!.personal,current);
});

test('late pre-mutation read cannot roll back confirmed personal state', async () => {
  const stale = deferred<any>(); let reads=0;
  const service=setup(async (_p,m,b)=>{ if(m==='GET'){reads++;return reads===1?data():stale.promise;} return receipt(b,{...personal(1),favorite:true}); });
  await service.select(target); const read=service.refresh(); await service.mutate({action:'favorite',value:true});
  stale.resolve(data()); await read;
  assert.equal(service.getSnapshot().data!.personal.favorite,true); assert.equal(service.getSnapshot().data!.personal.revision,1);
});

test('personal conflict retains the original draft and revision until explicit review', async () => {
  let current=personal(); const writes:any[]=[],acks:string[]=[];
  const service=setup(async (_p,m,b)=>{ if(m==='GET')return data('item',current); writes.push(structuredClone(b)); current={...personal(5),watchlisted:true};throw Object.assign(new Error('Personal state changed'),{code:'personal_state_conflict',retryable:true}); },1000,async key=>{acks.push(key);});
  await service.select(target); await service.mutate({action:'favorite',value:true}); await tick();
  assert.equal(writes.length,1); assert.equal(writes[0].expectedRevision,0);
  assert.equal(service.getSnapshot().mutationError!.code,'personal_state_conflict');
  assert.deepEqual(service.getSnapshot().pending,[{intent:{action:'favorite',value:true},phase:'conflict'}]);
  assert.equal(service.getSnapshot().data!.personal.revision,5);
  assert.equal(service.getSnapshot().data!.personal.favorite,false);
  assert.throws(()=>service.retryMutation(),/review/); await tick();
  assert.equal(writes.length,1); assert.equal(writes[0].expectedRevision,0); assert.deepEqual(acks,[]);
  service.dispose();
});

test('a matching current value cannot prove that a conflicted historical action applied', async () => {
  let current=personal(); const writes:any[]=[];
  const service=setup(async (_p,m,b)=>{ if(m==='GET')return data('item',current); writes.push(b); current={...personal(5),favorite:true};throw Object.assign(new Error('Personal state changed'),{code:'personal_state_conflict',retryable:true}); });
  await service.select(target); await service.mutate({action:'favorite',value:true}); await tick();
  assert.equal(writes.length,1); assert.equal(service.getSnapshot().mutationError!.code,'personal_state_conflict');
  assert.deepEqual(service.getSnapshot().pending,[{intent:{action:'favorite',value:true},phase:'conflict'}]);
  assert.equal(service.getSnapshot().data!.personal.favorite,true); service.dispose();
});

test('leaving the title (a new viewer gets a new cache and service) prevents queued writes and late mutation publication', async () => {
  const mutation=deferred<any>();let body:any,writes=0;
  const service=setup(async (_p,m,b)=>{if(m==='GET')return data();writes++;body=b;return mutation.promise;});
  await service.select(target);const a=service.mutate({action:'favorite',value:true});const b=service.mutate({action:'rating',value:4});await tick();
  service.cancel();await Promise.all([a,b]);
  assert.equal(service.getSnapshot().data,null);assert.equal(service.getSnapshot().pending.length,0);
  mutation.resolve(receipt(body,{...personal(1),favorite:true}));await tick();assert.equal(writes,1);assert.equal(service.getSnapshot().data,null);
});

test('rating clear is explicit null and unsupported or malformed intents do not reach transport', async()=>{
 let last:any;const service=setup(async(_p,m,b)=>{if(m==='GET')return data();last=b;return receipt(b,personal(1));});await service.select(target);
 await service.mutate({action:'rating',value:null});assert.equal(last.rating,null);assert.deepEqual(Object.keys(last).sort(),['expectedRevision','operationId','rating']);
 assert.throws(()=>service.mutate({action:'rating',value:3.7}),/Invalid/);assert.throws(()=>service.mutate({action:'rating',value:11}),/Invalid/);
 assert.throws(()=>service.mutate({action:'playlist',value:true} as any),/Invalid/);
 const sparse=setup(async()=>{const d=data();d.actions=[];d.metadata={status:'unavailable',ratings:[],genres:[],credits:[]};return d;});await sparse.select(target);
 assert.throws(()=>sparse.mutate({action:'favorite',value:true}),/not currently available/);assert.deepEqual(sparse.getSnapshot().data!.metadata.credits,[]);
});

test('wrong-scope receipts, a wrong item or revision and overlarge lists fail closed',async()=>{
 for(const alter of [(d:any)=>{d.scope.itemId='other';},(d:any)=>{d.scope.viewerFence='';},(d:any)=>{d.item.id='other';},(d:any)=>{d.item.libraryId='other';},(d:any)=>{d.revision.catalog=-1;},(d:any)=>{d.personal.revision=-1;},(d:any)=>{delete d.personal;},(d:any)=>{d.metadata.credits=Array(10001).fill({});},(d:any)=>{d.actions=Array(33).fill(d.actions[0]);},(d:any)=>{d.metadata.ratings='nope';}]){
  const service=setup(async()=>{const d=data();alter(d);return d;});await service.select(target);assert.equal(service.getSnapshot().phase,'error');assert.equal(service.getSnapshot().data,null);
 }
 const service=setup(async(_p,m,b)=>m==='GET'?data():{...receipt(b,{...personal(1),favorite:true}),viewerFence:'other'});await service.select(target);await service.mutate({action:'favorite',value:true});
 assert.equal(service.getSnapshot().mutationError!.code,'invalid_detail');assert.equal(service.getSnapshot().data!.personal.favorite,false);
});

test('one part of a title page that cannot be read is left out and the page is kept',async()=>{
 const read=async(alter:(d:any)=>void)=>{const service=setup(async()=>{const d=data();alter(d);return d;});await service.select(target);assert.equal(service.getSnapshot().phase,'ready');const result=service.getSnapshot().data!;service.dispose();return result;};
 // Actions: one that plays another item, a broken one and a repeated one are not offered.
 const actions=await read(d=>{d.actions[3].playback.itemId='other';d.actions.push({id:'favorite',labelKey:'again',enabled:false},{labelKey:'no id'},'nope',{id:'share',labelKey:'action.share',min:'low',future:true});});
 assert.deepEqual(actions.actions.map(a=>a.id),['watchlist','favorite','rating','start_over','share']);
 assert.deepEqual(actions.actions.at(-1),{id:'share',labelKey:'action.share',enabled:false});
 // Credits, ratings and genres: a row that cannot be read is left out; a portrait from elsewhere is not shown.
 const metadata=await read(d=>{d.metadata.credits=[{},{id:'2',name:'Writer',provider:'tmdb',portraitUrl:'https://elsewhere.test/p.jpg',personId:7},...d.metadata.credits];d.metadata.ratings=[{provider:'tmdb',value:11,max:10},...d.metadata.ratings];d.metadata.genres=[...d.metadata.genres,{name:'No id'}];d.metadata.status='partial';d.metadata.creditTotals={cast:'many'};});
 assert.deepEqual(metadata.metadata.credits,[{id:'2',name:'Writer',role:'',department:'',provider:'tmdb'},{id:'1',name:'Actor',role:'Role',department:'Acting',provider:'tmdb'}]);
 assert.deepEqual([metadata.metadata.ratings.length,metadata.metadata.genres.length,metadata.metadata.status,metadata.metadata.creditTotals],[1,1,'available',undefined]);
 // What the page is only drawn with reads neutrally; fields this app does not know are ignored.
 const bare=await read(d=>{d.item={id:'item',libraryId:'library',kind:'hologram',year:-1,overview:42,posterUrl:'bad\nurl',future:1};d.personal={revision:4,rating:11,status:'syncing',conflicts:[{id:'c'},{id:'c1',field:'rating',baseRevision:1,choices:[{operationId:'o',deviceId:'d',authoredAt:'t',value:1},{}]}]};delete d.metadata;d.actions=undefined;d.future={a:1};d.markers='nope';});
 // P2-29: a length the page does not carry stays unknown, never a made-up zero.
 assert.deepEqual(bare.item,{id:'item',libraryId:'library',title:'',kind:'hologram',progressSeconds:0,available:false});
 assert.deepEqual(bare.personal,{watchlisted:false,favorite:false,rating:null,revision:4,watched:false,progressSeconds:0,lastPlayedAt:'',status:'current',conflicts:[{id:'c1',field:'rating',baseRevision:1,baseValue:undefined,choices:[{operationId:'o',deviceId:'d',authoredAt:'t',value:1}]}]});
 assert.deepEqual([bare.metadata,bare.actions,bare.markers,'future' in bare],[{status:'unavailable',ratings:[],genres:[],credits:[]},[],[],false]);
 // Related rows this app cannot read are left out whole.
 assert.equal((await read(d=>{d.related={version:2};})).related,undefined);
 // Technical files no longer ride on the page; language summaries remain.
 const lazy=await read(d=>{d.files=[{id:'private-file',path:'/private/title.mkv'}];d.languages={audio:['en'],subtitles:['fr']};});
 assert.equal('files' in lazy,false);
 assert.deepEqual(lazy.languages,{audio:['en'],subtitles:['fr']});
});

test('bounded queue, cancellation and disposal settle pending commands without late publication',async()=>{
 const service=setup(async(_p,m)=>m==='GET'?data():new Promise(()=>{}),10000);await service.select(target);
 const pending=[];for(let i=0;i<8;i++)pending.push(service.mutate({action:'favorite',value:i%2===0}));
 assert.throws(()=>service.mutate({action:'rating',value:5}),/Wait/);service.cancel();await Promise.all(pending);assert.equal(service.getSnapshot().phase,'idle');
 service.dispose();assert.throws(()=>service.refresh(),/disposed/);
});

test('bounded episode/song/bookFile source metadata and unknown boundaries survive rich detail',async()=>{
 const fields={episode:{showId:'show',showTitle:'Show',seasonId:'season',seasonNumber:0,numbering:'seasonal',number:2,localIdentityStatus:'parsed',providerMatchStatus:'unmatched',orderingBasis:'unspecified',sourceBoundary:'unknown_shared_file'},song:{albumId:'album',albumTitle:'Album',albumArtist:'Various Artists',artist:'Performer',discNumber:2,trackNumber:null,providerMatchStatus:'unmatched',localMetadataIssue:'unassigned'},bookFile:{bookId:'book',bookTitle:'Book',discNumber:null,partNumber:2,sourceBoundary:'whole_file',localMetadataIssue:'unassigned'}};
 for(const [field,value]of Object.entries(fields)){
  const service=setup(async()=>{const d=data();d.item[field]=value;d.item.overview='Paragraph one.\n\nParagraph two.';d.item.sources[0].duration=100;return d;});await service.select(target);
  assert.equal(service.getSnapshot().phase,'ready');assert.deepEqual((service.getSnapshot().data!.item as any)[field],value);
  assert.equal(service.getSnapshot().data!.item.sources,undefined);assert.match(service.getSnapshot().data!.item.overview!,/\n\n/);
 }
 // Facts that cannot be read are left out whole; a fact the server stops sending reads as empty.
 const bad=setup(async()=>{const d=data();d.item.episode={...fields.episode,showTitle:'x'.repeat(2049)};return d;});await bad.select(target);assert.equal(bad.getSnapshot().phase,'ready');assert.equal(bad.getSnapshot().data!.item.episode,undefined);
 const loose=setup(async()=>{const d=data();const {orderingBasis:_gone,...rest}=fields.episode;d.item.episode={...rest,seasonNumber:'one'};return d;});await loose.select(target);const {seasonNumber:_unread,...kept}=fields.episode;assert.deepEqual(loose.getSnapshot().data!.item.episode,{...kept,orderingBasis:''});
});

test('watched and favorite actions use current reconciliation state without changing independent sets',async()=>{
 let current:PersonalState={...personal(),watchlisted:true,favorite:true,rating:4};const writes:any[]=[];
 const service=setup(async(_p,m,b)=>{if(m==='GET'){const d=data('item',current);d.actions.push({id:'watched',labelKey:'action.watched',enabled:true});return d;}writes.push(b);current={...current,revision:current.revision+1,...('watched'in b?{watched:b.watched,progressSeconds:0}:{favorite:b.favorite})};return {...receipt(b,personal()),current};});
 await service.select(target);await service.mutate({action:'watched',value:true});await tick();await service.mutate({action:'favorite',value:false});await tick();assert.equal(writes[0].watched,true);assert.equal(writes[1].favorite,false);assert.deepEqual(service.getSnapshot().data!.personal,{...personal(2),watchlisted:true,favorite:false,rating:4,watched:true});service.dispose();
});
test('offline reconciliation retains supplied device evidence and never silently rebases conflict',async()=>{
 let body:any;const service=setup(async(_p,m,b)=>{if(m==='GET')return data('item',personal(3));body=b;throw Object.assign(new Error('Review the current ancestor'),{code:'personal_needs_review'});});await service.select(target);
 const offline={deviceId:'device',deviceMutationId:'mutation',sequence:1,baseRevision:0,authoredAt:'2026-09-06T12:00:00Z'};
 await service.reconcile({operationId:'00000000-0000-4000-8000-000000000001',offline,intent:{action:'watched',value:true}});await tick();assert.deepEqual(body.offline,offline);assert.equal(body.expectedRevision,0);assert.equal(service.getSnapshot().pending[0].phase,'conflict');assert.throws(()=>service.retryMutation(),/offline intent needs review/);service.dismissMutation();assert.equal(service.getSnapshot().pending.length,0);service.dispose();
});
test('CD-24: audiobook part with progress past duration opens with a bounded resume',async()=>{
 // Persisted progress past the current duration still opens; the server clamps
 // the resume action to the known duration (detail.ts:140 bound kept).
 const over=data();
 over.item.duration=100;over.item.progressSeconds=150;
 over.actions=over.actions.map((a:any)=>a.id==='resume'?{...a,playback:{itemId:'item',startSeconds:100}}:a);
 const s=setup(async()=>over);await s.select(target);
 assert.equal(s.getSnapshot().phase,'ready');
 assert.equal(s.getSnapshot().data!.item.progressSeconds,150);
 assert.equal(s.getSnapshot().data!.actions.find(a=>a.id==='resume')!.playback!.startSeconds,100);
 s.dispose();
 // A resume past the duration is not offered; the rest of the page is kept.
 const bad=data();
 bad.actions=bad.actions.map((a:any)=>a.id==='resume'?{...a,playback:{itemId:'item',startSeconds:150}}:a);
 const t=setup(async()=>bad);await t.select(target);
 assert.equal(t.getSnapshot().phase,'ready');
 assert.deepEqual(t.getSnapshot().data!.actions.filter(a=>a.playback).map(a=>a.id),['start_over']);
 t.dispose();
});

test('a page of credits keeps the credits it can read; the group asked for, the cursor and the bound stay exact',()=>{
 const credit=(id:string)=>({id,name:'Name '+id,role:'Role',department:'Acting',provider:'tmdb'});
 const revision={catalog:1,viewer:0};
 const page=parseCreditPage({revision,group:'cast',total:1,future:true,credits:[credit('1'),{name:'No id'},'nope',{...credit('2'),portraitUrl:'/v1/metadata/item/other/art/portrait?v=1',future:1}],nextCursor:'next'},'item','cast');
 assert.deepEqual(page,{revision,group:'cast',credits:[credit('1'),credit('2')],total:2,nextCursor:'next'});
 for(const broken of [{group:'crew',total:0,credits:[]},{group:'cast',total:0,credits:'nope'},{group:'cast',total:0,credits:[],nextCursor:7},{group:'cast',total:0,credits:Array(10001).fill(credit('1'))}])assert.throws(()=>parseCreditPage(broken,'item','cast'),{code:'invalid_detail'});
});

test('detail credit pages read each later page by its cursor', async()=>{
 const {readCreditPage}=await import('../src/detail.ts');
 const revision={catalog:3,viewer:1};
 const makePage=(start:number)=>{
  const entries=Array.from({length:Math.min(37,537-start)},(_,index)=>{const id=String(start+index);return {id,kind:'movie',title:'Title '+id,libraryId:'library',available:true,navigation:{view:'item',entityId:id},playback:{itemId:id}};});
  return {id:'more_like:local:item',evidenceId:'item',relation:'more_like',endpoint:'/v1/items/item/recommendations/rows/more_like',title:'More like Item',kind:'related',artworkShape:'poster',viewerFence:'fence',entries,revision,start,limit:37,total:537,hasMore:start+entries.length<537,nextCursor:start+entries.length<537?String(start+entries.length):''};
 };
 const paths:string[]=[];
 const api:DetailApi={request:async<T>(path:string)=>{
  paths.push(path);const url=new URL(path,'http://test');const start=Number(url.searchParams.get('cursor')??0);const row=makePage(start);
  if(url.pathname.endsWith('/credits')) return {group:'cast',revision,total:537,credits:row.entries.map(entry=>({id:entry.id,name:entry.title,role:'Role',department:'Acting',provider:'tmdb'})),...(row.nextCursor?{nextCursor:row.nextCursor}:{})} as T;
  return row as T;
 }};
 const credits=await readCreditPage(api,'item','cast');
 // Credits page by cursor: each page is read on its own (the native title controller keeps its window).
 const later=await readCreditPage(api,'item','cast',{cursor:credits.nextCursor});
 const ids=Array.from({length:537},(_,index)=>String(index));
 assert.deepEqual(credits.credits.map(credit=>credit.id),ids.slice(0,37));
 assert.deepEqual(later.credits.map(credit=>credit.id),ids.slice(37,74));assert.equal(later.nextCursor,'74');
 assert.ok(paths.includes('/v1/items/item/credits?group=cast&limit=37&cursor=37'));
});

test('a page that reads recommendations itself asks detail for none, on every read of that selection', async () => {
  const paths: string[] = [];
  const service = setup(async path => { paths.push(path); return data(); });
  await service.select(target, {related: 'none'});
  assert.equal(service.getSnapshot().phase, 'ready');
  await service.refresh(); await service.refresh();
  await service.select({...target, itemId: 'item'});
  assert.deepEqual(paths.map(path => path.split('?')[1]), ['related=none', 'related=none', 'related=none', 'related=all']);
  assert.throws(() => service.select(target, {related: 'some' as never}));
  service.dispose();
});


test('a personal answer that is not this viewer\'s keeps the command for another try', async () => {
  const service=setup(async (_p,m,b)=>{if(m==='GET')return data();return {...receipt(b,{...personal(1),favorite:true}),viewerFence:'other'};});
  await service.select(target);await service.mutate({action:'favorite',value:true});
  assert.equal(service.getSnapshot().pending.length,1);service.dispose();
});

test('a favourite tapped while the server is out of reach is kept, the screen says so and moves on, and it is sent once the server answers', async () => {
  let state=personal(),reachable=false;const writes:{key:string|null;body:any}[]=[];
  const kept=new Map<string,OutboxEntry>();
  const store={list:async()=>[...kept.values()],put:async(entry:OutboxEntry)=>{kept.set(entry.id,entry);},delete:async(id:string)=>{kept.delete(id);}};
  const session:LocalSession={accessToken:'token',expiresAt:'2099-01-01T00:00:00Z',authorizationHorizon:'2099-01-02T00:00:00Z',tokenGeneration:'1',sessionFamilyId:'family',deviceId:'device',viewer:{serverId:'server',authority:'local',accountId:'account',profileId:'profile',role:'owner'}};
  const api=new HttpLocalApi('http://detail.test',session.accessToken,async (input,init)=>{
    const path=new URL(String(input)).pathname;
    if(init?.method==='PUT') {
      const body=JSON.parse(String(init.body));writes.push({key:new Headers(init.headers).get('Idempotency-Key'),body});
      if(!reachable)throw new TypeError('offline');
      state={...state,favorite:body.favorite,revision:state.revision+1};
      return Response.json(wireFixture(receipt(body,state),{progressSeconds:'progressMs'}));
    }
    assert.equal(path,'/v1/items/item/detail');return Response.json(wireFixture(data('item',state),{duration:'durationMs',progressSeconds:'progressMs',startSeconds:'startMs'}));
  });
  api.setOutboxSession(session);const outbox=api.configureOutbox(store);let ids=0;
  const service=new DetailService(withQueries({api,scope,timeoutMs:4000,requestId:async()=>`12345678-1234-4123-8123-${String(++ids).padStart(12,'0')}`}));
  try {
    await service.select(target);await service.mutate({action:'favorite',value:true});
    assert.equal(service.getSnapshot().mutationError!.code,'sync_pending');
    assert.equal(service.getSnapshot().pending.length,0,'the screen is free for the next action');
    assert.equal(outbox.waiting(),1);
    reachable=true;outbox.wake();
    for(let n=0;n<50&&outbox.waiting();n++)await tick();
    assert.equal(outbox.waiting(),0);assert.equal(state.favorite,true);
    assert.equal(writes.length,2);assert.deepEqual(writes[1],writes[0],'the same write, with the same key');
    assert.equal(writes[0].key,writes[0].body.operationId);
  } finally {service.dispose();outbox.dispose();}
});
