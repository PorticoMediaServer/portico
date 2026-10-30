import test from 'node:test';
import assert from 'node:assert/strict';
import {FreshnessHub} from '@core/freshness-hub.ts';
import {parseDirectSnapshot} from '@core/profile-management.ts';
import {attachShellFactFeed, bindShellFactWake, shellProofChanged} from '../src/shell/facts.ts';
import {clock, direct, system, harness} from './shell-facts-fixture.ts';

test('five hint groups refresh current display without re-admitting authority; bursts coalesce', async () => {
  const h=harness();h.rename();
  for(let i=0;i<10;i++)for(const group of ['directory','server','members','liveSources','libraryChannels'] as const)h.owner.request(group);
  await h.time.advance(1500);
  assert.equal(h.owner.source.getSnapshot().serverName,'Den');
  assert.equal(h.owner.source.getSnapshot().identity.libraries[0].name,'Cinema');
  assert.equal(h.owner.source.getSnapshot().identity.profileName,'Night');
  assert.equal(h.owner.source.getSnapshot().identity.username,'updated');
  assert.equal(h.counts().retired,0);assert.equal(h.counts().permission,1);assert.equal(h.counts().manifests,1);
  assert.equal(h.calls.filter(path=>path==='/v1/libraries').length,1);assert.equal(h.calls.filter(path=>path==='/v1/direct').length,1);
  assert.equal(h.calls.filter(path=>path.startsWith('/v1/guide/sources?')).length,1);
  assert.equal(h.initial.serverName,'Study');h.dispose();assert.equal(h.time.pending(),0);
});
test('guide failure cannot discard independently confirmed directory or invent a viewer retirement', async () => {
  const h=harness();h.rename();h.guideFailure(Object.assign(new Error('offline'),{retryAfterSeconds:7200}));
  h.owner.request('directory');h.owner.request('server');await h.time.advance(1000);
  assert.equal(h.owner.source.getSnapshot().identity.libraries[0].name,'Cinema');
  assert.equal(h.owner.source.getSnapshot().serverName,'Den');assert.equal(h.owner.source.getSnapshot().identity.liveAvailable,true);
  const reads=h.calls.filter(path=>path.startsWith('/v1/guide/sources?')).length;h.hub.setActive(false);h.hub.setActive(true);h.owner.request('server');await h.time.advance(60000);
  assert.equal(h.calls.filter(path=>path.startsWith('/v1/guide/sources?')).length,reads);assert.equal(h.counts().retired,0);h.dispose();
});
test('retained hidden media runs independent authority checks while display remains dormant', async () => {
  const h=harness();h.hub.setActive(false);h.owner.setRetained(true);h.owner.all();await h.time.advance(1000);
  assert.equal(h.calls.filter(path=>path==='/v1/libraries').length,0);
  assert.equal(h.calls.filter(path=>path==='/v1/system').length,0);
  assert.equal(h.calls.filter(path=>path==='/v1/direct').length,1);
  assert.deepEqual(h.counts(),{permission:1,manifests:1,retired:0});
  h.hub.setActive(true);await h.time.advance(1500);assert.ok(h.calls.includes('/v1/libraries'));h.dispose();
});
test('strict direct eligibility proof retires before input settles; malformed proof never certifies absence', async () => {
  const h=harness();h.hub.touch();h.direct({...direct(),profiles:[{...direct().profiles[0],id:'replacement'}],account:{...direct().account,primaryProfileId:'replacement'}});
  h.owner.request('members');await h.time.advance(400);assert.equal(h.counts().retired,1);h.dispose();
  const broken=harness();broken.direct({...direct(),profiles:[{...direct().profiles[0],name:undefined}]});broken.owner.request('members');await broken.time.advance(1000);
  assert.equal(broken.counts().retired,0);assert.equal(broken.owner.source.getSnapshot().identity.profileName,'Evening');
  await broken.time.advance(2500);assert.ok(broken.calls.filter(path=>path==='/v1/direct').length>1);broken.dispose();
});
test('readonly admission does not restart a stopped mutating permission owner', async () => {
  const h=harness(true);h.owner.all();await h.time.advance(1500);assert.equal(h.counts().permission,0);assert.equal(h.counts().retired,0);h.dispose();
});
test('system name/version are display but actual scope/security changes replace authority', () => {
  const initial={system:system(),direct:parseDirectSnapshot(direct())};
  assert.equal(shellProofChanged(initial,{system:{...system(),name:'Den',version:'2'}},'profile'),false);
  for(const next of [{...system(),id:'other'},{...system(),setupRequired:true},{...system(),hostedAttached:true, apiLevel: 1, apiVersion: '1.0', apiMajors: [1], capabilities: []}])assert.equal(shellProofChanged(initial,{system:next},'profile'),true);
});
test('first connected relay queues one catch-up without cursor, survives disconnect, and cancels on dispose', async () => {
  const time=clock(),hub=new FreshnessHub(time.env);let connected=false,catchups=0,current=true;const statuses=new Set<()=>void>(),resyncs=new Set<()=>void>();
  const events={getStatus:()=>({connected}),subscribeStatus:(fn:()=>void)=>{statuses.add(fn);return()=>statuses.delete(fn);},onResync:(fn:()=>void)=>{resyncs.add(fn);return()=>resyncs.delete(fn);}};
  const off=attachShellFactFeed(events as never,hub,()=>current,()=>{catchups++;});
  connected=true;for(const fn of statuses)fn();assert.equal(catchups,0);connected=false;await time.advance(0);assert.equal(catchups,1);
  connected=true;for(const fn of statuses)fn();await time.advance(0);assert.equal(catchups,1);
  for(const fn of resyncs)fn();assert.equal(catchups,2);current=false;off();hub.dispose();assert.equal(time.pending(),0);assert.equal(statuses.size+resyncs.size,0);
});

