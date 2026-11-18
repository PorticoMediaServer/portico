import assert from 'node:assert/strict';
import test from 'node:test';
import {
 accessTokenPrefix,apiKeyPrefix,apiScopePresets,createAccessToken,createApiKey,getApiAccessOptions,isApiCredentialSecret,listApiKeys,listAccessTokens,revokeAccessToken,listWebhookDeliveries,
 parseApiAccessOptions,parseApiCredential,parseIssuedApiCredential,parseIssuedWebhook,parseWebhook,parseWebhookDelivery,parseWebhookDeliveryPage,parseWebhookList,
 presetOf,revokeApiKey,sendWebhookTest,updateWebhook,webhookEdits,type ApiAccessTransport,
} from '../src/api-access.ts';
import {HttpLocalApi} from '../src/http.ts';

const member={accountId:'owner',username:'sam',profileId:'p1',profile:'Sam'};
const credential={id:'c1',kind:'key',name:'Automation',scopes:['read','playback'],hint:'abc123',createdAt:'2026-10-13T12:00:00Z',state:'active',member};

function recorder(answer:unknown){
 const calls:{path:string;method?:string;body?:unknown}[]=[];
 const transport:ApiAccessTransport={request:async<T>(path:string,method?:string,body?:unknown)=>{calls.push({path,method,body});return answer as T;}};
 return {calls,transport};
}

test('a listed credential never carries a secret, and an issued one carries the right kind of secret', () => {
 const listed=parseApiCredential({...credential,lastUsedAt:'2026-10-13T13:00:00Z',expiresAt:'2027-01-01T00:00:00Z'});
 assert.deepEqual(listed.scopes,['read','playback']);
 assert.equal(listed.lastUsedAt,'2026-10-13T13:00:00Z');
 assert.throws(()=>parseApiCredential({...credential,secret:apiKeyPrefix+'x'}));
 assert.throws(()=>parseApiCredential({...credential,scopes:[]}));
 assert.throws(()=>parseApiCredential({...credential,scopes:['everything']}));
 assert.throws(()=>parseApiCredential({...credential,state:'unknown'}));
 assert.throws(()=>parseApiCredential({...credential,member:undefined}));
 const issued=parseIssuedApiCredential({credential,secret:apiKeyPrefix+'secret'});
 assert.equal(issued.secret,apiKeyPrefix+'secret');
 assert.ok(isApiCredentialSecret(issued.secret!));
 assert.ok(isApiCredentialSecret(accessTokenPrefix+'x'));
 assert.ok(!isApiCredentialSecret('a-session-token'));
 // A key whose secret looks like a token, or like nothing, is not trusted.
 assert.throws(()=>parseIssuedApiCredential({credential,secret:accessTokenPrefix+'secret'}));
 assert.throws(()=>parseIssuedApiCredential({credential,secret:'plain'}));
 // A retried create names the credential without a secret.
 assert.equal(parseIssuedApiCredential({credential}).secret,undefined);
});

test('scope presets map to exact scope sets and back', () => {
 assert.equal(presetOf(['playback','read']),'playback');
 assert.equal(presetOf(['admin']),'admin');
 assert.equal(presetOf(['library']),undefined);
 assert.deepEqual(apiScopePresets.library,['read','playback','library']);
 const options=parseApiAccessOptions({scopes:[{id:'read'},{id:'playback'},{id:'library'},{id:'admin'}],maximumKeys:100,maximumTokens:25,tokensAllowed:false,requestsPerMinute:600,queryParameter:'apiKey'});
 assert.equal(options.tokensAllowed,false);
 // A scope this app does not know is not offered; a list of keys holding one is refused whole, never shown with a guessed scope.
 assert.deepEqual([...parseApiAccessOptions({scopes:[{id:'session'},{id:'read'},{id:'read'}],maximumKeys:1,maximumTokens:1,tokensAllowed:true,requestsPerMinute:1,queryParameter:'apiKey'}).scopes],['read']);
 assert.throws(()=>parseApiAccessOptions({scopes:[{id:'read'}],maximumKeys:-1,maximumTokens:1,tokensAllowed:true,requestsPerMinute:1,queryParameter:'apiKey'}));
});

test('credential services call the documented routes', async () => {
 const page=recorder({items:[credential],limit:100,total:1});
 assert.equal((await listApiKeys(page.transport,'next page')).items.length,1);
 assert.equal(page.calls[0].path,'/v1/admin/api-keys?limit=100&cursor=next%20page');
 const made=recorder({credential,secret:apiKeyPrefix+'s'});
 await createApiKey(made.transport,{operationId:'op-12345678',name:'Automation',scopes:['read']});
 assert.deepEqual(made.calls[0],{path:'/v1/admin/api-keys',method:'POST',body:{operationId:'op-12345678',name:'Automation',scopes:['read']}});
 const revoked=recorder({...credential,state:'revoked'});
 assert.equal((await revokeApiKey(revoked.transport,'c/1')).state,'revoked');
 assert.deepEqual(revoked.calls[0],{path:'/v1/admin/api-keys/c%2F1',method:'DELETE',body:undefined});
});

