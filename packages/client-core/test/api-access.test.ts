import assert from 'node:assert/strict';
import test from 'node:test';
import {
 accessTokenPrefix,apiKeyPrefix,apiScopePresets,createAccessToken,createApiKey,isApiCredentialSecret,listApiKeys,listAccessTokens,listWebhookDeliveries,
 parseApiAccessOptions,parseApiCredential,parseIssuedApiCredential,parseIssuedWebhook,parsePathReport,parseTitlePage,parseWebhook,parseWebhookDelivery,parseWebhookDeliveryPage,parseWebhookList,
 presetOf,queryTitles,reportChangedPaths,refreshIntegrationLibraries,revokeApiKey,sendWebhookTest,updateWebhook,webhookEdits,type ApiAccessTransport,
} from '../src/api-access.ts';

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
 await listAccessTokens(page.transport,'own next page');
 assert.equal(page.calls[1].path,'/v1/me/access-tokens?limit=100&cursor=own%20next%20page');
 const made=recorder({credential,secret:apiKeyPrefix+'s'});
 await createApiKey(made.transport,{operationId:'op-12345678',name:'Automation',scopes:['read']});
 assert.deepEqual(made.calls[0],{path:'/v1/admin/api-keys',method:'POST',body:{operationId:'op-12345678',name:'Automation',scopes:['read']}});
 const token=recorder({credential:{...credential,kind:'token'},secret:accessTokenPrefix+'s'});
 await createAccessToken(token.transport,{operationId:'op-12345678',name:'Mine',scopes:['read'],expiresAt:'2027-01-01T00:00:00Z'});
 assert.deepEqual(token.calls[0].body,{operationId:'op-12345678',name:'Mine',scopes:['read'],expiresAt:'2027-01-01T00:00:00Z'});
 const revoked=recorder({...credential,state:'revoked'});
 assert.equal((await revokeApiKey(revoked.transport,'c/1')).state,'revoked');
 assert.deepEqual(revoked.calls[0],{path:'/v1/admin/api-keys/c%2F1',method:'DELETE',body:undefined});
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

test('the title query and the paths-changed report', async () => {
 const row={id:'t1',kind:'movie',title:'A Film',libraryId:'movies',year:2001,available:true,providerIds:{tmdb:'603'}};
 assert.equal(parseTitlePage({items:[row],limit:50,nextCursor:'n'}).items[0].providerIds.tmdb,'603');
 // One title that cannot be read is left out alone; the page size and the cursor stay exact.
 assert.deepEqual(parseTitlePage({items:[{...row,providerIds:undefined},{...row,available:'yes'},row,{...row,title:'Again'}],limit:50,futureField:1}).items.map(t=>t.title),['A Film']);
 assert.throws(()=>parseTitlePage({items:[row],limit:-1}));
 assert.throws(()=>parseTitlePage({items:Array(101).fill(row),limit:50}));
 const titles=recorder({items:[row],limit:2});
 await queryTitles(titles.transport,{library:'movies',kind:'movie',sort:'year',order:'desc',yearFrom:2000,limit:2});
 assert.equal(titles.calls[0].path,'/v1/titles?library=movies&kind=movie&sort=year&order=desc&yearFrom=2000&limit=2');
 const report=recorder({paths:[{path:'/media/a',status:'queued',libraryId:'movies',sourceId:'s',folder:'a'},{path:'/x',status:'outside_libraries'}],jobs:[{id:'j1',status:'queued',processed:0,analyzed:0,warnings:0}]});
 const out=await reportChangedPaths(report.transport,[{path:'/media/a',change:'added'},{path:'/x'}]);
 assert.deepEqual(report.calls[0].body,{paths:[{path:'/media/a',change:'added'},{path:'/x'}]});
 assert.deepEqual(out.jobIds,['j1']);
 assert.equal(out.paths[1].status,'outside_libraries');
 assert.throws(()=>parsePathReport({paths:[{path:'/x',status:'scanning'}],jobs:[]}));
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

test('deferred integration work is explicit and never invents job IDs',async()=>{
 const raw={paths:[{path:'/media/A',status:'deferred',scanScope:'source',libraryId:'films',sourceId:'source'}],jobs:[],deferred:true};
 const parsed=parsePathReport(raw);assert.equal(parsed.deferred,true);assert.deepEqual(parsed.jobIds,[]);assert.equal(parsed.paths[0].scanScope,'source');
 assert.throws(()=>parsePathReport({...raw,deferred:'yes'}));assert.throws(()=>parsePathReport({...raw,paths:[{...raw.paths[0],scanScope:'everything'}]}));
 const queued=parsePathReport({paths:[],jobs:[{id:'real'}]});assert.equal(queued.deferred,false);assert.deepEqual(queued.jobIds,['real']);
 const request=recorder(raw);await refreshIntegrationLibraries(request.transport,'films');assert.equal(request.calls[0].path,'/v1/integrations/refresh');assert.deepEqual(request.calls[0].body,{libraryId:'films'});
});
