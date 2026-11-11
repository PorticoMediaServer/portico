import test from 'node:test';
import assert from 'node:assert/strict';
import {parseServerInvitation, validDirectPassword} from '../src/direct-account.ts';

test('accepts a policy password: 8+ chars, upper, lower and a non-letter', () => {
  assert.equal(validDirectPassword('Abcdef1!'), true);
  assert.equal(validDirectPassword('Correct Horse 9'), true);
  assert.equal(validDirectPassword('Pässwörd1'), true);
});

test('rejects short, single-case, letter-only and oversize values', () => {
  assert.equal(validDirectPassword(''), false);
  assert.equal(validDirectPassword('Ab1!'), false);
  assert.equal(validDirectPassword('abcdefgh1!'), false, 'no uppercase');
  assert.equal(validDirectPassword('ABCDEFGH1!'), false, 'no lowercase');
  assert.equal(validDirectPassword('Abcdefgh'), false, 'letters only');
  // 72-byte UTF-8 cap: 71 ASCII chars fit, 73 do not.
  assert.equal(validDirectPassword('A' + 'a'.repeat(69) + '1'), true);
  assert.equal(validDirectPassword('A' + 'a'.repeat(71) + '1'), false);
  // Counting is by code point, not UTF-16 units: 7 emojis + 'Aa1' is 10 chars.
  assert.equal(validDirectPassword('😀'.repeat(7) + 'Aa1'), true);
  assert.equal(validDirectPassword('😀'.repeat(4) + 'Aa1'), false, '7 code points is too short');
});

test('edge: non-string-shaped and boundary input never throws', () => {
  assert.equal(validDirectPassword('Aa1!Aa1!'), true);
  assert.equal(validDirectPassword('        Aa1'), true, 'spaces count as non-letters');
  assert.equal(validDirectPassword('12345678'), false);
});


test('invitation preview has explicit public fields and rejects private recipient details',async()=>{
 const {parsePublicInvitation}=await import('../src/direct-account.ts');
 const v={serverName:'Fixture server',expiresAt:'2026-11-01T12:00:00Z',role:'member',libraries:[{name:'Films'}],allLibraries:false,signInMethod:'portico',inviterUsername:'fixture_owner'};
 assert.equal(parsePublicInvitation(v).inviterUsername,'fixture_owner');
 assert.deepEqual(parsePublicInvitation(v).libraries,[{name:'Films'}]);
 assert.deepEqual(parsePublicInvitation({...v,libraries:[{name:'Films',kind:'movies'}]}).libraries,[{name:'Films',kind:'movies'}]);
 assert.throws(()=>parsePublicInvitation({...v,libraries:[{name:'Films',kind:null}]}));
 for(const field of ['notes','email','porticoUsername'])assert.throws(()=>parsePublicInvitation({...v,[field]:'private'}));
 for(const patch of [{signInMethod:'other'},{role:'owner'},{libraries:[{}]},{expiresAt:'bad'}])assert.throws(()=>parsePublicInvitation({...v,...patch}));
});
test('private notes use Unicode character limits and never accept control or formatting characters',async()=>{
 const {privatePersonNotes}=await import('../src/direct-account.ts');
 assert.equal(privatePersonNotes('  fixture note  '),'fixture note');assert.equal(privatePersonNotes('  '),null);assert.equal(privatePersonNotes(null),null);
 assert.equal(Array.from(privatePersonNotes('😀'.repeat(500))!).length,500);
 for(const s of ['x'.repeat(501),'private\ntext','hidden\u202e'])assert.throws(()=>privatePersonNotes(s));
});
test('person identity is username first, profile only when different and Private viewer stays private',async()=>{
 const {personIdentity}=await import('../src/direct-account.ts');
 assert.deepEqual(personIdentity('fixture_user','Kids'),{username:'fixture_user',profileName:'Kids'});
 assert.deepEqual(personIdentity('fixture_user','fixture_user'),{username:'fixture_user'});
 assert.deepEqual(personIdentity('fixture_user','Kids',true),{privateViewerKey:'together.privateViewer'});
 assert.throws(()=>personIdentity('','Kids'));
});

test('short invitation preview posts only the normalized short capability and rejects URL/long forms locally',async()=>{
 const {PeopleInvitationsClient}=await import('../src/direct-account.ts');
 const calls:any[]=[];
 const preview={serverName:'Fixture',expiresAt:'2026-11-01T12:00:00Z',role:'member',libraries:[],allLibraries:false,signInMethod:'local'};
 const client=new PeopleInvitationsClient({request:async<T>(path:string,method?:string,body?:unknown)=>{calls.push({path,method,body});return preview as T;}});
 assert.deepEqual(await client.previewShort('0123-4567-89ab-cdef'),preview);
 assert.deepEqual(calls,[{path:'/v1/access/invitations/preview',method:'POST',body:{shortCode:'0123456789ABCDEF'}}]);
 for(const raw of ['x'.repeat(43),'https://server.example/?shortCode=0123456789ABCDEF','0123-4567-89AB-CDEI'])await assert.rejects(client.previewShort(raw));
 assert.equal(calls.length,1);
});

