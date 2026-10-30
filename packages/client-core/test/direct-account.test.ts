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
test('People invitation list requests whole-set search/sort and preserves authoritative counts',async()=>{
 const {PeopleInvitationsClient,parsePeopleInvitationPage}=await import('../src/direct-account.ts');
 const row={id:'invitation-one',revision:1,role:'member',state:'expired',createdAt:'2026-10-01T00:00:00Z',expiresAt:'2026-10-02T00:00:00Z',allowedLibraries:[],notes:'A genuine link-only note',access:{maxStreamsMode:'inherit',maxStreams:0,allowRemote:true,accessTokens:true,maxContentRating:''}};
 const page={items:[row],limit:2,totalCount:27,matchedCount:13,nextCursor:'actual-next-cursor'};
 const calls:{path:string;method?:string;body?:unknown}[]=[];
 const client=new PeopleInvitationsClient({request:async<T>(path:string,method?:string,body?:unknown)=>{calls.push({path,method,body});return page as T;}});
 const value=await client.list({q:'find this',sort:'status',direction:'asc',state:'expired',limit:2});
 assert.equal(value.totalCount,27);assert.equal(value.matchedCount,13);assert.equal(value.items[0].notes,row.notes);
 const query=new URL(calls[0].path,'https://fixture.invalid');
 assert.equal(query.pathname,'/v1/admin/access/invitations');assert.equal(query.searchParams.get('q'),'find this');assert.equal(query.searchParams.get('state'),'expired');assert.equal(query.searchParams.get('sort'),'status');assert.equal(query.searchParams.get('direction'),'asc');assert.equal(query.searchParams.get('limit'),'2');
 for(const change of [{totalCount:undefined},{matchedCount:undefined},{matchedCount:28},{matchedCount:0},{limit:101},{items:[row,row]},{items:[{...row,email:'fabricated@example.invalid'}]},{items:[{...row,code:'must-never-appear'}]}])assert.throws(()=>parsePeopleInvitationPage({...page,...change}));
 const before=calls.length;await assert.rejects(client.list({limit:101}));await assert.rejects(client.list({q:'😀'.repeat(41)}));assert.equal(calls.length,before);
});
test('private notes use Unicode character limits and never accept control or formatting characters',async()=>{
 const {privatePersonNotes}=await import('../src/direct-account.ts');
 assert.equal(privatePersonNotes('  fixture note  '),'fixture note');assert.equal(privatePersonNotes('  '),null);assert.equal(privatePersonNotes(null),null);
 assert.equal(Array.from(privatePersonNotes('😀'.repeat(500))!).length,500);
 for(const s of ['x'.repeat(501),'private\ntext','hidden\u202e'])assert.throws(()=>privatePersonNotes(s));
});
test('invitation client recovers only code and expiry against the current revision',async()=>{
 const {PeopleInvitationsClient}=await import('../src/direct-account.ts');const calls:any[]=[];
 const api={request:async<T>(path:string,method?:string,body?:unknown)=>{calls.push({path,method,body});return {code:'x'.repeat(43),expiresAt:'2026-11-01T12:00:00Z',notes:'must never be shared'} as T;}};
 const c=new PeopleInvitationsClient(api);const link=await c.link({id:'fixture',revision:2,state:'pending'});
 assert.deepEqual(link,{code:'x'.repeat(43),expiresAt:'2026-11-01T12:00:00Z'});assert.deepEqual(calls[0],{path:'/v1/admin/access/invitations/fixture/link',method:'POST',body:{expectedRevision:2}});
 await assert.rejects(c.link({id:'fixture',revision:2,state:'accepted'}));assert.equal(calls.length,1);
});
test('invitation link preserves only a canonical supplied expected identity and never substitutes an address',async()=>{
 const {PeopleInvitationsClient}=await import('../src/direct-account.ts');
 const serverId='srv_'+'A'.repeat(43),base={code:'x'.repeat(43),expiresAt:'2026-11-01T12:00:00Z'};
 let raw:unknown={...base,serverId,notes:'private'};
 const c=new PeopleInvitationsClient({request:async<T>()=>raw as T});
 assert.deepEqual(await c.link({id:'fixture',revision:2,state:'pending'}),{...base,serverId});
 raw=base;assert.deepEqual(await c.link({id:'fixture',revision:2,state:'pending'}),base);
 for(const serverId of [null,'https://server.test','srv_'+'B'.repeat(43)]){raw={...base,serverId};await assert.rejects(c.link({id:'fixture',revision:2,state:'pending'}));}
});
test('person identity is username first, profile only when different and Private viewer stays private',async()=>{
 const {personIdentity}=await import('../src/direct-account.ts');
 assert.deepEqual(personIdentity('fixture_user','Kids'),{username:'fixture_user',profileName:'Kids'});
 assert.deepEqual(personIdentity('fixture_user','fixture_user'),{username:'fixture_user'});
 assert.deepEqual(personIdentity('fixture_user','Kids',true),{privateViewerKey:'together.privateViewer'});
 assert.throws(()=>personIdentity('','Kids'));
});

test('short invitation issuance displays once, preserves receipt absence and never resolves a long secret',async()=>{
 const {PeopleInvitationsClient}=await import('../src/direct-account.ts');
 const serverId='srv_'+'A'.repeat(43),expiresAt='2026-11-01T12:00:00Z';
 const calls:{path:string;method?:string;body?:unknown}[]=[];
 let raw:unknown={serverId,expiresAt,shortCode:'0123-4567-89AB-CDEF',notes:'private'};
 const client=new PeopleInvitationsClient({request:async<T>(path:string,method?:string,body?:unknown)=>{calls.push({path,method,body});return raw as T;}});
 const invitation={id:'fixture',revision:2,state:'pending' as const};
 assert.deepEqual(await client.shortCode(invitation,'new-code'),{serverId,expiresAt,shortCode:'0123-4567-89AB-CDEF'});
 assert.deepEqual(calls[0],{path:'/v1/admin/access/invitations/fixture/short-code',method:'POST',body:{expectedRevision:2,operationId:'new-code'}});
 raw={serverId,expiresAt};
 assert.deepEqual(await client.shortCode(invitation,'new-code'),{serverId,expiresAt});
 assert.equal(calls.length,2,'receipt absence never causes an automatic reissue');
 for(const change of [{shortCode:''},{shortCode:null},{shortCode:'x'.repeat(43)},{serverId:'https://server.example'},{code:'x'.repeat(43)}]){
  raw={serverId,expiresAt,...change};await assert.rejects(client.shortCode(invitation,'next'));
 }
 const before=calls.length;await assert.rejects(client.shortCode({...invitation,state:'accepted'},'next'));assert.equal(calls.length,before);
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
