import assert from 'node:assert/strict';
import test from 'node:test';
import {AccountAttemptIds,HostedAccountClient,MFARequiredError,accountReturnHandoff,nativeAccountReturn,parseAccountReturn,parseAccountSession,parseNativeAccountContinuation,parseSecurityResult,parseSecurityState,parseIdentityOptions,parseIdentityTransaction,providerRedirect,CredentialSessionService,type CredentialApi,type CredentialRecord,type HostedSession} from '../src/index.ts';
const session:HostedSession={account:{id:'acct_A',username:'alice',displayName:'Alice'},familyId:'family_A',accessToken:'access_A',expiresAt:'2030-01-01T00:00:00Z',refreshExpiresAt:'2030-01-08T00:00:00Z',refreshToken:'refresh_A'};
const mfa={mfaRequired:true,challengeId:'challenge_A',challengeToken:'opaque_factor',expiresAt:'2030-01-01T00:00:00Z'};
test('session parser separates browser/native credentials and MFA continuation',()=>{
 assert.deepEqual(parseAccountSession(session,'native'),session);
 const {refreshToken,...browser}=session;assert.deepEqual(parseAccountSession(browser,'browser'),browser);
 assert.throws(()=>parseAccountSession(session,'browser'));assert.throws(()=>parseAccountSession(browser,'native'));
 assert.throws(()=>parseAccountSession(mfa,'native'),MFARequiredError);
 assert.throws(()=>parseAccountSession({...session,profiles:[]},'native'));
});
test('security responses cannot be applied to another account or family',()=>{
 const state={accountId:'acct_A',familyId:'family_A',contactAddress:'alice@example.test',privateEmail:false,hasPassword:true,mfaEnabled:false,recoveryCodesRemaining:0,providers:[]};
 const scope={accountId:'acct_A',familyId:'family_A'};assert.deepEqual(parseSecurityState(state,scope),state);
 assert.throws(()=>parseSecurityState(state,{...scope,accountId:'acct_B'}));assert.throws(()=>parseSecurityState(state,{...scope,familyId:'family_B'}));
 assert.throws(()=>parseSecurityResult({...scope,status:'pending',otpAuthUri:'javascript:alert(1)'},scope));
 assert.equal(parseSecurityResult({...scope,status:'pending',otpAuthUri:'otpauth://totp/Portico:alice?secret=AAAA'},scope).status,'pending');
});
test('email-change resend retains the original challenge and checked account scope',async()=>{
 const scope={accountId:'acct_A',familyId:'family_A'};
 const result={...scope,status:'contact_pending',challengeId:'email_A',maskedAddress:'a***@example.test',resendsRemaining:4,expiresAt:'2030-01-01T00:00:00Z'};
 let response:unknown=result;
 const calls:{path:string;body:unknown;token?:string;signal?:AbortSignal}[]=[];
 const client=new HostedAccountClient('https://accounts.example.test','browser',async(path,_method,body,signal,token)=>{calls.push({path,body,signal,token});return response;});
 const controller=new AbortController();
 assert.equal((await client.resendEmailChange('email_A','resend_operation',scope,'token_A',controller.signal)).resendsRemaining,4);
 assert.deepEqual(calls[0],{path:'/v1/account/security/email/resend',body:{challengeId:'email_A',operationId:'resend_operation'},token:'token_A',signal:controller.signal});
 for(const patch of [{familyId:'family_B'},{accountId:'acct_B'},{challengeId:'email_B'},{status:'updated'},{signOut:true},{maskedAddress:undefined},{expiresAt:undefined},{resendsRemaining:undefined},{resendsRemaining:-1},{resendsRemaining:6},{resendsRemaining:1.5}]){
  response={...result,...patch};
  await assert.rejects(client.resendEmailChange('email_A','resend_operation',scope,'token_A'));
 }
});
test('native return carries only the expected public transaction ID',()=>{
 assert.equal(nativeAccountReturn('portico://account-return?transactionId=tx_A','tx_A'),'tx_A');
 for(const url of ['portico://account-return?transactionId=tx_B','portico://account-return/?transactionId=tx_A','portico://account-return?transactionId=tx_A&token=secret','portico://account-return?transactionId=tx_A&transactionId=tx_A','https://account-return?transactionId=tx_A','portico://evil@account-return?transactionId=tx_A','portico://account-return?transactionId=tx_A#secret'])assert.throws(()=>nativeAccountReturn(url,'tx_A'));
});
test('SEC-01: a current service returns with a completion code; an older one without',()=>{
 const code='C'.repeat(43),origin='https://accounts.example.com';
 // Older service: the transaction only.
 assert.deepEqual(parseAccountReturn('portico://account-return?transactionId=tx_A','tx_A',origin),{transactionId:'tx_A'});
 // Current service, continued by the account-return page into the sign-in browser.
 assert.deepEqual(parseAccountReturn('portico://account-return?transactionId=tx_A&code='+code,'tx_A',origin),{transactionId:'tx_A',completionCode:code});
 // Current service, as an app link on the Portico Account origin.
 assert.deepEqual(parseAccountReturn(origin+'/app/account-return#tx=tx_A&code='+code,'tx_A',origin),{transactionId:'tx_A',completionCode:code});
 assert.deepEqual(parseAccountReturn(origin+'/app/account-return#code='+code+'&tx=tx_A','tx_A',origin),{transactionId:'tx_A',completionCode:code});
 for(const url of [
  'portico://account-return?transactionId=tx_A&code=short',                          // not a code
  'portico://account-return?code='+code+'&transactionId=tx_A',                       // order is fixed
  'portico://account-return?transactionId=tx_B&code='+code,                          // another sign-in
  origin+'/app/account-return#tx=tx_A&code='+code+'&extra=1',
  origin+'/app/account-return?x=1#tx=tx_A&code='+code,
  origin+'/app/account-return#tx=tx_B&code='+code,
  'https://evil.example.com/app/account-return#tx=tx_A&code='+code,                  // another origin
  origin+'/account-return#tx=tx_A&code='+code,
 ])assert.throws(()=>parseAccountReturn(url,'tx_A',origin),url);
 // Without the account origin the app-link shape isn't accepted at all.
 assert.throws(()=>parseAccountReturn(origin+'/app/account-return#tx=tx_A&code='+code,'tx_A'));
});
test('SEC-01: the account-return page continues into the app only with a well-formed return',()=>{
 const code='D'.repeat(43);
 assert.equal(accountReturnHandoff('https://accounts.example.com/app/account-return#tx=tx_A&code='+code),'portico://account-return?transactionId=tx_A&code='+code);
 for(const href of ['https://accounts.example.com/app/account-return#tx=tx_A','https://accounts.example.com/app/account-return#tx=tx_A&code=x','https://accounts.example.com/app/account-return?tx=tx_A&code='+code,'https://accounts.example.com/other#tx=tx_A&code='+code,'https://accounts.example.com/app/account-return#tx=a%20b&code='+code])assert.equal(accountReturnHandoff(href),undefined,href);
});
test('SEC-01: the transaction view sends the completion code as X-Portico-Completion',async()=>{
 const code='E'.repeat(43),seen:(string|undefined)[]=[];
 const client=new HostedAccountClient('https://accounts.example.com','native',async(_path,_method,_body,_signal,_token,completion)=>{seen.push(completion);return {transactionId:'tx_A',provider:'google',mode:'login',status:'verified',expiresAt:'2030-01-01T00:00:00Z'};},'b'.repeat(43));
 await client.transaction('tx_A',undefined,code).catch(()=>{});
 await client.transaction('tx_A').catch(()=>{});
 assert.deepEqual(seen,[code,undefined]);
 await assert.rejects(client.transaction('tx_A',undefined,'not-a-code'));
 // The default transport puts it in the header, never the URL.
 const original=globalThis.fetch;const requests:{url:string;headers:Record<string,string>}[]=[];
 globalThis.fetch=(async(url:string,init:{headers:Record<string,string>})=>{requests.push({url,headers:init.headers});return new Response('null',{status:200});}) as typeof fetch;
 try{await new HostedAccountClient('https://accounts.example.com','native',undefined,'b'.repeat(43)).transaction('tx_A',undefined,code);}finally{globalThis.fetch=original;}
 assert.equal(requests[0]!.headers['X-Portico-Completion'],code);assert(!requests[0]!.url.includes(code));
});
test('SEC-01: the start asks for a completion code only when the service offers one',async()=>{
 const bodies:unknown[]=[];
 const client=new HostedAccountClient('https://accounts.example.com','native',async(_path,_method,body)=>{bodies.push(body);return {transactionId:'tx_A',expiresAt:'2030-01-01T00:00:00Z',mode:'login',authorizationUrl:'https://accounts.google.com/o/oauth2/auth?x=1'};},'b'.repeat(43));
 await client.start('google',{mode:'login',requestId:'r1',completion:'code'});
 await client.start('google',{mode:'login',requestId:'r2'});
 assert.equal((bodies[0] as {completion?:string}).completion,'code');
 assert.equal('completion' in (bodies[1] as object),false,'an older service refuses unknown start fields');
 const browser=new HostedAccountClient('https://accounts.example.com','browser',async(_path,_method,body)=>{bodies.push(body);return {transactionId:'tx_A',expiresAt:'2030-01-01T00:00:00Z',mode:'login',authorizationUrl:'https://accounts.google.com/o/oauth2/auth?x=1'};});
 await browser.start('google',{mode:'login',requestId:'r3',completion:'code'});
 assert.equal('completion' in (bodies[2] as object),false,'browser sign-ins return to the page, not an app');
 const options={password:true,verifiedContactRequired:false,providers:[]};
 assert.equal(parseIdentityOptions(options).nativeCompletion,undefined);
 assert.equal(parseIdentityOptions({...options,nativeCompletion:'code'}).nativeCompletion,'code');
});
test('SEC-01: the continuation keeps a completion code only with its transaction',()=>{
 const value={version:1,binding:'a'.repeat(43),expiresAt:'2030-01-01T00:00:00Z',transactionId:'tx_A',completionCode:'F'.repeat(43)};
 assert.deepEqual(parseNativeAccountContinuation(JSON.stringify(value)),value);
 assert.throws(()=>parseNativeAccountContinuation(JSON.stringify({...value,transactionId:undefined})));
 assert.throws(()=>parseNativeAccountContinuation(JSON.stringify({...value,completionCode:'short'})));
});
test('private continuation restores only allowed keys and expires',()=>{
 const value={version:1,binding:'a'.repeat(43),expiresAt:'2030-01-01T00:00:00Z',transactionId:'tx_A',mfa};assert.deepEqual(parseNativeAccountContinuation(JSON.stringify(value)),value);
 for(const extra of [{password:'secret'},{refreshToken:'secret'},{idToken:'raw-provider-assertion'}])assert.throws(()=>parseNativeAccountContinuation(JSON.stringify({...value,...extra})));
 assert.equal(parseNativeAccountContinuation(JSON.stringify({...value,expiresAt:'2000-01-01T00:00:00Z'})),undefined);
 assert.equal(parseNativeAccountContinuation(null),undefined);assert.throws(()=>parseNativeAccountContinuation('not JSON'));
});
test('provider options expose per-app Apple configuration without granting an assertion',()=>{
 const options=parseIdentityOptions({password:true,registration:true,verifiedContactRequired:true,providers:[{id:'apple',enabled:true,native:true,nativeClientIds:['tv.getportico.portico.ios','tv.getportico.portico.tv']}]});assert.equal(options.providers[0].nativeClientIds.length,2);
 assert.throws(()=>providerRedirect('https://evil.example.test/oauth','google'));
 const tx={transactionId:'tx_A',provider:'apple',mode:'login',status:'verified',expiresAt:'2030-01-01T00:00:00Z',contactVerified:true,needsUsername:true,onboardingToken:'opaque'};
 assert.equal(parseIdentityTransaction(tx,'tx_A').onboardingToken,'opaque');assert.throws(()=>parseIdentityTransaction(tx,'tx_B'));
});
test('provider email is optional and comes only from a verified current transaction fact',()=>{
 const tx={transactionId:'tx_A',provider:'apple',mode:'login',status:'onboarding',expiresAt:'2030-01-01T00:00:00Z',contactVerified:true,needsUsername:true};
 assert.equal(parseIdentityTransaction({...tx,verifiedEmail:'actual@privaterelay.appleid.com'},'tx_A').verifiedEmail,'actual@privaterelay.appleid.com');
 assert.equal(Object.hasOwn(parseIdentityTransaction({...tx,contactAddress:'account-primary@example.test',email:'raw-provider@example.test'}),'verifiedEmail'),false);
 assert.equal(Object.hasOwn(parseIdentityTransaction({...tx,contactVerified:false}),'verifiedEmail'),false);
 for(const verifiedEmail of ['',null,42,'x'.repeat(321),'address\n@example.test'])assert.throws(()=>parseIdentityTransaction({...tx,verifiedEmail}));
 for(const status of ['started','exchanging','contact_pending','failed'])assert.throws(()=>parseIdentityTransaction({...tx,status,verifiedEmail:'actual@example.test'}));
 assert.throws(()=>parseIdentityTransaction({...tx,contactVerified:false,verifiedEmail:'actual@example.test'}));
 assert.throws(()=>parseIdentityTransaction({...tx,verifiedEmail:'actual@example.test'},'tx_B'));
});
test('account requests keep the session mode and continuation shape at the API boundary',async()=>{
 const calls:{path:string;method:string;body:unknown}[]=[];
 const client=new HostedAccountClient('https://accounts.example.test','native',async(path,method,body)=>{calls.push({path,method,body});if(path==='/v1/sessions')return session;return {kind:'registration',registrationId:'reg_A',status:'contact_pending',revision:'1',maskedAddress:'a***@example.test',expiresAt:'2030-01-01T00:00:00Z',contactVerified:false};},'a'.repeat(43));
 await client.login('alice','Password1!','request_123456789');assert.deepEqual(calls[0].body,{username:'alice',password:'Password1!',requestId:'request_123456789',sessionMode:'native'});
 const pending=await client.register({username:'alice',displayName:'Alice',password:'Password1!',contactAddress:'alice@example.test',requestId:'request_123456789'});assert.equal(pending.kind,'registration');assert.equal(calls[1].path,'/v1/registrations');
 assert.throws(()=>new HostedAccountClient('http://untrusted.example.test','native',undefined,'a'.repeat(43)));
 assert.throws(()=>new HostedAccountClient('https://accounts.example.test','native'));
});
test('identical retries reuse IDs; changed input and explicit restart replace them',async()=>{
 let count=0;const ids=new AccountAttemptIds(async()=>String(++count));assert.equal(await ids.for('login',{username:'alice'}),'1');assert.equal(await ids.for('login',{username:'alice'}),'1');assert.equal(await ids.for('login',{username:'bob'}),'2');ids.clear();assert.equal(await ids.for('login',{username:'bob'}),'3');
});
test('automatic browser approval cannot overwrite an already selected account',async()=>{
 const saved:CredentialRecord={version:1,active:{session,authority:'refresh_A',context:{}},revocations:[]};
 const api:CredentialApi={authorityFromSession:s=>s.refreshToken!,refresh:async()=>session,revoke:async()=>{}};
 const service=new CredentialSessionService(api,{load:async()=>structuredClone(saved),save:async()=>{}},async()=> 'operation_id');let called=false;
 await assert.rejects(service.authenticateIfSignedOut(async()=>{called=true;return {...session,account:{...session.account,id:'acct_B'}};}),{code:'account_changed'});assert.equal(called,false);assert.equal(service.getSnapshot().session?.account.id,'acct_A');
});

