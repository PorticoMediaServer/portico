import test from 'node:test';
import assert from 'node:assert/strict';

/** The generated runtime over schemas of a test's own: the decoder's union rules, without a route that needs them. */
async function runtimeFor(shapes:Record<string,unknown>){
 const {readFileSync}=await import('node:fs');
 const {stripTypeScriptTypes}=await import('node:module');
 const template=readFileSync(new URL('../../../apikit/templates/runtime.ts.txt',import.meta.url),'utf8');
 const source=template.replace("import { schemas } from './schemas.ts';",`const schemas = ${JSON.stringify(shapes)};`);
 return await import('data:text/javascript;base64,'+Buffer.from(stripTypeScriptTypes(source)).toString('base64'));
}

test('a nullable string keeps string and null and refuses other values',async()=>{
 const api=await runtimeFor({Tracks:{type:'object',properties:{subtitles:{anyOf:[{type:'string'},{type:'null'}]}}}});
 for(const subtitles of [null,'eng']) assert.equal(api.decode('Tracks',{subtitles}).subtitles,subtitles);
 for(const subtitles of [1,{},[],true]) assert.throws(()=>api.decode('Tracks',{subtitles}));
});

test('closed requests reject optional nulls while presentation objects retain their absent-null rule',async()=>{
 const fields={type:'object',properties:{id:{type:'string'},note:{type:'string'}},required:['id']};
 const api=await runtimeFor({Request:{...fields,additionalProperties:false},Presentation:fields,Nullable:{...fields,additionalProperties:false,properties:{...fields.properties,note:{anyOf:[{type:'string'},{type:'null'}]}}}});
 assert.deepEqual({...api.decode('Request',{id:'request'})},{id:'request'});
 assert.equal(api.decode('Request',{id:'request',note:'kept'}).note,'kept');
 assert.throws(()=>api.decode('Request',{id:'request',note:null}));
 assert.throws(()=>api.decode('Request',{id:'request',unknown:true}));
 assert.deepEqual({...api.decode('Presentation',{id:'request',note:null})},{id:'request'});
 assert.equal(api.decode('Nullable',{id:'request',note:null}).note,null);
});

test('a nullable list keeps null and string lists and refuses malformed lists',async()=>{
 const api=await runtimeFor({Policy:{type:'object',properties:{allowedLibraries:{anyOf:[{type:'array',items:{type:'string'}},{type:'null'}]}}}});
 for(const allowedLibraries of [null,[],['library']]) assert.doesNotThrow(()=>api.decode('Policy',{allowedLibraries}));
 for(const allowedLibraries of [1,{},[1],true]) assert.throws(()=>api.decode('Policy',{allowedLibraries}));
});

test('unique wire arrays reject duplicate compatibility IDs and equal objects with different key order',async()=>{
 const api=await runtimeFor({Compatibility:{type:'array',maxItems:3,uniqueItems:true,items:{type:'integer',enum:[1,2,4]}},Records:{type:'array',uniqueItems:true,items:{type:'object',additionalProperties:{type:'integer'}}}});
 assert.deepEqual(api.decode('Compatibility',[1,4]),[1,4]);
 for(const value of [[1,1],[3],[1,2,4,4],null]) assert.throws(()=>api.decode('Compatibility',value));
 assert.throws(()=>api.decode('Records',[{a:1,b:2},{b:2,a:1}]));
 assert.equal(api.decode('Records',[{a:1},{a:2}]).length,2);
});

test('analysis control decoder preserves acknowledgements and queued jobs',async()=>{
 const api=await import('../src/runtime.ts');
 const name='PostItemsIdAnalysisJobsJobControlResponse';
 for(const status of ['pause','resume','cancel']) assert.equal((api.decode(name,{variant:'acknowledgement',status}) as {status:string}).status,status);
 const job={variant:'job',id:'job',status:'queued',processed:0,analyzed:0,warnings:0};
 const decoded=api.decode(name,job) as typeof job;
 assert.equal(decoded.id,job.id);assert.equal(decoded.warnings,0);
 for(const invalid of [null,[],1,{},{status:1},{variant:'acknowledgement'},{variant:'acknowledgement',status:1},{variant:'job',id:'job'},{status:'pause'}]) assert.throws(()=>api.decode(name,invalid));
 assert.deepEqual({...(api.decode(name,{variant:'acknowledgement',status:'pause',future:1}) as object)},{variant:'acknowledgement',status:'pause'});
 assert.deepEqual({...(api.decode(name,{variant:'newer-kind',status:'x'}) as object)},{variant:'newer-kind',unknownVariant:true});
});

test('oneOf decoding refuses overlapping and absent response branches',async()=>{
 const api=await runtimeFor({Unique:{oneOf:[
  {type:'object',properties:{name:{type:'string'}},required:['name']},
  {type:'object',properties:{count:{type:'integer'}},required:['count']},
 ]}});
 assert.equal(api.decode('Unique',{name:'title'}).name,'title');
 assert.equal(api.decode('Unique',{count:1}).count,1);
 for(const value of [{},null,[],{name:'title',count:1}]) assert.throws(()=>api.decode('Unique',value));
});

test('invitation acceptance contracts describe local and Portico methods without administrator notes',async()=>{
 const {decode}=await import('../src/runtime.ts');
 const code='fixture-invitation-capability-00000000000001';
 const shortCode='0123-4567-89AB-CDEF';
 const localMethod={username:'fixture_user',password:'Fixture1!'};
 const porticoMethod={porticoIdentity:{keyId:'fixture-key',payload:'fixture-payload',signature:'fixture-signature'}};
 for(const capability of [{code},{shortCode}]){
  assert.doesNotThrow(()=>decode('PostAccessInvitationsAcceptRequest',{...capability,...localMethod}));
  assert.doesNotThrow(()=>decode('PostAccessInvitationsAcceptRequest',{...capability,...porticoMethod}));
 }
 const local={variant:'accepted',accountId:'fixture_account',username:'fixture_user',profileId:'fixture_profile',role:'member',allowedLibraries:[]};
 assert.doesNotThrow(()=>decode('PostAccessInvitationsAcceptResponse',local));
 const {schemas}=await import('../src/schemas.ts');
 const localBranch=(schemas.PostAccessInvitationsAcceptResponse as any).anyOf[0];
 assert.equal('notes' in localBranch.properties,false);assert.equal('email' in localBranch.properties,false);
 for(const bad of [{},null,{code},{code,porticoIdentity:{}},
  {code:'fixture-code',...localMethod},{code:'x'.repeat(129),...localMethod},
  {shortCode:'0123 4567 89AB CDEF',...localMethod},
  {code,shortCode,...localMethod},{code,...localMethod,...porticoMethod},
 ])assert.throws(()=>decode('PostAccessInvitationsAcceptRequest',bad));
});
