import test from 'node:test';
import assert from 'node:assert/strict';
import {NativeScreenLifetime} from '../src-next/screens/settings-live/lifetime';
import {NativeLiveOwnerController} from '../src-next/screens/settings-live/live-owner-controller';
import {parseChannelMapPage, parseLiveDefaults, parseLiveSources, parseSourceDocument, parseTunerDiscovery, millisOf, secondsOf, listOf} from '../src-next/screens/settings-live/live-owner-model';
import {appendRecordingPage, needsRuleOwner, rulePaddingChoice, rulePaddingFromChoice, parseAdminRulePage, parseAdminRules, parseDVRDocument, parseTunerView} from '../src-next/screens/settings-live/recording-owner-model';
import type {ShellAuthority} from '../src-next/shell/model';

function authority() {
  let value: ShellAuthority = {serverId: 'server', accountId: 'account', profileId: 'main', epoch: 'session1', viewerFence: 'home-fence'};
  let valid = true;
  const listeners = new Set<() => void>();
  return {authority: () => value, current: () => valid, subscribeAuthority: (fn: () => void) => {listeners.add(fn); return () => {listeners.delete(fn);};}, revoke: () => {valid = false; for (const fn of [...listeners]) fn();}};
}

const source = {diagnostics: {skippedChannels: 2, skippedProgrammes: 340, adjustedProgrammes: 5, examples: ['live.import.no-title']}, deliveryValidation: 'ok', capacityKnown: true, planningEstimate: 2, id: 's1', name: 'Synthetic Live TV', revision: 4, state: 'ready', tunerCount: 2, tunerCountMode: 'discovered', generation: 'g1', publishedAt: '2026-10-24T15:50:00Z', channels: 14, programmes: 193};
const settings = {
  enabledGroups: ['News'], enabledChannels: [], kind: 'playlist', streamBufferMs: null, retryWindowMs: 30000, userAgent: '',
  filters: {categories: {mode: 'include', values: ['News']}, countries: {mode: 'exclude', values: []}, keywords: {mode: 'include', values: ['HD']}},
  logoImport: {enabled: true, overwriteExisting: false}, channelNumbering: {mode: 'sequential', startAt: 100, step: 1}, guideSourceId: '', importLimits: null,
};
const defaults = {importLimits: {bytes: 1 << 28, channels: 100000, programmes: 10000000}, streamBufferMs: 8000, retryWindowMs: 60000, userAgent: 'Portico/1.0', guideDays: 7, logoImport: true, discoveryEnabled: true};

test('live source list reads the server envelope and the fields the screen shows', () => {
  const value = parseLiveSources({protocolVersion: '1.0', serverId: 'server', sources: [source], refresh: [{sourceId: 's1', state: 'refreshing', errorCode: '', nextRefresh: '', failures: 0}]}, 'server');
  assert.equal(value.sources[0]!.name, 'Synthetic Live TV');
  assert.equal(value.sources[0]!.programmes, 193);
  assert.equal(value.sources[0]!.diagnostics.skippedProgrammes, 340);
  assert.equal(value.refresh[0]!.state, 'refreshing');
  assert.throws(() => parseLiveSources({protocolVersion: '1.0', serverId: 'other', sources: [], refresh: []}, 'server'), /invalid_response/);
  assert.throws(() => parseLiveSources({protocolVersion: '1.0', serverId: 'server', sources: [{...source, revision: -1}], refresh: []}, 'server'), /invalid_response/);
});

