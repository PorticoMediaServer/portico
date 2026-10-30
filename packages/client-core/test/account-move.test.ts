import test from 'node:test';
import assert from 'node:assert/strict';
import {AccountMoveClient,MemberAccountMoveClient,ReadOnlySignInClient,parseSignInSettings,parseAccountMove,parseMovePasswordTicket,parseMoveCodePreview,parseAccountMoveCodes,consumeMoveCodeFragment,consumeAccountMoveFragment,accountMoveLink} from '../src/account-move.ts';
import {HttpLocalApi} from '../src/index.ts';
import {parseServerSignIn} from '../src/sign-in.ts';
import {makeMemberResetCode} from '../src/account-reset.ts';
const code='A'.repeat(43),until=new Date(Date.now()+60000).toISOString();
test('local move and reset codes preserve a canonical expected server identity without manufacturing one',async()=>{
 const serverId='srv_'+'A'.repeat(43),row={accountId:'a',username:'alice',code,expiresAt:until};
 assert.equal(parseAccountMoveCodes({items:[{...row,serverId}]})[0].serverId,serverId);
 assert.equal(Object.hasOwn(parseAccountMoveCodes({items:[row]})[0],'serverId'),false);
 for(const invalid of [null,'server','srv_'+'B'.repeat(43)])assert.throws(()=>parseAccountMoveCodes({items:[{...row,serverId:invalid}]}));
 let raw:unknown={code,expiresAt:until,serverId};
 const api={request:async<T>()=>raw as T};
 assert.equal((await makeMemberResetCode(api,'member',1)).serverId,serverId);
 raw={code,expiresAt:until};assert.equal(Object.hasOwn(await makeMemberResetCode(api,'member',1),'serverId'),false);
 for(const invalid of [null,'server','srv_'+'B'.repeat(43)]){raw={code,expiresAt:until,serverId:invalid};await assert.rejects(makeMemberResetCode(api,'member',1));}
});
const move={id:'move_A',from:'portico',to:'local',startOver:false,stage:'moving_to_local',revision:2,people:137,profiles:180,invitations:4,waiting:120,serverName:'Home',ownAddressRequired:true,devices:214};
const session={accessToken:'candidate_A',sessionFamilyId:'family_A',tokenGeneration:'1',expiresAt:'2099-01-01T00:00:00Z',authorizationHorizon:'2099-02-01T00:00:00Z',viewer:{serverId:'server',accountId:'member',profileId:'profile',authority:'local',role:'member'}};
const grant={authority:'local',serverId:'server',account:{id:'member',username:'alice',primaryProfileId:'profile',role:'member',revision:1,disabled:false,allowedLibraries:[]},profiles:[{id:'profile',name:'Evening',primary:true,position:0,art:'blue',revision:1,pinRevision:1,pinRequired:false}],canManage:true,capabilities:{manageAccount:true,managePeople:false,manageServer:false,manageSignIn:false},session};
function fixture(response:unknown=grant){
 let reply=response,current=true;const calls:{path:string;method?:string;body?:any}[]=[],retired:string[]=[];
 const api={request:async<T>(path:string,method?:string,body?:unknown)=>{calls.push({path,method,body});return reply as T;}};
 const options={serverId:'server',isCurrent:()=>current,retireLateGrant:async(s:any)=>{retired.push(s.sessionFamilyId);}};
 return {api,options,calls,retired,set:(value:unknown)=>{reply=value;},end:()=>{current=false;}};
}
test('public move is explicit, committed and consistent with server kind; absent/unknown/contradictory facts refuse credentials',()=>{
 const system={id:'server',name:'Home',version:'1',signIn:'local',move:null,setupRequired:false,hostedConfigured:true,hostedAttached:false, apiLevel: 1, apiVersion: '1.0', apiMajors: [1], capabilities: []};
 for(const move of [undefined,'preview','switch','to_portico'])assert.throws(()=>parseServerSignIn({...system,move}));
 assert.equal(parseServerSignIn({...system,move:'to_local'}).move,'to_local');
 assert.equal(parseServerSignIn({...system,signIn:'portico',move:'to_portico'}).move,'to_portico');
});
test('owner move counters are whole live counts and Start over has no invented device total',()=>{
 assert.equal(parseAccountMove(move).waiting,120);
 for(const patch of [{waiting:138},{revision:0},{stage:'approved'},{ownAddressRequired:false},{devices:undefined}])assert.throws(()=>parseAccountMove({...move,...patch}));
 const {devices,...withoutDevices}=move;assert.equal('devices' in parseAccountMove({...withoutDevices,startOver:true}),false);
 assert.throws(()=>parseAccountMove({...move,startOver:true}));
});
test('old-password proof is only an opaque bounded ticket; code preview exposes no authority or secret',()=>{
 assert.deepEqual(parseMovePasswordTicket({ticket:code,expiresAt:until}),{ticket:code,expiresAt:until});
 for(const raw of [{ticket:code,expiresAt:until,session},{ticket:code,expiresAt:'2000-01-01T00:00:00Z'},{ticket:'short',expiresAt:until}])assert.throws(()=>parseMovePasswordTicket(raw));
 assert.deepEqual(parseMoveCodePreview({username:'alice',signIn:'local',expiresAt:until}),{username:'alice',signIn:'local',expiresAt:until});
 assert.throws(()=>parseMoveCodePreview({username:'alice',signIn:'local',expiresAt:until,code}));
 assert.throws(()=>parseAccountMoveCodes({items:[{accountId:'a',username:'alice',code,expiresAt:until},{accountId:'a',username:'alice',code,expiresAt:until}]}));
});
test('member commands use checked routes and code preview never redeems; the original server profile survives redemption',async()=>{
 const f=fixture({ticket:code,expiresAt:until}),client=new MemberAccountMoveClient(f.api,f.options);
 await client.password('alice','old password');f.set({username:'alice',signIn:'local',expiresAt:until});await client.previewCode(code);
 assert.deepEqual(f.calls.map(x=>x.path),['/v1/auth/move/password','/v1/auth/move/code']);
 f.set(grant);const result=await client.redeemLocal(code,'alice','new password');assert.equal(result.kind,'signed-in');if(result.kind==='signed-in')assert.equal(result.snapshot.profiles[0].id,'profile');
 assert.deepEqual(f.calls[2].body,{code,username:'alice',password:'new password'});
 assert.throws(()=>client.redeemPortico({ticket:code,code} as any,{payload:'p',signature:'s',keyId:'k'}));client.dispose();
});
test('owner commands send exact revision/once-only code request and final confirmation produces a typed local continuation',async()=>{
 const f=fixture({move}),client=new AccountMoveClient(f.api,f.options);
 assert.equal((await client.current())?.waiting,120);f.set({...move,stage:'preview'});const preview=await client.preview('local',false);
 f.set({signIn:grant});const result=await client.confirm({moveId:preview.id,expectedRevision:preview.revision,confirmation:'',username:'owner',password:'new password'});assert.equal(result.kind,'local');
 f.set({items:[{accountId:'member',username:'alice',code,expiresAt:until}]});assert.equal((await client.codes(['member']))[0].code,code);
 assert.deepEqual(f.calls[3].body,{accountIds:['member']});f.set(undefined);await client.finish(parseAccountMove(move));assert.deepEqual(f.calls[4].body,{expectedRevision:2});client.dispose();
});
test('cancelled and authority-replaced redemptions retire the exact late grant and never publish or auto replay',async()=>{
 for(const end of ['cancel','authority'] as const){
  let answer!:(v:unknown)=>void,requests=0;const retired:string[]=[];let current=true;
  const client=new MemberAccountMoveClient({request:()=>{requests++;return new Promise(resolve=>{answer=resolve;}) as any;}},{serverId:'server',isCurrent:()=>current,retireLateGrant:async s=>{retired.push(s.sessionFamilyId);}});
  const pending=client.redeemLocal(code,'alice','new password');if(end==='cancel')client.cancel();else current=false;
  answer(grant);await assert.rejects(pending);assert.deepEqual(retired,['family_A']);assert.equal(requests,1);client.dispose();
 }
});
test('duplicate mutation and offline failure grant nothing; the client never retries a spent operation',async()=>{
 let fail!:(e:unknown)=>void,calls=0;const f=fixture();
 const client=new MemberAccountMoveClient({request:()=>{calls++;return new Promise((_resolve,reject)=>{fail=reject;}) as any;}},f.options);
 const first=client.redeemLocal(code,'alice','new password');await assert.rejects(client.redeemLocal(code,'alice','new password'),{code:'move_request_in_progress'});
 fail(new Error('offline'));await assert.rejects(first);assert.equal(calls,1);assert.deepEqual(f.retired,[]);client.dispose();
});
test('link fragments are scrubbed synchronously, exact and memory-only',()=>{
 const kept:string[]=[];const history={replaceState:(_a:unknown,_b:string,url?:string|URL|null)=>{kept.push(String(url));}};
 assert.equal(consumeMoveCodeFragment({pathname:'/sign-in',search:'',hash:'#code='+code},history),code);assert.deepEqual(kept,['/sign-in']);
 for(const hash of ['#code=short','#code='+code+'&code='+code,'#code='+code+'&token=private'])assert.throws(()=>consumeMoveCodeFragment({pathname:'/sign-in',search:'',hash},history));
 assert.equal(kept.every(url=>!url.includes(code)),true);
 assert.equal(accountMoveLink('https://server.test',code),'https://server.test/sign-in#code='+code);
 assert.throws(()=>accountMoveLink('https://foreign@server.test',code));
 assert.throws(()=>consumeMoveCodeFragment({pathname:'/sign-in',search:'?code='+code,hash:''},history));assert.equal(kept.at(-1),'/sign-in');
});
test('read-only Sign-in is live owner/main-profile data; denied capability makes no request and late capability loss drops private owner facts',async()=>{
 const status={signIn:'local',owner:{accountId:'owner',username:'alice',displayName:'Alice',email:'alice@example.test'},move,webPath:'/settings?scope=server&section=signin'};
 assert.equal(parseSignInSettings(status).owner.email,'alice@example.test');
 for(const patch of [{signIn:'mixed'},{owner:{...status.owner,username:''}},{webPath:'/settings/server/signin'},{move:undefined}])assert.throws(()=>parseSignInSettings({...status,...patch}));
 let allowed=false,calls=0,answer!:(v:unknown)=>void;
 const client=new ReadOnlySignInClient({request:()=>{calls++;return new Promise(resolve=>{answer=resolve;}) as any;}},{serverId:'server',isCurrent:()=>true,capabilities:()=>({manageAccount:allowed,managePeople:allowed,manageServer:allowed,manageSignIn:allowed})});
 assert.throws(()=>client.read(),{code:'forbidden'});assert.equal(calls,0);allowed=true;
 const pending=client.read();allowed=false;answer(status);await assert.rejects(pending,{code:'cancelled'});client.dispose();
});
test('actual HTTP collision retains only the checked current DTO; English text and arbitrary fields never become a collision model',async()=>{
 let current:unknown={existingUsername:'sam_k',serverName:'Home'};
 const api=new HttpLocalApi('https://server.test','',async()=>Response.json({error:{code:'move_collision',message:'untrusted server prose',current}},{status:409}));
 await assert.rejects(api.request('/v1/auth/move/portico','POST',{}),(error:any)=>{assert.equal(error.code,'move_collision');assert.deepEqual(error.current,{existingUsername:'sam_k',serverName:'Home'});return true;});
 for(const invalid of [undefined,{existingUsername:'',serverName:'Home'},{existingUsername:'sam_k',serverName:'Home',code}]){current=invalid;await assert.rejects(api.request('/v1/auth/move/portico','POST',{}),{code:'invalid_account_move'});}
});

