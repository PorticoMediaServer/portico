import test from 'node:test';
import assert from 'node:assert/strict';
import {PreferencesReader} from '@core/preferences-reader.ts';
import type {PreferenceSnapshot} from '@core/preferences.ts';
import type {SettingsConfiguration} from '@core/settings-configuration.ts';
import {settingsSpec} from '@core/presentation/settings-spec.ts';
import {SettingsController, type SettingsControllerOptions} from '../src/screens/settings/controller.ts';

const serverId = 'captured-server', fence = 'a'.repeat(64);
const section = settingsSpec.find(section => section.scope === 'server' && section.id === 'integrations')!;
const configuration: SettingsConfiguration = {revision: 4, registryRevision: 'actual-registry', signInPolicyRevision: 'policy-1', path: '/actual/portico.toml', fields: [
  {id: 'integrations.maxWebhooks', key: 'integrations.max_webhooks', current: 12, effective: 10, default: 8, source: 'file', configOnly: true, applicationMode: 'next-operation'},
]};
const preferences: PreferenceSnapshot = {deviceClass: 'web', registry: {revision: 'test', fields: []}, documents: [], effective: {}, effectiveSource: {}, clampedFields: [], registryRevision: 'test'};

function fixture(input: {owner?: boolean; readConfiguration?: SettingsControllerOptions['readConfiguration']; engines?: boolean} = {}) {
  const calls: string[] = [], signals: AbortSignal[] = [];
  let current = true;
  const api: SettingsControllerOptions['api'] = {request: async <T>(path: string, _method?: string, _body?: unknown, signal?: AbortSignal) => {
    calls.push(path);if (signal) signals.push(signal);
    const scope = {serverId, viewerFence: fence};
    const name = path.split('/').at(-1)!;
    return {scope, data: {name, observedAt: 1000, freshUntil: 31000, facts: {}}} as T;
  }};
  const reader = new PreferencesReader(api);
  const controller = new SettingsController({api, serverId, viewerId: 'captured-viewer', role: input.owner === false ? 'member' : 'owner', mainProfile: true,
    capabilities: {manageServer: input.owner !== false, managePeople: false, manageSignIn: false}, preferences, reader,
    readConfiguration: input.readConfiguration, isCurrent: () => current, reportError: error => {throw error;},
    environment: {platform: 'web', os: 'browser'}, eligibility: {hasEngine: () => input.engines === true, library: () => undefined},
    reading: {read: () => {throw new Error('Unexpected Reading read');}, patch: () => {throw new Error('Unexpected Reading write');}},
  });
  return {controller, calls, signals, retire: () => {current = false;}, dispose: () => {controller.dispose();reader.dispose();}};
}

test('a page reads the configuration through its own reader, once per successful scope', async () => {
  let reads = 0;
  const f = fixture({readConfiguration: async () => {reads++;return configuration;}});
  try {
    await f.controller.load(section);
    const matches = f.controller.configurationMatches('integrations.max_webhooks');
    assert.equal(matches?.length, 1);
    assert.deepEqual(matches?.[0], {...configuration.fields[0], label: matches?.[0].label, path: configuration.path});
    assert.equal(f.controller.getSnapshot(section).loading, false);
    await f.controller.load(section);
    assert.equal(reads, 1);
  } finally {f.dispose();}
});

// W2 F1 (16 Nov walk): the rebuilt server keeps no configuration document and no console settings; a page
// with no reader of its own asks for neither, so it never answers 404 and its other rows still draw.
test('a server page with no reader of its own asks the server for no old configuration or console settings', async () => {
  const f = fixture({engines: true});
  const history = settingsSpec.find(section => section.scope === 'server' && section.id === 'history')!;
  try {
    await f.controller.load(section);
    await f.controller.load(history);
    assert.equal(f.controller.getSnapshot(history).error, undefined);
    for (const old of ['/v1/admin/settings/configuration', '/v1/admin/console/settings', '/v1/admin/maintenance/settings', '/v1/admin/connectivity/status', '/v1/admin/notifications/settings'])
      assert.ok(!f.calls.some(path => path.startsWith(old)), `${old} was asked: ${f.calls.join(', ')}`);
  } finally {f.dispose();}
});

test('a server with no configuration route opens the page with nothing configured there', async () => {
  const f = fixture({readConfiguration: async () => {throw Object.assign(new Error('Not found.'), {status: 404, code: 'not_found'});}});
  try {await f.controller.load(section);assert.equal(f.controller.getSnapshot(section).error, undefined);assert.deepEqual(f.controller.configurationMatches('integrations.max_webhooks'), []);}
  finally {f.dispose();}
});
test('denied members start no owner reads; an injected reader retains the same lifetime boundary', async () => {
  const denied = fixture({owner: false});
  try {await denied.controller.load(section);assert.deepEqual(denied.calls, []);} finally {denied.dispose();}
  let signal: AbortSignal | undefined, release!: (value: typeof configuration) => void;
  const f = fixture({readConfiguration: async captured => {signal = captured;return new Promise(resolve => {release = resolve;});}});
  const pending = f.controller.load(section);await new Promise(resolve => setImmediate(resolve));
  f.retire();release(configuration);await pending;
  assert.deepEqual(f.controller.configurationMatches('integrations.max_webhooks'), []);
  f.dispose();assert.equal(signal?.aborted, true);
});

test('current-section quiet configuration bypasses successful memoization without changing page acknowledgement',async()=>{
 let reads=0;const f=fixture({readConfiguration:async()=>({...configuration,revision:++reads,path:reads===1?'/first':'/fresh'})});
 try{await f.controller.load(section);const before=f.controller.getSnapshot(section),inputs=f.controller.quietInputs(section),configurationInput=inputs.find(input=>input.id==='configuration')!;
  assert.deepEqual(await configurationInput.refresh(),{kind:'adopted'});assert.equal(reads,2);assert.equal(f.controller.configurationMatches('integrations.max_webhooks')?.[0]?.path,'/fresh');assert.deepEqual(f.controller.getSnapshot(section),before);
  assert.deepEqual(inputs.map(input=>input.id),['configuration','header/storage']);
 }finally{f.dispose();}
});

test('quiet section input admission cancels stale configuration while unreadability keeps original error and confirmed data',async()=>{
 let reads=0,signal:AbortSignal|undefined,release!:(value:SettingsConfiguration)=>void,fail=false;const error=Object.assign(new Error('original'),{retryAfterSeconds:7200});
 const f=fixture({readConfiguration:async active=>{if(++reads===1)return configuration;if(fail)throw error;signal=active;return new Promise(resolve=>{release=resolve;});}});
 try{await f.controller.load(section);const input=f.controller.quietInputs(section).find(input=>input.id==='configuration')!,pending=input.refresh();f.controller.setQuietInputActive(true);assert.equal(signal?.aborted,true);f.controller.setQuietInputActive(false);release({...configuration,path:'/stale'});assert.deepEqual(await pending,{kind:'deferred'});assert.equal(f.controller.configurationMatches('integrations.max_webhooks')?.[0]?.path,configuration.path);
  fail=true;const before=f.controller.getSnapshot(section),result=await input.refresh();assert.equal(result.kind,'unreadable');if(result.kind==='unreadable')assert.equal(result.error,error);assert.equal(f.controller.getSnapshot(section),before);
 }finally{f.dispose();}
});