test('one source document keeps null overrides distinct from zero and refuses unreadable settings', () => {
  const value = parseSourceDocument({protocolVersion: '1.0', serverId: 'server', result: {sourceId: 's1', sourceName: 'Synthetic Live TV', revision: 4, digest: 'abc', effectiveLogoImport: {enabled: true, overwriteExisting: false}, settings, effective: defaults, availableKinds: ['playlist', 'xmltv-guide'], tunerCount: 2}}, 'server');
  assert.equal(value.settings.streamBufferMs, null);
  assert.equal(value.settings.retryWindowMs, 30000);
  assert.equal(value.effective.streamBufferMs, 8000);
  assert.deepEqual(value.settings.filters.keywords.values, ['HD']);
  assert.throws(() => parseSourceDocument({protocolVersion: '1.0', serverId: 'server', result: {sourceId: 's1', sourceName: 'x', revision: 4, digest: 'abc', effectiveLogoImport: {enabled: true, overwriteExisting: false}, settings: {...settings, filters: {}}, effective: defaults, availableKinds: [], tunerCount: 0}}, 'server'), {code: 'invalid_administration_response'});
});

test('channel map pages and tuner discovery read only named fields', () => {
  const map = parseChannelMapPage({protocolVersion: '1.0', serverId: 'server', result: {sourceId: 's1', generation: 'g1', nextCursor: 'c2', items: [{mark: {text: 'NN 12', seed: 's1:c1'}, channelId: 'c1', name: 'Northwind News', sourceNumber: '101', number: '12', group: 'News', guideChannelId: '', hidden: true, position: 3, overridden: true}]}}, 'server');
  assert.equal(map.items[0]!.number, '12');
  assert.equal(map.items[0]!.hidden, true);
  assert.deepEqual(map.items[0]!.mark, {text: 'NN 12', seed: 's1:c1'});
  assert.ok(Object.isFrozen(map.items[0]!.mark));
  const discovery = parseTunerDiscovery({protocolVersion: '1.0', serverId: 'server', result: {devices: [{deviceId: 'd', model: 'Prime', friendlyName: '', address: '192.168.1.50', baseUrl: 'http://192.168.1.50', tunerCount: 3, configured: false}], searchedFor: '', discoveryEnabled: true}}, 'server');
  assert.equal(discovery.devices[0]!.tunerCount, 3);
});

test('recording documents read the server vocabulary and keep signed priorities', () => {
  const dvr = parseDVRDocument({protocolVersion: '1.0', serverId: 'server', result: {revision: 9, digest: 'dvr-digest', folderTokens: [], settings: {prePaddingMs: 60000, postPaddingMs: 180000, keepPolicy: {mode: 'keep-count', keepCount: 10, keepDays: 0, keepUntilWatched: false}, conversion: {mode: 'none', ladderId: '', deleteOriginal: false}, folderTemplate: '', movieFolderTemplate: '', recordingProfile: 'original', sidecars: {nfo: false, poster: false, fanart: false, thumbnail: false}, tunerPreference: 'first-available', liveTunerConflict: 'keep-watching'}, enumerations: {keepMode: ['keep-all', 'keep-count', 'keep-days']}}}, 'server');
  assert.equal(dvr.settings.keepPolicy.keepCount, 10);
  assert.deepEqual(dvr.enumerations.keepMode, ['keep-all', 'keep-count', 'keep-days']);
  const rules = parseAdminRules({protocolVersion: '1.0', serverId: 'server', result: {items: [{id: 'r1', revision: 2, owner: {authority: 'local', accountId: 'a', profileId: 'p'}, status: '', kind: 'series', name: 'The Clockmakers', match: 'clock', sourceId: 's1', enabled: true, options: {prePaddingMs: null, postPaddingMs: null, keepPolicy: null, newEpisodesOnly: true, priority: -5, folderTemplate: ''}}]}}, 'server');
  assert.equal(rules[0]!.options.priority, -5);
  assert.equal(rules[0]!.options.prePaddingMs, null);
  const tuners = parseTunerView({protocolVersion: '1.0', serverId: 'server', result: {sources: [{sourceId: 's1', sourceName: 'Synthetic Live TV', tunerCount: 2, inUse: 1, conflicts: 1, active: [{kind: 'live', accountName: 'Demo', profileName: 'Sam', recordingId: '', title: 'Cycling: Mountain Stage', state: 'watching', startsAt: '', endsAt: ''}], upcoming: []}], observedAt: '2026-10-24T16:00:00Z', upcomingWindowHours: 48}}, 'server');
  assert.equal(tuners.sources[0]!.conflicts, 1);
  assert.equal(tuners.sources[0]!.active[0]!.profileName, 'Sam');
});