test('the personal token panel lists, creates and revokes through the rebuilt server', async () => {
 const key={id:'own/token',name:'Mine',scopes:['read'],createdAt:'2026-10-13T12:00:00Z'};
 const calls: unknown[][]=[];
 const api=new HttpLocalApi('https://server.test','viewer',async (input,init)=>{
  const path=new URL(String(input)).pathname, method=init?.method;
  const body=init?.body&&JSON.parse(String(init.body));
  calls.push([method,path,body,new Headers(init?.headers).get('Idempotency-Key')]);
  if(path==='/v1/me/api-access')return Response.json({error:{code:'not_found',message:'Not found',retry:false}},{status:404});
  if(method==='GET')return Response.json({items:[key]});
  if(method==='POST')return Response.json({key,secret:'pk_example'});
  return new Response(null,{status:204});
 });
 const list=await listAccessTokens(api);
 assert.equal(list.items[0]?.id,key.id);
 assert.equal(list.nextCursor,undefined);
 assert.equal(await getApiAccessOptions(api),undefined);
 const receipt=await createAccessToken(api,{operationId:'same-draft',name:'Mine',scopes:['read']});
 assert.equal(receipt.key.id,key.id);
 assert.equal(receipt.secret,'pk_example');
 await revokeAccessToken(api,key.id);
 assert.deepEqual(calls,[
  ['GET','/v1/me/tokens',undefined,null],
  ['GET','/v1/me/api-access',undefined,null],
  ['POST','/v1/me/tokens',{name:'Mine',scopes:['read']},'same-draft'],
  ['DELETE','/v1/me/tokens/own%2Ftoken',{},null],
 ]);
});

const webhook={id:'w1',name:'Home',url:'http://192.168.1.2/hook',events:['playback.started','a.future.event'],enabled:true,createdAt:'2026-10-13T12:00:00Z',waiting:0,dropped:1};

test('current webhook pages and receipts keep events and distinct log rows', async () => {
 assert.deepEqual(parseWebhook(webhook).events,['playback.started','a.future.event']);
 assert.throws(()=>parseWebhook({...webhook,secret:'whsec_x'}));
 assert.throws(()=>parseWebhook({...webhook,waiting:-1}));
 assert.equal(parseIssuedWebhook({webhook,secret:'whsec_x'}).secret,'whsec_x');
 assert.throws(()=>parseIssuedWebhook({webhook}));
 assert.equal(parseWebhookList({items:[webhook]}).items.length,1);
 const delivery={event:'test',state:'waiting',tries:1,createdAt:'2026-10-13T12:00:00Z',nextTryAt:'2026-10-13T12:00:30Z',status:500,error:'status_500'};
 assert.equal(parseWebhookDelivery(delivery).error,'status_500');
 assert.throws(()=>parseWebhookDelivery({...delivery,state:'lost'}));
 assert.equal(parseWebhookDeliveryPage({items:[delivery,{...delivery,state:'lost'},delivery]}).items.length,2);
 assert.throws(()=>parseWebhookList({items:[webhook,{...webhook,id:'w2',waiting:-1}]}));
 const made=recorder(webhook);
 await updateWebhook(made.transport,'w1',{enabled:false});
 assert.deepEqual(made.calls[0],{path:'/v1/admin/webhooks/w1',method:'PATCH',body:{enabled:false}});
 const result=recorder({delivered:false,status:503});
 assert.equal((await sendWebhookTest(result.transport,'w1')).delivered,false);
 const log=recorder({items:[delivery]});
 await listWebhookDeliveries(log.transport,'w1',{cursor:'c'});
 assert.equal(log.calls[0].path,'/v1/admin/webhooks/w1/deliveries?cursor=c');
});

test('webhook form sends only changed fields', () => {
 const was=parseWebhook(webhook);
 assert.deepEqual(webhookEdits(was,{name:'Home',url:webhook.url,events:['a.future.event','playback.started']}),{});
 assert.deepEqual(webhookEdits(was,{name:'Hall',url:webhook.url,events:[...was.events]}),{name:'Hall'});
});

test('a credential page carries the exact total across pages and refuses a page without one', async () => {
 const page=recorder({items:[credential],limit:100,total:250});
 assert.equal((await listApiKeys(page.transport)).total,250);
 await assert.rejects(listApiKeys(recorder({items:[credential],limit:100}).transport));
 await assert.rejects(listApiKeys(recorder({items:[credential],limit:100,total:-1}).transport));
});