test('an invitation\'s access grant is read exactly, and an inexact one refuses the document',()=>{
 const base={id:'grant',revision:1,role:'member',state:'pending',createdAt:'2026-10-01T00:00:00Z',expiresAt:'2026-10-02T00:00:00Z',allowedLibraries:[]};
 const access={maxStreamsMode:'limit',maxStreams:2,allowRemote:false,accessTokens:false,maxContentRating:'PG-13'};
 assert.deepEqual(parseServerInvitation({...base,access}).access,access);
 for(const bad of [undefined,{...access,maxStreams:0},{...access,maxStreamsMode:'inherit'},{...access,maxStreamsMode:'some'},{...access,allowRemote:'no'},{...access,maxContentRating:7}])
  assert.throws(()=>parseServerInvitation({...base,access:bad}),JSON.stringify(bad));
});

test('invitations on the rebuilt server: one list, filtered and sorted here, codes only where they are shown', async () => {
 const {PeopleInvitationsClient}=await import('../src/direct-account.ts');
 const at=(day:number)=>`2026-11-${String(day).padStart(2,'0')}T00:00:00Z`;
 const rows=[{id:'inv_a',code:'K7QP-2MXD-9HRT-4WNC',role:'member',state:'open',allLibraries:true,libraries:[],note:'For Dana',createdAt:at(3),expiresAt:at(10)},
  {id:'inv_b',role:'admin',state:'accepted',allLibraries:false,libraries:['lib-1'],createdAt:at(1),expiresAt:at(8),acceptedAt:at(2)},
  {id:'inv_c',role:'member',state:'revoked',allLibraries:true,libraries:[],createdAt:at(2),expiresAt:at(9)}];
 const calls:{path:string;method?:string;body?:any}[]=[];
 const client=new PeopleInvitationsClient({request:async<T>(path:string,method?:string,body?:unknown)=>{calls.push({path,method,body});
  if(method==='POST')return {...rows[0],id:'inv_new',note:(body as any).note} as T;
  if(method==='DELETE'){rows[0]={...rows[0],state:'revoked',code:undefined as any};return undefined as T;}
  return (method==='PATCH'?{...rows[0],...(body as object)}:{items:rows}) as T;}});
 const page=await client.list({sort:'sent',direction:'desc'});
 assert.deepEqual(page.items.map(row=>[row.id,row.state]),[['inv_a','pending'],['inv_c','revoked'],['inv_b','accepted']]);
 assert.equal(page.items.some(row=>'code' in row),false,'a list never carries a code');
 assert.deepEqual(page.items[2].allowedLibraries,['lib-1']);
 assert.deepEqual((await client.list({state:'pending',q:'dana'})).items.map(row=>row.id),['inv_a']);
 assert.deepEqual((await client.list({sort:'status',direction:'asc'})).items.map(row=>row.id),['inv_a','inv_b','inv_c']);
 assert.deepEqual(await client.link({id:'inv_a',revision:1,state:'pending'}),{code:'K7QP-2MXD-9HRT-4WNC',expiresAt:at(10)});
 assert.deepEqual(await client.shortCode({id:'inv_a',revision:1,state:'pending'},'op'),{serverId:'',expiresAt:at(10),shortCode:'K7QP-2MXD-9HRT-4WNC'});
 calls.length=0;
 const created=await client.create({operationId:'op',role:'member',allowedLibraries:['lib-1'],expiresInHours:72,notes:' For Eli '});
 assert.deepEqual(calls[0],{path:'/v1/admin/invitations',method:'POST',body:{role:'member',allLibraries:false,libraries:['lib-1'],note:'For Eli',expiresInDays:3}});
 assert.equal(created.code,'K7QP-2MXD-9HRT-4WNC');
 calls.length=0;
 await assert.rejects(client.create({operationId:'op',role:'member',allowedLibraries:[],expiresInHours:24,access:{maxStreamsMode:'limit',maxStreams:2,allowRemote:true,accessTokens:true,maxContentRating:''}}),/not available/);
 await assert.rejects(client.create({operationId:'op',role:'member',allowedLibraries:[],expiresInHours:24,porticoUsername:'dana'}),/not available/);
 assert.equal(calls.length,0,'what an invitation cannot keep is refused before anything is sent');
 await client.edit('inv_a',{operationId:'op',expectedRevision:1,role:'admin',allowedLibraries:[],expiresAt:at(10)});
 assert.deepEqual(calls[0],{path:'/v1/admin/invitations/inv_a',method:'PATCH',body:{role:'admin',allLibraries:true,libraries:[],note:''}});
 const revoked=await client.revoke({id:'inv_a',revision:1},'op');
 assert.equal(revoked.state,'revoked');assert.equal(calls[1].method,'DELETE');
 await assert.rejects(client.link({id:'inv_a',revision:1,state:'pending'}),/no longer usable/);
});