test('seconds, milliseconds and comma lists round-trip without inventing a default', () => {
  assert.equal(secondsOf(null), '');
  assert.equal(secondsOf(8000), '8');
  assert.equal(millisOf(''), null);
  assert.equal(millisOf('8'), 8000);
  assert.deepEqual(listOf(' News, Sports ,, HD '), ['News', 'Sports', 'HD']);
});

test('the owner controller reads the real list and writes only after a server receipt', async () => {
  const owner = authority(), calls: {path: string; method: string; body?: unknown}[] = [];
  let rejection: Error | undefined;
  const lifetime = new NativeScreenLifetime(owner);
  const api = {request: async <T,>(path: string, method = 'GET', body?: unknown) => {
    calls.push({path, method, body});
    if (path === '/v1/admin/live-sources' && method === 'GET') return {protocolVersion: '1.0', serverId: 'server', sources: [source], refresh: []} as T;
    if (path === '/v1/admin/live-sources/s1/enabled') {if (rejection) throw rejection; return {} as T;}
    throw new Error(`unexpected ${method} ${path}`);
  }};
  const controller = new NativeLiveOwnerController(lifetime, api, () => 'op1');
  await controller.load();
  assert.equal(controller.getSnapshot().list?.sources.length, 1);
  rejection = Object.assign(new Error('conflict'), {status: 412});
  await controller.setEnabled('s1', false);
  assert.equal(calls.filter(call => call.method === 'POST').length >= 1, true);
  assert.equal(controller.getSnapshot().error, rejection);
  assert.equal(controller.getSnapshot().list?.sources[0]!.state, 'ready');
  lifetime.dispose(); controller.dispose();
});

test('administrative rules retain legal DVR values, title and ownerless rows', () => {
  const row = {id: 'r', revision: 2, owner: {authority: 'local', accountId: 'a', profileId: 'p'}, status: '', kind: 'title', name: 'Title', match: 'Title', sourceId: 's', enabled: false, options: {prePaddingMs: 3600000, postPaddingMs: 21600000, keepPolicy: {mode: 'keep-count', keepCount: 5000, keepDays: 0, keepUntilWatched: false}, newEpisodesOnly: false, priority: 500, folderTemplate: ''}};
  const legacy = {...row, id: 'legacy', owner: {authority: '', accountId: '', profileId: ''}, status: 'needs-owner-and-guide-selection'};
  const envelope = (items: unknown[]) => ({serverId: 'server', result: {items, nextCursor: 'next'}});
  const page = parseAdminRulePage(envelope([row, legacy]), 'server');
  assert.equal(page.nextCursor, 'next'); assert.equal(page.items[0]!.options.prePaddingMs, 3600000);
  for (const value of [0, 60000, 1800000, 21600000, null]) {
    const parsed = parseAdminRulePage(envelope([{...row, kind: 'series', options: {...row.options, prePaddingMs: value, postPaddingMs: 7200000}}]), 'server');
    assert.equal(parsed.items[0]!.options.prePaddingMs, value); assert.equal(parsed.items[0]!.options.postPaddingMs, 7200000);
  }
  assert.equal(page.items[0]!.options.keepPolicy?.keepCount, 5000); assert.equal(needsRuleOwner(page.items[1]!), true);
  for (const value of [-1, 0.5, 21600001]) assert.throws(() => parseAdminRulePage(envelope([{...row, options: {...row.options, prePaddingMs: value}}]), 'server'));
});

