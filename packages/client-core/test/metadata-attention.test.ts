import {test} from 'node:test';
import assert from 'node:assert/strict';
import {parseMetadataAttention,metadataAttentionService} from '../src/metadata-attention.ts';
import {parseMetadataSettings} from '../src/metadata-settings.ts';
const page={libraryId:'lib',total:1,nextCursor:'',entries:[{entityId:'title',kind:'item',itemKind:'movie',parentId:'',actions:['accept','search','id','dismiss'],title:'Film',revision:4,reason:'low_confidence',confidence:.88,candidates:[{key:'a'.repeat(64),identity:{provider:'tmdb',type:'movie',id:'42'},title:'Film',year:2020,confidence:.88,reasons:['exact_title_or_alias'],orders:[]}]}]};
test('attention parser keeps reason and typed candidates',()=>{const out=parseMetadataAttention(page,'lib');assert.equal(out.entries[0].candidates[0].identity.id,'42');assert.ok(Object.isFrozen(out.entries));assert.throws(()=>parseMetadataAttention(page,'wrong'));assert.throws(()=>parseMetadataAttention({...page,entries:Array(51).fill(page.entries[0])},'lib'));assert.throws(()=>parseMetadataAttention({...page,nextCursor:7},'lib'));});
test('one title or suggestion that cannot be read is left out alone',()=>{const entry=page.entries[0],candidate=entry.candidates[0];
 const out=parseMetadataAttention({...page,futureField:1,entries:[{...entry,confidence:NaN},{...entry,entityId:'kind',kind:'season'},{...entry,entityId:'stale',revision:0},entry,{...entry,title:'Again'},
  {...entry,entityId:'second',futureField:1,actions:['accept','merge','dismiss','dismiss'],candidates:[{...candidate,identity:{provider:'imdb',type:'movie',id:'7'}},{...candidate,key:'b'.repeat(64),reasons:['exact',7],orders:[{id:'aired',name:'Aired'},{id:7}]},{...candidate,key:'b'.repeat(64),title:'Again'}]}]},'lib');
 assert.deepEqual(out.entries.map(e=>[e.entityId,e.title]),[['title','Film'],['second','Film']]);
 assert.deepEqual([...out.entries[1].actions],['accept','dismiss']);
 assert.deepEqual(out.entries[1].candidates.map(c=>[c.key[0],c.title,[...c.reasons],c.orders.length]),[['b','Film',['exact'],1]]);
 assert.equal(parseMetadataAttention({...page,entries:[{...entry,candidates:Array(4).fill(candidate)}]},'lib').entries.length,0);});
test('attention commands use the library route and its revision',async()=>{const calls:unknown[]=[];const service=metadataAttentionService(async <T>(path:string,method?:string,body?:unknown)=>{calls.push({path,method,body});return page as T;},'lib');await service.list();await service.act({kind:'item',entityId:'title',expectedRevision:4,action:'dismiss'});assert.deepEqual(calls[1],{path:'/v1/libraries/lib/metadata/attention',method:'POST',body:{kind:'item',entityId:'title',expectedRevision:4,action:'dismiss'}});});
test('entry actions keep the original command bytes, escaped library route and one dispatch',async()=>{
 const calls:unknown[]=[];
 const service=metadataAttentionService(async <T>(path:string,method?:string,body?:unknown)=>{calls.push({path,method,body});return undefined as T;},'lib /');
 const entry=parseMetadataAttention(page,'lib').entries[0];
 const actions=[{action:'accept' as const,candidateKey:'a'.repeat(64),order:'official'},
  {action:'search' as const,query:'  Film /  '},{action:'id' as const,provider:'tmdb' as const,providerId:'42'},
  {action:'dismiss' as const},{action:'retry' as const}];
 for(const command of actions){
  const before=calls.length;
  await service.act({kind:entry.kind,entityId:entry.entityId,expectedRevision:entry.revision,...command});
  await service.actOnEntry(entry,command);
  assert.equal(calls.length,before+2);
  assert.equal(JSON.stringify(calls[before+1]),JSON.stringify(calls[before]));
 }
 assert.deepEqual(calls[0],{path:'/v1/libraries/lib%20%2F/metadata/attention',method:'POST',body:{kind:'item',entityId:'title',expectedRevision:4,action:'accept',candidateKey:'a'.repeat(64),order:'official'}});
 const failure=new Error('unknown outcome');let attempts=0;
 const failed=metadataAttentionService(async()=>{attempts++;throw failure;},'lib');
 await assert.rejects(failed.actOnEntry(entry,{action:'retry'}),error=>error===failure);
 assert.equal(attempts,1);
});
test('metadata settings return credential presence only',()=>{const out=parseMetadataSettings({revision:1,artworkMaxEdge:1920,localArtworkUsedBytes:0,localArtworkLimitBytes:2**31,includeAdult:false,tmdbCredentialConfigured:true,tvdbCredentialConfigured:false,tmdbReadToken:'secret'});assert.equal('tmdbReadToken' in out,false);assert.throws(()=>parseMetadataSettings({...out,localArtworkLimitBytes:0}));});

test('an episode order is stored with what is added to file numbers, and reads back the same', async () => {
  const {orderTakesOffset, readTmdbOrder, tmdbAbsoluteOrder} = await import('../src/screen-metadata.ts');
  assert.equal(tmdbAbsoluteOrder('official', 0), 'official');
  assert.equal(tmdbAbsoluteOrder('official', 12), 'tmdb-absolute:official:12');
  assert.equal(tmdbAbsoluteOrder('tmdb-group:abc123', -3), 'tmdb-absolute:tmdb-group:abc123:-3');
  assert.deepEqual(readTmdbOrder('tmdb-absolute:tmdb-group:abc123:-3'), {base: 'tmdb-group:abc123', offset: -3});
  assert.deepEqual(readTmdbOrder('official'), {base: 'official', offset: 0});
  assert.deepEqual(readTmdbOrder(''), {base: '', offset: 0});
  assert.equal(orderTakesOffset('official'), true);
  assert.equal(orderTakesOffset('dvd'), false);
  assert.throws(() => tmdbAbsoluteOrder('dvd', 2));
});

test('screen rows and parentless songs do not construct an album artwork route',()=>{
 const entry=page.entries[0];
 const out=parseMetadataAttention({...page,entries:[entry,{...entry,entityId:'song',kind:'song',itemKind:'song',parentId:''}]},'lib');
 assert.deepEqual(out.entries.map(e=>e.entityId),['title','song']);
 assert.equal(out.entries[0].artworkUrl,undefined);
 assert.equal(out.entries[1].artworkUrl,undefined);
 assert.equal(out.entries[0].candidates[0].identity.id,'42');
});