test('independent server inputs retain each failure and full floor, even beside a terminal input', async () => {
  const h=harness();h.systemFailure(Object.assign(new Error('system refused'),{status:400}));
  h.preferenceFailure(Object.assign(new Error('preference wait'),{status:429,retryAfterSeconds:7200}));
  h.owner.request('server');await h.time.advance(1500);
  const reads=()=>h.calls.filter(path=>path.startsWith('/v1/preferences?')).length;assert.equal(reads(),1);
  h.systemFailure(undefined);h.hub.setActive(false);h.hub.setActive(true);h.owner.request('server');await h.time.advance(60000);
  assert.equal(reads(),1,'system retry or wake cannot shorten independent preference floor');
  h.preferenceFailure(undefined);await h.time.advance(7200000);assert.equal(reads(),2);h.dispose();
});
test('malformed Home authority proof stays owed instead of inheriting the legacy terminal parser flag', async () => {
  const h=harness(),error=Object.assign(new Error('unreadable Home'),{code:'invalid_home',retryable:false});h.homeFailure(error);
  h.owner.authority();await h.time.advance(1000);assert.equal(h.counts().manifests,1);assert.equal(h.counts().retired,0);
  h.homeFailure(undefined);await h.time.advance(2500);assert.equal(h.counts().manifests,2);h.dispose();
});

test('online reconciles retained hidden authority without opening display or depending on a second first-connect', async () => {
  const h=harness();h.hub.setActive(false);h.owner.setRetained(true);let wakes=0;
  const page=new EventTarget(),browser=new EventTarget();Object.defineProperty(page,'hidden',{value:true});
  const off=bindShellFactWake(page as never,browser as never,{isCurrent:()=>true,retained:()=>true,wakeAuth:()=>{wakes++;},invalidate:()=>h.owner.all()});
  browser.dispatchEvent(new Event('online'));await h.time.advance(1000);
  assert.equal(wakes,1);assert.equal(h.counts().permission,1);assert.equal(h.counts().manifests,1);
  assert.equal(h.calls.filter(path=>path==='/v1/libraries'||path==='/v1/system').length,0);
  off();browser.dispatchEvent(new Event('online'));assert.equal(wakes,1);h.dispose();
});

test('canonical hint delivery preserves server revision and preference address replay dedupe', async () => {
  const h=harness();const server={id:'one',type:'settings.changed',resource:{kind:'settings',id:'server'},revision:'2'};
  h.hub.accept(server);await h.time.advance(1500);const first=h.calls.length;
  h.hub.accept({...server,id:'replay'});await h.time.advance(1500);assert.equal(h.calls.length,first);
  const pref={id:'p',type:'settings.changed',resource:{kind:'settings',id:'preferences'},revision:'3',data:{scope:'profile-device-class',deviceClass:'web'}};
  h.hub.accept(pref);await h.time.advance(1500);const second=h.calls.length;
  h.hub.accept({...pref,id:'p-replay'});await h.time.advance(1500);assert.equal(h.calls.length,second);
  h.hub.accept({...pref,id:'other-scope',data:{scope:'profile-server'}});await h.time.advance(1500);assert.equal(h.calls.length,second+1);
  h.dispose();
});

test('guide Retry-After remains binding when later hints switch server, live-source and channel groups', async () => {
  const h=harness(),guideReads=()=>h.calls.filter(path=>path.startsWith('/v1/guide/sources?')).length;
  h.guideFailure(Object.assign(new Error('guide wait'),{status:429,retryAfterSeconds:7200}));
  h.owner.request('server');await h.time.advance(1000);assert.equal(guideReads(),1);
  h.owner.request('liveSources');await h.time.advance(1000);assert.equal(guideReads(),1);
  h.owner.request('libraryChannels');await h.time.advance(1000);assert.equal(guideReads(),1);
  h.hub.setActive(false);h.owner.setRetained(true);h.owner.all();h.hub.resync();
  h.hub.note('evict:'.repeat(100000));await h.time.advance(1000);h.hub.setActive(true);h.owner.all();
  h.guideFailure(undefined);await h.time.advance(7_200_400-h.time.env.now()-1);assert.equal(guideReads(),1);
  await h.time.advance(1);assert.equal(guideReads(),2,'all three groups coalesce at the original response-time floor');
  assert.equal(h.counts().retired,0);h.dispose();assert.equal(h.time.pending(),0);
});

test('mounted strict direct proof retires synchronously through the original classifier without another request', async () => {
  const h=harness();h.hub.touch();h.owner.observeDirect(parseDirectSnapshot({...direct(),account:{...direct().account,disabled:true}}));
  assert.equal(h.counts().retired,1);assert.equal(h.calls.length,0);assert.equal(h.owner.source.getSnapshot().identity.profileName,'Evening');h.dispose();
  const renamed=harness(),proof=direct();proof.account.username='New';proof.profiles[0].name='New';
  renamed.owner.observeDirect(parseDirectSnapshot(proof));assert.equal(renamed.counts().retired,0);assert.equal(renamed.calls.length,0);renamed.dispose();
});
