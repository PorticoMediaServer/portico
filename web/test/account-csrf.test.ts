import test from 'node:test';
import assert from 'node:assert/strict';
import {browserCsrfCookieName} from '../src/platform/account-csrf.ts';
import {BrowserAccountApi} from '../src/platform/account.ts';
const canonical='https://web.getportico.tv';
const names=['__Host-portico_refresh_csrf','__Host-portico_refresh_043ce85acde96e8215de9e465a964463_csrf','__Host-portico_refresh_b799de072b363fb7989009bd94fd4a0b_csrf','portico_refresh_dev_7064b4bcb47fd41e41bb122a10940227_csrf','portico_refresh_dev_csrf'];
test('only closed Hosted CSRF cookie names are accepted as metadata',()=>{
  for(const name of names)assert.equal(browserCsrfCookieName(name),name);
  for(const name of [null,'','portico_refresh_csrf','__Host-portico_refresh_other_csrf','__Host-portico_refresh_csrf; other=value','__Host-portico_refresh_'+ 'A'.repeat(32)+'_csrf'])assert.equal(browserCsrfCookieName(name),undefined);
});
function browser(t:import('node:test').TestContext){
  const values=new Map<string,string>(),page={cookie:''},location={origin:canonical};
  for(const [key,value] of Object.entries({location,document:page,localStorage:{getItem:(key:string)=>values.get(key)??null,setItem:(key:string,value:string)=>values.set(key,value)}})){
    const before=Object.getOwnPropertyDescriptor(globalThis,key);
    Object.defineProperty(globalThis,key,{configurable:true,value});
    t.after(()=>{if(before)Object.defineProperty(globalThis,key,before);else delete (globalThis as Record<string,unknown>)[key];});
  }
  const sent:{path:string;token:string|undefined;body:any;method:string;init:RequestInit}[]=[];
  let header:string|undefined,name:string|undefined=names[0];
  t.mock.method(globalThis,'fetch',async(url:unknown,init:RequestInit)=>{
    sent.push({path:String(url),method:init.method!,init,token:new Headers(init.headers).get('X-CSRF-Token')??undefined,body:init.body&&JSON.parse(String(init.body))});
    const headers={...(name?{'X-Portico-CSRF-Cookie':name}:{}),...(header?{'X-CSRF-Token':header}:{})};
    if(init.method==='OPTIONS')return new Response(null,{status:204,headers});
    if(String(url).endsWith('/v1/sessions/refresh'))return new Response(JSON.stringify({account:{id:'account',username:'person',displayName:'Person'},accessToken:'synthetic-access',familyId:'exact-family',expiresAt:new Date(Date.now()+60000).toISOString(),refreshExpiresAt:new Date(Date.now()+120000).toISOString()}),{headers});
    return new Response(null,{status:204,headers});
  });
  return {location,page,values,sent,metadata:(value:string|undefined)=>{name=value;},reply:(token:string|undefined)=>{header=token;}};
}
test('refresh and revoke use the exact original cookie with either foreign-cookie ordering',async t=>{
  const env=browser(t),token='a'.repeat(43),foreign='b'.repeat(43);
  for(const [index,name] of names.entries()){
    env.metadata(name);
    const caller=index>=3?'http://127.0.0.1:19412':canonical;
    env.values.delete('portico.account.csrf.v2:'+JSON.stringify([caller,caller])+':name');
    const api=new BrowserAccountApi(caller,{callerOrigin:caller});
    for(const first of [true,false]){
      const own=name+'='+token,other='__Host-portico_refresh_foreign_csrf='+foreign;
      env.page.cookie=first?other+'; '+own:own+'; '+other;
      await api.refresh({familyId:'exact-family'},'exact-intent');await api.revoke({familyId:'exact-family'});
      for(const sent of env.sent.slice(-2)){assert.equal(sent.token,token,caller);assert.equal(sent.body.familyId,'exact-family');}
    }
  }
});
test('same-host missing, malformed and duplicate expected cookies cannot borrow a stored or foreign token',async t=>{
  const env=browser(t),api=new BrowserAccountApi(canonical,{callerOrigin:canonical});
  const name='__Host-portico_refresh_csrf',good='a'.repeat(43);
  env.reply(good);await api.send('/v1/servers','GET');env.values.set('portico.account.csrf.v1:'+canonical,good);
  for(const cookie of ['',name+'=bad',name+'='+good+'; '+name+'='+good,name+'='+good+'; '+name+'=bad',name+'; __Host-portico_refresh_foreign_csrf='+good]){
    env.page.cookie=cookie;await api.revoke({familyId:'original'});assert.equal(env.sent.at(-1)!.token,undefined,cookie);
  }
});
test('different API hosts use only their captured caller/API header key, never document cookies',async t=>{
  const env=browser(t),one='https://one.getportico.tv',two='https://two.getportico.tv';
  Object.defineProperty(env.page,'cookie',{get(){throw new Error('foreign document cookie read');}});
  const first=new BrowserAccountApi(canonical,{callerOrigin:one});
  const second=new BrowserAccountApi(canonical,{callerOrigin:two});
  const other=new BrowserAccountApi('https://api.getportico.tv',{callerOrigin:one});
  env.values.set('portico.account.csrf.v1:'+canonical,'z'.repeat(43));
  await first.refresh({familyId:'one'},'first');assert.equal(env.sent.at(-1)!.token,undefined);
  env.reply('a'.repeat(43));await first.send('/v1/servers','GET');
  env.reply('b'.repeat(43));await second.send('/v1/servers','GET');
  env.reply('c'.repeat(43));await other.send('/v1/servers','GET');env.reply(undefined);
  for(const [api,value] of [[first,'a'],[second,'b'],[other,'c'],[new BrowserAccountApi(canonical,{callerOrigin:one}),'a']] as const){
    await api.revoke({familyId:'original'});assert.equal(env.sent.at(-1)!.token,value.repeat(43));
  }
  env.values.set('portico.account.csrf.v2:'+JSON.stringify([canonical,one]),'malformed');
  await first.revoke({familyId:'original'});assert.equal(env.sent.at(-1)!.token,undefined);
});
test('reload learns the exact name once with anonymous OPTIONS, then submits the original intent once',async t=>{
  const env=browser(t),context={callerOrigin:canonical};
  const api=new BrowserAccountApi(canonical,context);context.callerOrigin='https://one.getportico.tv';
  env.page.cookie=names[0]+'='+'a'.repeat(43);
  await api.refresh({familyId:'exact-family'},'exact-intent');
  assert.equal(env.sent.length,2);
  const metadata=env.sent[0];assert.equal(metadata.method,'OPTIONS');assert.equal(metadata.init.credentials,'omit');assert.equal(metadata.init.redirect,'error');assert.equal(metadata.init.cache,'no-store');
  assert.equal(metadata.init.body,undefined);assert.equal(metadata.init.headers,undefined);
  assert.equal(env.sent[1].token,'a'.repeat(43));assert.deepEqual(env.sent[1].body,{familyId:'exact-family',requestId:'exact-intent'});
  await new BrowserAccountApi(canonical,{callerOrigin:canonical}).revoke({familyId:'exact-family'});
  assert.equal(env.sent.filter(s=>s.method==='OPTIONS').length,1,'captured scoped metadata survives reload');
});
test('missing metadata fails before a credential command; the next explicit action may try metadata again',async t=>{
  const env=browser(t),api=new BrowserAccountApi(canonical,{callerOrigin:canonical});env.metadata(undefined);
  await assert.rejects(api.revoke({familyId:'original'}));
  assert.deepEqual(env.sent.map(s=>s.method),['OPTIONS']);
  env.metadata(names[0]);env.page.cookie=names[0]+'='+'a'.repeat(43);
  await api.revoke({familyId:'original'});assert.deepEqual(env.sent.map(s=>s.method),['OPTIONS','OPTIONS','POST']);
});
test('concurrent commands share one metadata read and an aborted original never sends credentials',async t=>{
  browser(t);const api=new BrowserAccountApi(canonical,{callerOrigin:canonical});
  let release!:(response:Response)=>void;
  const wait=new Promise<Response>(resolve=>{release=resolve;});let reads=0,writes=0;
  t.mock.method(globalThis,'fetch',async(_url:unknown,init:RequestInit)=>{if(init.method==='OPTIONS'){reads++;return wait;}writes++;return new Response(null,{status:204});});
  const controller=new AbortController();
  const first=api.send('/v1/sessions/revoke','POST',{familyId:'first'},controller.signal);
  const second=api.revoke({familyId:'second'});controller.abort();
  release(new Response(null,{status:204,headers:{'X-Portico-CSRF-Cookie':names[0]}}));
  await assert.rejects(first);await second;assert.equal(reads,1);assert.equal(writes,1);await api.settled();
});

