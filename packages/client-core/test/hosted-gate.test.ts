import test from 'node:test';import assert from 'node:assert/strict';
import {ApiError,CredentialSessionService,HostedGate,hostedGate,type CredentialRecord,type HostedSession} from '../src/index.ts';

const clock=(start=1_800_000_000_000)=>{let now=start,random=0;return{now:()=>now,random:()=>random,advance:(ms:number)=>{now+=ms;},roll:(v:number)=>{random=v;}};};

test('a person always gets through; the app\'s own requests wait for a recovering service',async()=>{
 const c=clock(),gate=new HostedGate(c);let calls=0;
 await assert.rejects(gate.run('interactive',async()=>{calls++;throw new ApiError(503,'unavailable','down',true);}));
 assert.ok(gate.blockedFor()>=10000,'one failure opens the circuit');
 // Automatic work is refused locally: the service is not touched.
 await assert.rejects(gate.run('automatic',async()=>{calls++;return 1;}),(e:unknown)=>e instanceof ApiError&&e.code==='account_service_recovering'&&e.retryable&&(e.retryAfterSeconds??0)>0);
 assert.equal(calls,1);
 // A tap goes through regardless, and a success closes the circuit for everyone.
 assert.equal(await gate.run('interactive',async()=>{calls++;return 'ok';}),'ok');
 assert.equal(gate.blockedFor(),0);assert.equal(await gate.run('automatic',async()=>7),7);
});

test('backoff grows, jitter is proportional, and Retry-After is a floor with spread beyond it',()=>{
 const c=clock(),gate=new HostedGate(c);
 c.roll(0);gate.failed();assert.equal(gate.blockedFor(),10000);
 for(let i=0;i<20;i++)gate.failed();
 assert.equal(gate.blockedFor(),5000*2**8,'capped at ~21 minutes');
 const late=new HostedGate(c);c.roll(0.999);for(let i=0;i<8;i++)late.failed();
 assert.ok(late.blockedFor()-5000*2**8>5*60000,'at the cap a herd is spread by minutes, not seconds');
 // Everyone told "come back in an hour" must not come back in the same second.
 const a=new HostedGate(c),b=new HostedGate(c);c.roll(0);a.failed(3600);c.roll(0.9);b.failed(3600);
 assert.ok(a.blockedFor()>=3600000&&b.blockedFor()>a.blockedFor());
});

test('an answer about this request is not an outage',async()=>{
 const gate=new HostedGate(clock());
 for(const status of [400,401,403,404,409,422]){await assert.rejects(gate.run('interactive',async()=>{throw new ApiError(status,'refused','no');}));assert.equal(gate.blockedFor(),0,String(status));}
 await assert.rejects(gate.run('interactive',async()=>{throw new ApiError(429,'capacity_limited','busy',true,7);}));assert.ok(gate.blockedFor()>=7000);
 const net=new HostedGate(clock());await assert.rejects(net.run('interactive',async()=>{throw new TypeError('fetch failed');}));assert.ok(net.blockedFor()>0,'a network failure counts');
 const aborted=new HostedGate(clock());const abort=new Error('aborted');abort.name='AbortError';await assert.rejects(aborted.run('interactive',async()=>{throw abort;}));assert.equal(aborted.blockedFor(),0,'a cancelled request says nothing about the service');
});

test('identical automatic requests share one flight, and origins share one gate',async()=>{
 const gate=new HostedGate(clock());let calls=0,release:(()=>void)|undefined;
 const request=()=>{calls++;return new Promise<number>(r=>{release=()=>r(42);});};
 const one=gate.run('automatic',request,'routes:srv'),two=gate.run('automatic',request,'routes:srv');
 await new Promise(r=>setTimeout(r,5));release!();assert.deepEqual(await Promise.all([one,two]),[42,42]);assert.equal(calls,1);
 assert.equal(hostedGate('https://account.example'),hostedGate('https://account.example/'));
 assert.notEqual(hostedGate('https://account.example'),hostedGate('https://other.example'));
});

const session=(refreshInDays:number,token='one'):HostedSession=>({accessToken:'access-'+token,refreshToken:'refresh-'+token,familyId:'family',expiresAt:new Date(Date.now()+3600000).toISOString(),refreshExpiresAt:new Date(Date.now()+refreshInDays*86400000).toISOString(),account:{id:'account',username:'u',displayName:'U'}} as HostedSession);
function store(){let value:CredentialRecord|undefined;return{load:async()=>structuredClone(value),save:async(v:CredentialRecord)=>{value=structuredClone(v);}};}

test('keep-alive renews a sign-in only once a third of its window is used, and defers to the circuit',async()=>{
 let refreshes=0;const gate=new HostedGate(clock(Date.now()));
 const api={gate,authorityFromSession:(s:HostedSession)=>s.refreshToken!,refresh:async()=>{refreshes++;return session(90,'two');},revoke:async()=>{}};
 const fresh=new CredentialSessionService(api,store(),async()=>crypto.randomUUID());await fresh.adopt(session(80));
 assert.equal(await fresh.keepAlive(),false,'80 days left: nothing to do');assert.equal(refreshes,0);
 const aging=new CredentialSessionService(api,store(),async()=>crypto.randomUUID());await aging.adopt(session(45));
 gate.failed();assert.equal(await aging.keepAlive(),false,'the service is recovering: do not add to it');assert.equal(refreshes,0);
 gate.succeeded();assert.equal(await aging.keepAlive(),true);assert.equal(refreshes,1);
 assert.equal(await aging.keepAlive(),false,'renewed to 90 days: quiet again');assert.equal(refreshes,1);
});

test('a keep-alive that fails never signs anyone out',async()=>{
 const api={gate:new HostedGate(clock(Date.now())),authorityFromSession:(s:HostedSession)=>s.refreshToken!,refresh:async()=>{throw new ApiError(503,'unavailable','down',true);},revoke:async()=>{}};
 const service=new CredentialSessionService(api,store(),async()=>crypto.randomUUID());await service.adopt(session(10));
 assert.equal(await service.keepAlive(),false);assert.notEqual(service.getSnapshot().phase,'signedOut');assert.ok(service.getSnapshot().session);
});
