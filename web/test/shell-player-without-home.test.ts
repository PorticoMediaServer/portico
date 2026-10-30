import test from 'node:test';
import assert from 'node:assert/strict';
import {readFileSync} from 'node:fs';
import {HttpLocalApi} from '@core/index.ts';
import {HomeService} from '@core/home-service.ts';
import {FreshnessHub} from '@core/freshness-hub.ts';
import {assembleShellBindings} from '../src/shell/assembly.tsx';
import type {ShellAssemblyContext} from '../src/shell/bootstrap.ts';
import {createScreenAlbumPlayEffect, createScreenPlayEffect} from '../src/screens/browse/playback-effect.ts';
import {quietClock} from './settings-quiet-clock.ts';

// Region values as the registry publishes them; an older server publishes nothing else, so the rest use their declared defaults.
const region = {'region.locale': 'en-US', 'region.hourCycle': 'auto', 'region.formats': 'auto', 'region.dateOrder': 'auto', 'region.firstDay': 'auto', 'region.sizeUnits': 'binary'};
const appearance = {'appearance.motion': 'follow', 'appearance.showBackdrops': true, 'appearance.cardSizePercent': 100, 'appearance.textSize': 'standard',
  'appearance.layout': 'auto', 'appearance.libraryView': 'grid', 'appearance.episodesLayout': 'auto'};
const preferences = {deviceClass: 'web', effective: {...region, ...appearance}, effectiveSource: {}, clampedFields: [], registryRevision: '1', documents: [],
  registry: {revision: '1', fields: []}};
const viewer = {serverId: 'server', authority: 'local' as const, accountId: 'account', profileId: 'profile', role: 'member' as const};
const session = {sessionFamilyId: 'family', accessToken: 'test-token', deviceId: 'device', installationId: 'install', authorizationHorizon: new Date(Date.now() + 3600000).toISOString(), viewer};

/** A protected shell whose Home either never loads or answers 503. Nothing here reads the Home document. */
async function shell(home: 'never' | '503') {
  const api = new HttpLocalApi('https://portico.test', '', (async () => new Response(null, {status: 503})) as typeof fetch);
  let homeReads = 0;
  const service = new HomeService({request: async () => {homeReads++;throw Object.assign(new Error('Service Unavailable'), {status: 503, code: 'server_unavailable'});}});
  if (home === '503') await service.load().catch(() => {});
  assert.equal(service.getSnapshot().document, undefined, 'there is no Home document');
  const clock = quietClock(), hub = new FreshnessHub({...clock.env, gatherMs: 0, settleMs: 0, backoffMs: [1000]});
  const context = {api, session: () => session, events: {on: () => () => {}}, freshness: {hub}, home: service, artwork: {},
    facts: {serverName: 'Server', preferences, identity: {viewer, serverName: 'Server', libraries: [], capabilities: {}}}, epoch: 1, isCurrent: () => true,
    signOut: () => {}, switchProfile: async () => {}, switchServer: async () => {}} as unknown as ShellAssemblyContext;
  const assembly = await assembleShellBindings(context);
  return {assembly, homeReads: () => homeReads};
}

globalThis.matchMedia ??= (() => ({matches: false, addEventListener() {}, removeEventListener() {}})) as never;

for (const home of ['never', '503'] as const) {
  test(`P1: with Home ${home === 'never' ? 'never loaded (a cold deep link)' : 'answering 503'}, item and album Play start playback`, async t => {
    const {assembly} = await shell(home);
    t.after(() => assembly.dispose());
    const runtime = assembly.getBinding().playback?.runtime;
    assert.ok(runtime, 'the player exists from the session alone');
    const started: unknown[] = [];
    // Playback itself is mocked here; the real runtime playing with no Home fence is proven in playback-runtime-adapter.
    runtime.playFromScreen = async target => {started.push(['screen', target]);};
    runtime.playFromQueue = async source => {started.push(['queue', source]);};
    const play = createScreenPlayEffect({isCurrent: () => true, runtime: () => assembly.getBinding().playback?.runtime});
    await play({libraryId: 'library', itemId: 'item', startSeconds: 0}, new AbortController().signal);
    const album = createScreenAlbumPlayEffect({isCurrent: () => true, runtime: () => assembly.getBinding().playback?.runtime});
    await album('album_1', false, new AbortController().signal, () => true);
    assert.deepEqual(started, [['screen', {libraryId: 'library', itemId: 'item', startSeconds: 0}], ['queue', {container: {kind: 'album', id: 'album_1'}}]], 'never a refusal');
  });
}

test('P1: the shell builds its player from the session, never from the Home document', () => {
  const assembly = readFileSync(new URL('../src/shell/assembly.tsx', import.meta.url), 'utf8');
  assert.match(assembly, /new HomeDocumentResources<Selection>\(/, 'only Home\'s own selection follows the Home document');
  assert.doesNotMatch(assembly, /HomeDocumentResources<Player>/);
  assert.match(assembly, /try \{playerResource = createPlayer\(\);/);
  assert.doesNotMatch(assembly.slice(assembly.indexOf('const createPlayer'), assembly.indexOf('try {playerResource')), /context\.home|document\./, 'player construction reads nothing from Home');
});