test('ownerless legacy rule cannot toggle and deletion keeps its original identity and revision', async () => {
  const owner = authority(), calls: {path: string; method?: string}[] = [];
  const lifetime = new NativeScreenLifetime(owner);
  const api = {request: async <T,>(path: string, method?: string) => {
    calls.push({path, method});
    if (path === '/v1/admin/live-sources') return {protocolVersion: '1.0', serverId: 'server', sources: [source], refresh: []} as T;
    return {} as T;
  }};
  const controller = new NativeLiveOwnerController(lifetime, api, () => 'op1');
  const legacy = parseAdminRules({serverId: 'server', result: {items: [{id: 'legacy', revision: 7, owner: {authority: '', accountId: '', profileId: ''}, status: 'needs-owner-and-guide-selection', kind: 'series', name: 'Legacy', match: 'legacy', sourceId: '', enabled: false, options: {prePaddingMs: 0, postPaddingMs: 0, keepPolicy: null, newEpisodesOnly: false, priority: 0, folderTemplate: ''}}]}}, 'server')[0]!;
  await assert.rejects(controller.saveRuleEnabled(legacy, [], true));
  assert.equal(calls.length, 0);
  await controller.deleteRule(legacy);
  assert.equal(calls[0]!.method, 'DELETE');
  assert.match(calls[0]!.path, /recording-groups\/legacy\?expectedRevision=7&operationId=op1/);
});

test('recording controller forwards independent owner and rule cursors', async () => {
  const calls: string[] = [], lifetime = new NativeScreenLifetime(authority());
  const api = {request: async <T,>(path: string) => {
    calls.push(path);
    const result = path.startsWith('/v1/admin/dvr/recording-owners') ? {items: [], nextCursor: 'owner-next'} : path.startsWith('/v1/admin/dvr/recording-groups') ? {items: [], nextCursor: 'rule-next'} : [];
    return {serverId: 'server', result} as T;
  }};
  const controller = new NativeLiveOwnerController(lifetime, api, () => 'op1');
  assert.equal((await controller.policy('owner-one')).nextCursor, 'owner-next');
  assert.equal((await controller.rules('rule-one')).nextCursor, 'rule-next');
  assert.ok(calls.some(path => path.includes('recording-owners?limit=200&cursor=owner-one')));
  assert.ok(calls.some(path => path.includes('recording-groups?limit=100&cursor=rule-one')));
});

test('recording continuation deduplicates full keys and refuses cursor cycles without consuming retries', () => {
  const seen = new Set<string>();
  const first = [{key: 'local/a/p'}, {key: 'hosted/a/p'}];
  const second = appendRecordingPage(first, [{key: 'local/a/p'}, {key: 'local/a/q'}], 'c1', 'c2', seen, row => row.key);
  assert.deepEqual(second.map(row => row.key), ['local/a/p', 'hosted/a/p', 'local/a/q']);
  seen.add('c1');
  assert.throws(() => appendRecordingPage(second, [{key: 'third'}], 'c2', 'c1', seen, row => row.key));
  assert.equal(seen.has('c2'), false);
  assert.deepEqual(appendRecordingPage(second, [{key: 'third'}], 'c2', '', seen, row => row.key).map(row => row.key), ['local/a/p', 'hosted/a/p', 'local/a/q', 'third']);
});

test('new-rule padding choice preserves minute wire units and null inheritance', () => {
  assert.equal(rulePaddingFromChoice('60'), 60000);
  assert.equal(rulePaddingChoice(60000), '60');
  assert.equal(rulePaddingFromChoice('default'), null);
  assert.equal(rulePaddingChoice(null), 'default');
});