test('metadata cannot change a captured command body or learn a token from an anonymous response',async t=>{
  const env=browser(t),api=new BrowserAccountApi(canonical,{callerOrigin:canonical});
  let release!:(response:Response)=>void;
  const metadata=new Promise<Response>(resolve=>{release=resolve;});let sent:RequestInit|undefined;
  t.mock.method(globalThis,'fetch',async(_url:unknown,init:RequestInit)=>{if(init.method==='OPTIONS')return metadata;sent=init;return new Response(null,{status:204});});
  const body={familyId:'original',requestId:'original-intent'};
  const operation=api.send('/v1/sessions/revoke','POST',body);
  body.familyId='successor';body.requestId='new-intent';
  release(new Response(null,{status:204,headers:{'X-Portico-CSRF-Cookie':names[0],'X-CSRF-Token':'x'.repeat(43)}}));
  await operation;assert.deepEqual(JSON.parse(String(sent!.body)),{familyId:'original',requestId:'original-intent'});
  assert.equal(env.values.get('portico.account.csrf.v2:'+JSON.stringify([canonical,canonical])),undefined);
});

const v2=(api:string,caller:string)=>'portico.account.csrf.v2:'+JSON.stringify([api,caller]);
test('existing crosshost v1 refresh and revoke bridge only a proven document and exact API into absent v2',async t=>{
  const env=browser(t),one='https://one.getportico.tv',two='https://two.getportico.tv';
  const legacy='portico.account.csrf.v1:'+canonical;
  env.location.origin=one;env.values.set(legacy,'a'.repeat(43));
  const first=new BrowserAccountApi(canonical,{callerOrigin:one});
  await first.refresh({familyId:'exact-family'},'original-intent');await first.revoke({familyId:'exact-family'});
  assert.equal(env.sent[0].token,'a'.repeat(43));assert.equal(env.sent[1].token,'a'.repeat(43));
  assert.equal(env.values.get(v2(canonical,one)),'a'.repeat(43));assert.equal(env.values.get(legacy),'a'.repeat(43));
  env.location.origin=two;env.values.set(legacy,'b'.repeat(43));
  await new BrowserAccountApi(canonical,{callerOrigin:two}).revoke({familyId:'second'});
  assert.equal(env.sent.at(-1)!.token,'b'.repeat(43));assert.equal(env.values.get(v2(canonical,two)),'b'.repeat(43));
  assert.equal(env.values.get(v2(canonical,one)),'a'.repeat(43));
  await new BrowserAccountApi('https://api.getportico.tv',{callerOrigin:two}).revoke({familyId:'other-api'});
  assert.equal(env.sent.at(-1)!.token,undefined,'another API cannot borrow this legacy key');
});
test('legacy bridge refuses absent/mismatched real document and present malformed v2',async t=>{
  const env=browser(t),caller='https://one.getportico.tv',legacy='portico.account.csrf.v1:'+canonical;
  env.values.set(legacy,'a'.repeat(43));
  const api=new BrowserAccountApi(canonical,{callerOrigin:caller});
  for(const origin of ['',canonical,'https://two.getportico.tv']){
    env.location.origin=origin;await api.revoke({familyId:'original'});
    assert.equal(env.sent.at(-1)!.token,undefined);assert.equal(env.values.has(v2(canonical,caller)),false);
  }
  Object.defineProperty(globalThis,'location',{configurable:true,value:undefined});
  await api.revoke({familyId:'original'});assert.equal(env.sent.at(-1)!.token,undefined);
  assert.equal(env.values.has(v2(canonical,caller)),false);
  Object.defineProperty(globalThis,'location',{configurable:true,value:env.location});
  env.location.origin=caller;
  for(const value of ['','malformed']){
    env.values.set(v2(canonical,caller),value);await api.revoke({familyId:'original'});
    assert.equal(env.sent.at(-1)!.token,undefined);assert.equal(env.values.get(v2(canonical,caller)),value);
  }
  env.values.delete(v2(canonical,caller));env.values.set(legacy,'bad');await api.revoke({familyId:'original'});
  assert.equal(env.sent.at(-1)!.token,undefined);assert.equal(env.values.has(v2(canonical,caller)),false);
});
test('legacy bridge rechecks actual document before read and write; late response cannot write a foreign legacy key',async t=>{
  const env=browser(t),caller='https://one.getportico.tv',legacy='portico.account.csrf.v1:'+canonical;
  env.location.origin=caller;env.values.set(legacy,'a'.repeat(43));
  const api=new BrowserAccountApi(canonical,{callerOrigin:caller});
  const original=localStorage.getItem;
  t.mock.method(localStorage,'getItem',(key:string)=>{const value=original(key);if(key===legacy)env.location.origin='https://two.getportico.tv';return value;});
  await api.revoke({familyId:'original'});
  assert.equal(env.sent.at(-1)!.token,undefined);assert.equal(env.values.has(v2(canonical,caller)),false);
  env.reply('b'.repeat(43));await api.send('/v1/servers','GET');
  assert.equal(env.values.get(legacy),'a'.repeat(43),'retired original document cannot publish legacy state');
});
test('a new tab keeps old/new crosshost token keys current after rotation and existing v2 wins over legacy',async t=>{
  const env=browser(t),caller='https://one.getportico.tv',legacy='portico.account.csrf.v1:'+canonical;
  env.location.origin=caller;env.values.set(legacy,'a'.repeat(43));
  const api=new BrowserAccountApi(canonical,{callerOrigin:caller});
  env.reply('b'.repeat(43));await api.refresh({familyId:'exact-family'},'original-intent');env.reply(undefined);
  assert.equal(env.sent.at(-1)!.token,'a'.repeat(43));
  assert.equal(env.values.get(legacy),'b'.repeat(43),'old tabs read the new rotated header');
  assert.equal(env.values.get(v2(canonical,caller)),'b'.repeat(43));
  await new BrowserAccountApi(canonical,{callerOrigin:caller}).revoke({familyId:'exact-family'});
  assert.equal(env.sent.at(-1)!.token,'b'.repeat(43),'new tabs read the same latest header');
  env.values.set(legacy,'c'.repeat(43));await api.revoke({familyId:'exact-family'});
  assert.equal(env.sent.at(-1)!.token,'b'.repeat(43),'present valid v2 is never overwritten by bridge');
});
test('old Hosted without metadata still accepts original public/bearer commands once; protected commands fail discovery',async t=>{
  const env=browser(t),api=new BrowserAccountApi(canonical,{callerOrigin:canonical});env.metadata(undefined);
  for(const [path,body,token] of [
    ['/v1/sessions',{username:'person',password:'synthetic'},undefined],
    ['/v1/registrations',{username:'person',password:'synthetic'},undefined],
    ['/v1/account/recovery/password/reset',{code:'synthetic'},undefined],
    ['/v1/sessions/logout-all',{},'original-bearer'],
  ] as const){
    const before=env.sent.length;await api.send(path,'POST',body,undefined,token);
    assert.equal(env.sent.length,before+1);assert.equal(env.sent.at(-1)!.method,'POST');assert.equal(env.sent.at(-1)!.token,undefined);
    assert.deepEqual(env.sent.at(-1)!.body,body);
  }
  assert.equal(env.sent.filter(s=>s.method==='OPTIONS').length,0);
  for(const path of ['/v1/sessions/refresh','/v1/sessions/revoke','/v1/app-authorizations','/v1/app-authorizations/original/decision']){
    const before=env.sent.length;await assert.rejects(api.send(path,'POST',{familyId:'original'}));
    assert.deepEqual(env.sent.slice(before).map(s=>s.method),['OPTIONS'],'no credential replay or protection bypass');
  }
});

