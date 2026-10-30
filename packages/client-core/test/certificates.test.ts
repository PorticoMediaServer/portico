import test from 'node:test';
import assert from 'node:assert/strict';
import {HttpLocalApi} from '../src/index.ts';
import {parseCertificateStatus,certificateError,loadCertificate,saveCertificate,readCertificateResponse} from '../src/certificates.ts';
const namespace='ptc-aaaaaaaaaaaaaaaaaaaa';
function status(){return {configured:true,authorityId:'a'.repeat(64),config:{enabled:true,publicPort:32500,publicAddress:'8.8.8.8',revision:'7'},state:'ready',environment:'production',namespace,dnsName:'*.'+namespace+'.direct.getportico.tv',tlsReady:true,publiclyTrusted:true,listenerBound:true,listenPort:32500,reachability:'probe_required'};}
const response=(raw:unknown)=>new Response(JSON.stringify(raw),{headers:{'Content-Type':'application/json'}});
test('certificate projection never invents reachability and drops unrecognized secrets',()=>{
 const s=parseCertificateStatus({...status(),csr:'secret',providerResponse:{token:'secret'}});
 assert.equal(s.reachability,'probe_required');assert.equal('csr' in s,false);assert.equal('providerResponse' in s,false);
 assert.ok(Object.isFrozen(s.config));
 assert.throws(()=>parseCertificateStatus({...status(),reachability:'verified'}));
 assert.throws(()=>parseCertificateStatus({...status(),environment:'staging',publiclyTrusted:true}));
 assert.throws(()=>parseCertificateStatus({...status(),namespace:'pca-'+ 'a'.repeat(52)}));
 assert.throws(()=>parseCertificateStatus({...status(),namespace:'ptc-'+ 'b'.repeat(20)}));
});
test('configuration preserves exact revision and authority fence, selected origin and bearer',async()=>{
 const calls:{url:string;init:RequestInit|undefined}[]=[];
 const api=new HttpLocalApi('https://selected.example:32500','owner-token',async(url,init)=>{calls.push({url:String(url),init});return response(status());});
 const s=await loadCertificate(api,new AbortController().signal);
 await saveCertificate(api,s,{...s.config,publicPort:4443,revision:'999'},new AbortController().signal);
 assert.equal(calls[1].url,'https://selected.example:32500/v1/networking/certificate/config');
 assert.equal(new Headers(calls[1].init?.headers).get('Authorization'),'Bearer owner-token');
 const b=JSON.parse(String(calls[1].init?.body));assert.equal(b.authorityId,s.authorityId);assert.equal(b.config.revision,'7');assert.equal(b.config.publicPort,4443);
 assert.equal(calls[1].init?.redirect,'error');assert.equal(calls[1].init?.credentials,'omit');
});
test('CD-10: signed config uses Go wire order and a read-edit-save roundtrip',async()=>{
 const bodies:string[]=[];
 const api=new HttpLocalApi('https://selected.example:32500','owner-token',async(_url,init)=>{bodies.push(String(init?.body));return response(status());});
 const s=await loadCertificate(api,new AbortController().signal);
 // Parsed projection already uses wire order.
 assert.deepEqual(Object.keys((s as any).config),['enabled','publicPort','publicAddress','revision']);
 await saveCertificate(api,s,{enabled:false,publicAddress:'9.9.9.9',publicPort:4443,revision:'ignored'},new AbortController().signal);
 const raw=bodies[1]!;
 // Outer authorityId then config; inner enabled, publicPort, publicAddress, revision.
 assert.deepEqual(Object.keys(JSON.parse(raw)),['authorityId','config']);
 assert.deepEqual(Object.keys(JSON.parse(raw).config),['enabled','publicPort','publicAddress','revision']);
 assert.ok(raw.indexOf('"enabled"')<raw.indexOf('"publicPort"')&&raw.indexOf('"publicPort"')<raw.indexOf('"publicAddress"')&&raw.indexOf('"publicAddress"')<raw.indexOf('"revision"'));
 const parsed=JSON.parse(raw);
 assert.equal(parsed.config.revision,'7','current revision wins, never the edited one');
 assert.equal(parsed.config.enabled,false);
 assert.equal(parsed.config.publicPort,4443);
});
test('late response is discarded after session replacement, including A-to-B-to-A',async()=>{
 let finish!:(r:Response)=>void;
 const api=new HttpLocalApi('https://selected.example','one',()=>new Promise(resolve=>{finish=resolve;}));
 const pending=loadCertificate(api,new AbortController().signal);
 api.setAccessToken('two');api.setAccessToken('one');finish(response(status()));
 await assert.rejects(pending,/session changed/);
});
test('buffered native response is bounded, and errors never echo provider diagnostics',async()=>{
 const native={ok:true,headers:new Headers({'Content-Type':'application/json'}),body:null,text:async()=>JSON.stringify(status())} as Response;
 assert.equal(parseCertificateStatus(await readCertificateResponse(native,new AbortController().signal)).namespace,namespace);
 await assert.rejects(readCertificateResponse({...native,text:async()=> 'x'.repeat(16385)} as Response,new AbortController().signal));
 assert.match(certificateError({status:401,message:'secret'}),/local owner/);
 assert.doesNotMatch(certificateError({status:500,message:'secret'}),/secret/);
});
test('route metadata keeps an explicit arbitrary valid HTTPS port and never exposes an arbitrary link',()=>{
 const host='current.'+namespace+'.direct.getportico.tv';
 assert.equal(parseCertificateStatus({...status(),routeHostname:host,routeUrl:'https://'+host+':32500'}).routeHostname,host);
 assert.throws(()=>parseCertificateStatus({...status(),routeHostname:host,routeUrl:'https://user:password@'+host+':32500'}));
 assert.throws(()=>parseCertificateStatus({...status(),routeHostname:host,routeUrl:'https://'+host+':443'}));
});


test('certificate session subscriptions invalidate cached owner UI across token changes',()=>{
 const api=new HttpLocalApi('https://server.example:32500','a');
 const seen:number[]=[];
 const unsubscribe=api.subscribeCertificateSession(()=>seen.push(api.getCertificateSessionEpoch()));
 api.setAccessToken('b');api.setAccessToken('a');api.setAccessToken('a');
 assert.deepEqual(seen,[1,2]);
 unsubscribe();api.setAccessToken('');assert.deepEqual(seen,[1,2]);
});
test('the direct domain is operator configuration: portico.direct and its route names parse, other namespaces do not',()=>{
 const zone=namespace+'.portico.direct',host='66-94-102-39.'+zone;
 const s=parseCertificateStatus({...status(),dnsName:'*.'+zone,routeHostname:host,routeUrl:'https://'+host+':32500'});
 assert.equal(s.dnsName,'*.'+zone);
 assert.throws(()=>parseCertificateStatus({...status(),dnsName:'*.ptc-bbbbbbbbbbbbbbbbbbbb.portico.direct'}));
 assert.throws(()=>parseCertificateStatus({...status(),dnsName:'*.'+namespace}));
 assert.throws(()=>parseCertificateStatus({...status(),dnsName:'*.'+zone,routeHostname:'a.b.'+zone,routeUrl:'https://a.b.'+zone+':32500'}));
 assert.throws(()=>parseCertificateStatus({...status(),dnsName:'*.'+zone,routeHostname:'x.'+namespace+'.direct.getportico.tv',routeUrl:'https://x.'+namespace+'.direct.getportico.tv:32500'}));
});