test('rule toggle adopts only its exact advanced receipt', async () => {
  const owner = {authority: 'local' as const, accountId: 'a', profileId: 'p'};
  const row = {id: 'r', revision: 3, owner, status: 'disabled', kind: 'title', name: 'Title', match: 'Title', sourceId: 's', enabled: false, options: {prePaddingMs: 60000, postPaddingMs: 0, keepPolicy: null, newEpisodesOnly: false, priority: 0, folderTemplate: ''}};
  const rule = parseAdminRules({serverId: 'server', result: {items: [row]}}, 'server')[0]!;
  const grants = [{owner, enabled: true, revision: 1}];
  let receipt = {...row, revision: 4, enabled: true};
  const calls: string[] = [];
  const api = {request: async <T,>(path: string) => {
    calls.push(path);
    if (path === '/v1/admin/live-sources') return {serverId: 'server', sources: [source], refresh: []} as T;
    return {serverId: 'server', result: receipt} as T;
  }};
  const controller = new NativeLiveOwnerController(new NativeScreenLifetime(authority()), api, () => 'op1');
  assert.equal((await controller.saveRuleEnabled(rule, grants, true)).revision, 4);
  for (const bad of [{...receipt, id: 'other'}, {...receipt, owner: {...owner, profileId: 'other'}}, {...receipt, revision: 3}, {...receipt,revision:5}, {...receipt,revision:999}, {...receipt, enabled: false}, {...receipt, match: 'other'}, {...receipt, options: {...receipt.options, prePaddingMs: 0}}]) {
    receipt = bad; await assert.rejects(controller.saveRuleEnabled(rule, grants, true), /invalid_response/);
  }
});

test('new rule accepts only its actual command receipt and never refreshes a shared source',async()=>{
 const owner={authority:'local' as const,accountId:'a',profileId:'p'},grants=[{owner,enabled:true,revision:1}];
 const draft={owner,anchor:{sourceId:'s',channelId:'c',generation:'g',programmeId:'programme'},kind:'title',name:'Actual title',match:'Actual title',sourceId:'s',enabled:false,
  options:{prePaddingMs:60000,postPaddingMs:null,keepPolicy:null,newEpisodesOnly:false,priority:500,folderTemplate:''}};
 const row={id:'generated',revision:7,owner,status:'disabled',kind:draft.kind,name:draft.name,match:draft.match,sourceId:'s',enabled:false,
  options:{...draft.options,postPaddingMs:120000,keepPolicy:{mode:'keep-count',keepCount:5000,keepDays:0,keepUntilWatched:false}}};
 let receipt:any={serverId:'server',result:row};const calls:{method?:string;body?:any}[]=[];
 const controller=new NativeLiveOwnerController(new NativeScreenLifetime(authority()),{request:async<T>(_path:string,method?:string,body?:unknown)=>{calls.push({method,body});return receipt as T;}},()=> 'original-create');
 try{
  assert.equal((await controller.createRule(draft,grants)).revision,7,'create/revive is not assumed revision one');
  assert.deepEqual(calls[0].body.anchor,draft.anchor);assert.equal(calls[0].body.operationId,'original-create');assert.equal(calls[0].body.options.prePaddingMs,60000);
  for(const invalid of [{},{serverId:'wrong',result:row},{serverId:'server',result:{...row,id:''}},{serverId:'server',result:{...row,revision:0}},
   ...[{owner:{...owner,profileId:'other'}},{sourceId:'other'},{kind:'series'},{name:'Other'},{match:'Other'},{enabled:true},{options:{...row.options,prePaddingMs:0}}].map(change=>({serverId:'server',result:{...row,...change}}))]){
   receipt=invalid;await assert.rejects(controller.createRule(draft,grants),/invalid_response/);
  }
  assert(calls.every(call=>call.method==='POST'),'no receipt causes an unrelated shared GET');
 }finally{controller.dispose();}
});