test('verified old-tab events retire only this crosshost v2 and next command uses current legacy',async t=>{
  const env=browser(t),caller='https://one.getportico.tv',legacy='portico.account.csrf.v1:'+canonical;
  env.location.origin=caller;env.values.set(legacy,'a'.repeat(43));
  const api=new BrowserAccountApi(canonical,{callerOrigin:caller});await api.revoke({familyId:'original'});
  const notify=(value:string)=>api.observeLegacyCsrf({key:legacy,newValue:value,storageArea:localStorage});
  env.values.set(legacy,'b'.repeat(43));notify('b'.repeat(43));
  assert.equal(env.values.get(v2(canonical,caller)),'a'.repeat(43),'event only marks stale');
  await api.revoke({familyId:'original'});assert.equal(env.sent.at(-1)!.token,'b'.repeat(43));
  env.values.set(legacy,'c'.repeat(43));notify('b'.repeat(43));notify('c'.repeat(43));
  await api.refresh({familyId:'exact-family'},'original-intent');assert.equal(env.sent.at(-1)!.token,'c'.repeat(43));
  env.values.set(legacy,'d'.repeat(43));notify('d'.repeat(43));env.values.set(legacy,'e'.repeat(43));
  await api.revoke({familyId:'original'});assert.equal(env.sent.at(-1)!.token,'e'.repeat(43),'read current value after verified invalidation');
  env.reply('f'.repeat(43));await api.refresh({familyId:'exact-family'},'original-intent');env.reply(undefined);
  notify('d'.repeat(43));notify('e'.repeat(43));
  await api.revoke({familyId:'original'});assert.equal(env.sent.at(-1)!.token,'f'.repeat(43),'late old events preserve new dual-write');
});
test('foreign, malformed and stale legacy events cannot enable fallback or overwrite malformed v2',async t=>{
  const env=browser(t),caller='https://one.getportico.tv',legacy='portico.account.csrf.v1:'+canonical;
  env.location.origin=caller;env.values.set(v2(canonical,caller),'a'.repeat(43));env.values.set(legacy,'b'.repeat(43));
  const api=new BrowserAccountApi(canonical,{callerOrigin:caller});
  for(const event of [
    {key:legacy+'-other-api',newValue:'b'.repeat(43),storageArea:localStorage},
    {key:legacy,newValue:'bad',storageArea:localStorage},
    {key:legacy,newValue:'c'.repeat(43),storageArea:localStorage},
    {key:legacy,newValue:'b'.repeat(43),storageArea:null},
    {key:legacy,newValue:'b'.repeat(43),storageArea:{} as Storage},
  ]){
    api.observeLegacyCsrf(event);await api.revoke({familyId:'original'});assert.equal(env.sent.at(-1)!.token,'a'.repeat(43));
  }
  env.location.origin='https://two.getportico.tv';
  api.observeLegacyCsrf({key:legacy,newValue:'b'.repeat(43),storageArea:localStorage});env.location.origin=caller;
  await api.revoke({familyId:'original'});assert.equal(env.sent.at(-1)!.token,'a'.repeat(43));
  env.values.set(v2(canonical,caller),'bad');api.observeLegacyCsrf({key:legacy,newValue:'b'.repeat(43),storageArea:localStorage});
  await api.revoke({familyId:'original'});assert.equal(env.sent.at(-1)!.token,undefined);assert.equal(env.values.get(v2(canonical,caller)),'bad');
  env.values.set(v2(canonical,caller),'a'.repeat(43));api.observeLegacyCsrf({key:legacy,newValue:'b'.repeat(43),storageArea:localStorage});
  env.values.set(v2(canonical,caller),'bad');await api.revoke({familyId:'original'});assert.equal(env.sent.at(-1)!.token,undefined);
});
test('same-host legacy events never permit header fallback for missing or duplicate exact cookies',async t=>{
  const env=browser(t),legacy='portico.account.csrf.v1:'+canonical;
  env.values.set(legacy,'a'.repeat(43));env.values.set(v2(canonical,canonical),'a'.repeat(43));
  const api=new BrowserAccountApi(canonical,{callerOrigin:canonical});
  for(const cookie of ['',names[0]+'='+'a'.repeat(43)+'; '+names[0]+'='+'b'.repeat(43)]){
    env.page.cookie=cookie;api.observeLegacyCsrf({key:legacy,newValue:'a'.repeat(43),storageArea:localStorage});
    await api.revoke({familyId:'original'});assert.equal(env.sent.at(-1)!.token,undefined);
  }
});
test('browserAccount routes legacy events through its one existing listener and retains the credential key',async t=>{
  const env=browser(t),caller='https://one.getportico.tv',legacy='portico.account.csrf.v1:'+canonical;
  env.location.origin=caller;env.values.set(legacy,'a'.repeat(43));
  const listeners=new Map<string,((event:any)=>void)[]>();
  const before=Object.getOwnPropertyDescriptor(globalThis,'addEventListener');
  Object.defineProperty(globalThis,'addEventListener',{configurable:true,value:(type:string,callback:(event:any)=>void)=>{listeners.set(type,[...(listeners.get(type)??[]),callback]);}});
  t.after(()=>{if(before)Object.defineProperty(globalThis,'addEventListener',before);else delete (globalThis as Record<string,unknown>).addEventListener;});
  t.mock.method(globalThis,'setTimeout',(()=>0) as unknown as typeof setTimeout);
  let syncs=0;
  const {componentModule}=await import('./helpers/component-harness.mjs');
  const csrf=await import('../src/platform/account-csrf.ts');
  const routes=await import('../../packages/contracts/src/hosted/routes.ts');
  const module=await componentModule(new URL('../src/platform/account.ts',import.meta.url),{
    '../../../packages/contracts/src/hosted/routes.ts':routes,
    './account-origin.ts':{browserAccountOrigin:()=>canonical},
    './account-csrf.ts':csrf,
    '@core/index.ts':{CredentialSessionService:class {async synchronize(){syncs++;}async retryRevocations(){}async keepAlive(){}}},
  });
  const account=module.browserAccount();assert.equal(module.browserAccount(),account);assert.equal(listeners.get('storage')!.length,1);
  await account.api.revoke({familyId:'original'});assert.equal(env.sent.at(-1)!.token,'a'.repeat(43));
  env.values.set(legacy,'b'.repeat(43));listeners.get('storage')![0]({key:legacy,newValue:'b'.repeat(43),storageArea:localStorage});
  await account.api.revoke({familyId:'original'});assert.equal(env.sent.at(-1)!.token,'b'.repeat(43));assert.equal(syncs,0);
  listeners.get('storage')![0]({key:'portico.account.v1:'+canonical});assert.equal(syncs,1);
});