test('move links preserve the genuine optional expected identity and scrub before returning private code',()=>{
 const id='srv_'+'A'.repeat(43),kept:string[]=[];
 const history={replaceState:(_state:unknown,_title:string,path?:string|URL|null)=>{kept.push(String(path));}};
 const url=new URL(accountMoveLink('https://server.test',code,id));
 assert.equal(url.href,'https://server.test/sign-in#code='+code+'&id='+id);
 assert.deepEqual(consumeAccountMoveFragment(url,history),{code,id});assert.deepEqual(kept,['/sign-in']);
 assert.deepEqual(consumeAccountMoveFragment(new URL(accountMoveLink('https://server.test',code)),history),{code});
 assert.throws(()=>consumeMoveCodeFragment(url,history),'code-only callers must not discard identity');
 for(const hash of ['#code='+code+'&id=wrong','#code='+code+'&id='+id+'&id='+id,'#code='+code+'&id=','#code='+code+'&token=private']){
  assert.throws(()=>consumeAccountMoveFragment({pathname:'/sign-in',search:'',hash},history));assert.equal(kept.at(-1),'/sign-in');
 }
 assert.throws(()=>consumeAccountMoveFragment({pathname:'/sign-in',search:'?id='+id,hash:'#code='+code},history));
 assert.throws(()=>accountMoveLink('https://server.test',code,'srv_'+'B'.repeat(43)));
});

test('move-link query refusal uses original live Location values after synchronous history scrubbing',()=>{
 const id='srv_'+'A'.repeat(43);
 let current=new URL('https://server.test/sign-in?id='+id+'#code='+code);
 const location={get pathname(){return current.pathname;},get search(){return current.search;},get hash(){return current.hash;}};
 const history={replaceState:(_state:unknown,_title:string,path?:string|URL|null)=>{current=new URL(String(path),current);}};
 assert.throws(()=>consumeAccountMoveFragment(location,history));
 assert.equal(current.href,'https://server.test/sign-in');
 current=new URL('https://server.test/sign-in#code='+code+'&id='+id);
 assert.deepEqual(consumeAccountMoveFragment(location,history),{code,id});
 assert.equal(current.href,'https://server.test/sign-in');
});