test('original create/toggle caller retirement during POST/PUT never refreshes or adopts under a successor',async()=>{
 for(const command of ['create','toggle'] as const){
  let answer!:(value:unknown)=>void,live=true;const calls:{method?:string;signal?:AbortSignal}[]=[];
  const owner={authority:'local' as const,accountId:'a',profileId:'p'},grants=[{owner,enabled:true,revision:1}];
  const row={id:'original',revision:3,owner,status:'disabled',kind:'title',name:'Title',match:'Title',sourceId:'s',enabled:false,options:{prePaddingMs:60000,postPaddingMs:0,keepPolicy:null,newEpisodesOnly:false,priority:0,folderTemplate:''}};
  const rule=parseAdminRules({serverId:'server',result:{items:[row]}},'server')[0]!;
  const lifetime=new NativeScreenLifetime(authority()),caller=new AbortController();
  const controller=new NativeLiveOwnerController(lifetime,{request:async<T>(_path:string,method?:string,_body?:unknown,signal?:AbortSignal)=>{calls.push({method,signal});return await new Promise<unknown>(resolve=>{answer=resolve;}) as T;}},()=> 'original-operation');
  const proof={signal:caller.signal,isCurrent:()=>live};
  const request=command==='toggle'?controller.saveRuleEnabled(rule,grants,true,proof):controller.createRule({owner,anchor:{sourceId:'s',channelId:'c',generation:'g',programmeId:'programme'},kind:'title',name:'Title',match:'Title',sourceId:'s',enabled:false,options:row.options},grants,proof);
  try{
   assert.equal(calls.length,1);live=false;caller.abort();assert.equal(calls[0].signal?.aborted,true);
   const refused=assert.rejects(request,{name:'AbortError'});answer({serverId:'server',result:{...row,revision:4,enabled:command==='toggle'}});await refused;
   assert.equal(calls.length,1);assert.equal(lifetime.current(),true,'borrowed controller lifetime remains live');
  }finally{controller.dispose();lifetime.dispose();}
 }
});

test('last original owner predicate aborting a command caller inside the real lifetime request refuses before API dispatch',async()=>{
 const caller=new AbortController();let insideRequest=false,armed=false,requests=0;
 class ObservedLifetime extends NativeScreenLifetime{
  override request<T>(read:(signal:AbortSignal)=>Promise<T>,signal?:AbortSignal):Promise<T>{insideRequest=true;return super.request(read,signal).finally(()=>{insideRequest=false;});}
 }
 const original=authority();const lifetime=new ObservedLifetime({...original,current:()=>{if(armed)caller.abort();return original.current();}});
 const controller=new NativeLiveOwnerController(lifetime,{request:async<T>()=>{requests++;return {} as T;}},()=> 'original-pre-io');
 const owner={authority:'local' as const,accountId:'a',profileId:'p'},grants=[{owner,enabled:true,revision:1}];
 try{
  await assert.rejects(controller.createRule({owner,anchor:{sourceId:'s',channelId:'c',generation:'g',programmeId:'programme'},kind:'title',name:'Title',match:'Title',sourceId:'s',enabled:false,
   options:{prePaddingMs:60000,postPaddingMs:0,keepPolicy:null,newEpisodesOnly:false,priority:0,folderTemplate:''}},grants,
   {signal:caller.signal,isCurrent:()=>{if(insideRequest)armed=true;return true;}}),{name:'AbortError'});
  assert.equal(armed,true);assert.equal(caller.signal.aborted,true);assert.equal(requests,0,'no API dispatch, acceptance or shared refresh after nested caller abort');assert.equal(lifetime.current(),true);
 }finally{controller.dispose();lifetime.dispose();}
});

test('native live documents accept the shared 100,000-choice limit and retain rewind', () => {
  const enabled = Array.from({length: 100000}, (_, index) => `channel-${index}`);
  const result = {sourceId: 's1', sourceName: 'Large source', revision: 4, digest: 'abc', effectiveLogoImport: {enabled: true, overwriteExisting: false}, settings: {...settings, enabledGroups: enabled, enabledChannels: enabled}, effective: {...defaults, timeshiftMinutes: 120}, availableKinds: ['playlist'], tunerCount: 2};
  const value = parseSourceDocument({serverId: 'server', result}, 'server');
  assert.equal(value.settings.enabledChannels.length, 100000);
  assert.equal(value.settings.enabledGroups.length, 100000);
  assert.equal(value.settings.enabledChannels[99999], 'channel-99999');
  assert.equal(value.effective.timeshiftMinutes, 120);
  assert.throws(() => parseSourceDocument({serverId: 'other', result}, 'server'), /invalid_response/);
  assert.throws(() => parseSourceDocument({serverId: 'server', result: {...result, settings: {...result.settings, enabledChannels: [...enabled, 'too-many']}}}, 'server'), {code: 'invalid_administration_response'});
  assert.throws(() => parseSourceDocument({serverId: 'server', result: {...result, settings: {...settings, kind: 'xmltv-guide', streamBufferMs: 8000}}}, 'server'), {code: 'invalid_administration_response'});
  const live = parseLiveDefaults({serverId: 'server', result: {scope: 'server', revision: 1, digest: 'defaults', settings: {...defaults, timeshiftMinutes: 60}}}, 'server');
  assert.equal(live.settings.timeshiftMinutes, 60);
});