test('A80: the deletion preview needs no proof; the final deletion sends the typed confirmation', async () => {
  const {HostedAccountClient} = await import('../src/hosted-security.ts');
  const calls: {path: string; method: string; body: any; token?: string}[] = [];
  const preview = {deletionToken: 'tok_' + 'a'.repeat(20), expiresAt: '2026-09-23T08:00:00Z', ownedServers: [{id: 'srv_1', name: 'Home'}], affectedServers: [{id: 'srv_1', name: 'Home'}, {id: 'srv_2', name: 'Cabin'}], canDelete: true};
  const client = new HostedAccountClient('https://account.example', 'browser', async (path, method, body, _signal, token) => {
    calls.push({path, method, body, token});
    if (path === '/v1/account/deletion/preview') return preview;
    if (path === '/v1/account/deletion') return {accountDeleted: true, deletionId: 'del_1', status: 'accepted', pendingServers: ['srv_1'], acceptedAt: '2026-09-23T07:00:00Z', receiptExpiresAt: '2026-10-23T07:00:00Z'};
    throw new Error('unexpected ' + path);
  });
  const p = await client.deletionPreview('op_' + 'b'.repeat(20), 'access');
  assert.deepEqual(calls[0], {path: '/v1/account/deletion/preview', method: 'POST', body: {purpose: 'account_deletion_preview', target: '', operationId: 'op_' + 'b'.repeat(20)}, token: 'access'});
  assert.equal('proof' in calls[0]!.body, false);
  const receipt = await client.acceptDeletion(p, '  Sam@Example.com ', 'access');
  assert.deepEqual(calls[1]!.body, {deletionToken: preview.deletionToken, disposition: '', confirmation: 'Sam@Example.com'});
  assert.equal(receipt.accountDeleted, true);
  await assert.rejects(() => client.acceptDeletion(p, '   ', 'access'));
});