test('native DVR defaults refuse deleting the only original and unknown conversion modes', () => {
  const result = {revision: 1, digest: 'dvr', folderTokens: [], enumerations: {}, settings: {prePaddingMs: 60000, postPaddingMs: 180000,
    keepPolicy: {mode: 'keep-count', keepCount: 10, keepDays: 0, keepUntilWatched: false},
    conversion: {mode: 'none', ladderId: '', deleteOriginal: false}, folderTemplate: '{Title}', movieFolderTemplate: '{Title} ({Year})',
    recordingProfile: 'original', sidecars: {nfo: false, poster: false, fanart: false, thumbnail: false}, tunerPreference: 'first-available', liveTunerConflict: 'keep-watching'}};
  const document = (conversion: typeof result.settings.conversion) => ({serverId: 'server', result: {...result, settings: {...result.settings, conversion}}});
  assert.equal(parseDVRDocument(document(result.settings.conversion), 'server').settings.folderTemplate, '{Title}');
  assert.throws(() => parseDVRDocument(document({...result.settings.conversion, deleteOriginal: true}), 'server'), {code: 'invalid_administration_response'});
  assert.throws(() => parseDVRDocument(document({...result.settings.conversion, mode: 'unexpected'}), 'server'), {code: 'invalid_administration_response'});
  assert.equal(parseDVRDocument(document({...result.settings.conversion, mode: 'remux', deleteOriginal: true}), 'server').settings.conversion.deleteOriginal, true);
  assert.throws(() => parseDVRDocument({...document(result.settings.conversion), serverId: 'other'}, 'server'), /invalid_response/);
});


test('native source editor reads persisted rows and sends write-only changes with one revision',async()=>{
 const owner=authority(),calls:any[]=[];
 const remote={kind:'xtream',guideUrl:'https://guide.example/guide.xml',guideUrlRedacted:false,refreshMs:21600000,ownerLimit:2,useDiscoveredCapacity:false};
 const stored={sourceId:'s1',sourceName:'Source',revision:7,digest:'digest',tunerCount:2,effectiveLogoImport:{enabled:true,overwriteExisting:false},availableKinds:['playlist'],settings:{...settings,enabled:true,viewerAccess:'server-members',remote},effective:defaults};
 const api={async request<T>(path:string,method='GET',body?:any){calls.push({path,method,body});if(path==='/v1/admin/live-sources')return {serverId:'server',sources:[source],refresh:[]} as T;return {serverId:'server',result:stored} as T;}};
 const controller=new NativeLiveOwnerController(new NativeScreenLifetime(owner),api,()=> 'operation');
 const doc=await controller.document('s1');assert.equal(doc.settings.viewerAccess,'server-members');assert.equal(doc.settings.remote?.ownerLimit,2);assert.equal(doc.settings.remote?.password,undefined);
 await controller.saveDocument(doc,{...doc.settings,enabled:false,viewerAccess:'owner-only',remote:{...doc.settings.remote!,refreshMs:43200000,ownerLimit:4,password:'replacement',username:''}});
 const put=calls.find(value=>value.method==='PUT');assert.equal(put.body.expectedRevision,7);assert.equal(put.body.settings.enabled,false);assert.equal(put.body.settings.remote.password,'replacement');assert.equal(put.body.settings.remote.username,'');assert.equal(put.body.settings.remote.refreshMs,43200000);controller.dispose();
});
