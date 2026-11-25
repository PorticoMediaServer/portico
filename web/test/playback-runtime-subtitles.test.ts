import test from 'node:test';
import assert from 'node:assert/strict';
import {playbackFence, queueScope} from '../../packages/player-core/src/index.ts';
import type {SubtitlePlan} from '@core/subtitles.ts';
import {WebShellPlaybackRuntime} from '../src/platform/playback/runtime.ts';

const scope = queueScope({serverId: 'server', authority: 'local', accountId: 'account', profileId: 'profile',
  controllerId: 'controller', controllerEpoch: 'epoch', commandLaneId: 'lane'});
const fence = playbackFence({kind: 'queue', playback: {scope, run: 1}, itemId: 'film', sourceId: 'version',
  entryId: 'entry', sessionId: 'session', generation: 1});

function makePlan(overrides: Partial<SubtitlePlan> = {}): SubtitlePlan {
  const selected = overrides.selected === undefined ? {
    id: 'sub_ass', sourceId: 'version', language: 'en', title: 'English', format: 'ass',
    enabled: true, forced: false, hearingImpaired: false, renderer: 'client' as const,
    offsetUs: '0', revision: 1,
    deliveries: [{format: 'ass', url: '/v1/media/subtitles/grant1/sub_ass/1/original.ass'}],
  } : overrides.selected;
  return {
    sessionId: 'session', generation: 1, sourceId: 'version',
    mode: 'track', renderer: 'client', revision: 'plan1', offsetUs: '0',
    documentUrl: null, selected,
    resources: selected ? [selected] : [],
    ...overrides,
  } as SubtitlePlan;
}

const settle = async () => { for (let i = 0; i < 8; i++) await Promise.resolve(); };

test('renderDrawnSubtitles renders plain ASS and enforces ownership via offers.subtitlePlan when stack.subtitles is absent', async () => {
  const runtime = Object.create(WebShellPlaybackRuntime.prototype) as any;
  let plan: SubtitlePlan | null = makePlan();
  const shown: unknown[][] = [], sidecars: unknown[] = [];
  let syncs = 0;
  const stack: any = {
    sessionKey: '1:session:1', expanded: true, retired: false, life: new AbortController(),
    web: {slot: {nativeSurfaceActive: false, selectSidecarSubtitle: (id: string | null) => sidecars.push(id)}},
    service: {getSnapshot: () => ({session: {id: 'session', generation: 1}, intentId: 1, playbackRun: 1}), changePlayback: async () => {}},
  };
  const assScript = '[Script Info]\n[Events]\nFormat: Layer, Start, End, Style, Name, MarginL, MarginR, MarginV, Effect, Text\nDialogue: 0,0:00:01.00,0:00:03.00,Default,,0,0,0,,Hello drawn\n';
  Object.assign(runtime, {
    stack, video: {},
    source: {
      api: {baseUrl: 'https://server.example', mediaUrl: (path: string) => `https://server.example${path}`},
      settings: {subtitlePreferences: () => ({size: 'medium', color: 'white', background: 'translucent', position: 'bottom', font: 'sans', keepStyling: false, useSystemCaptionStyle: false})},
    },
    env: {
      fetch: async () => new Response(assScript, {status: 200, headers: {'Content-Type': 'text/plain'}}),
      timers: {setTimeout, clearTimeout},
    },
    surface: {
      show: (cues: unknown[], offset: number) => shown.push([cues, offset]),
      guard: () => {},
      clear: () => {},
    },
    snapshot: {playback: {playbackRun: 1}, offers: {subtitlePlan: plan}},
    current: (s: unknown) => s === stack,
    offers: () => plan ? {subtitlePlan: plan} : null,
    sync: () => { syncs++; },
  });

  runtime.renderSubtitles(stack);
  await settle();
  assert.equal(shown.length, 1, 'plain ASS cues were rendered without stack.subtitles');
  assert.equal((shown[0]![0] as {text: string}[])[0]!.text, 'Hello drawn');
  assert.equal(sidecars.at(-1), 'sub_ass');
  assert.ok(syncs >= 1);

  // Changing plan revision invalidates ownership of an in-flight read.
  let resolveFetch!: (response: Response) => void;
  runtime.env.fetch = () => new Promise<Response>(resolve => { resolveFetch = resolve; });
  plan = makePlan({revision: 'plan2'});
  runtime.renderSubtitles(stack);
  plan = makePlan({revision: 'plan3'});
  resolveFetch(new Response(assScript, {status: 200, headers: {'Content-Type': 'text/plain'}}));
  await settle();
  assert.equal(shown.length, 1, 'stale plan revision cannot render after offers changed');
});

test('renderDrawnSubtitles triggers burnUndrawable fallback when drawing is unavailable and stack.subtitles is absent', async () => {
  const runtime = Object.create(WebShellPlaybackRuntime.prototype) as any;
  const plan = makePlan({offsetUs: '500000'});
  const changeCalls: unknown[] = [];
  let syncs = 0;
  const stack: any = {
    sessionKey: '1:session:1', expanded: true, retired: false, life: new AbortController(),
    web: {slot: {nativeSurfaceActive: false, selectSidecarSubtitle: () => {}}},
    service: {
      getSnapshot: () => ({session: {id: 'session', generation: 1}, intentId: 1, playbackRun: 1}),
      changePlayback: async (request: unknown) => { changeCalls.push(request); },
    },
  };
  Object.assign(runtime, {
    stack, video: {}, // No ownerDocument -> createDrawnSubtitleSurface returns undefined -> triggers failed -> burnUndrawable.
    source: {
      api: {baseUrl: 'https://server.example', mediaUrl: (path: string) => `https://server.example${path}`},
      settings: {subtitlePreferences: () => ({size: 'medium', color: 'white', background: 'translucent', position: 'bottom', font: 'sans', keepStyling: true, useSystemCaptionStyle: false})},
    },
    env: {timers: {setTimeout, clearTimeout}},
    snapshot: {playback: {playbackRun: 1}, offers: {subtitlePlan: plan}},
    current: (s: unknown) => s === stack,
    offers: () => ({subtitlePlan: plan}),
    sync: () => { syncs++; },
  });

  runtime.renderSubtitles(stack);
  await settle();
  assert.deepEqual(changeCalls, [{subtitleTrackId: 'sub_ass', subtitleDelivery: 'burn', subtitleOffsetMs: 500}]);
  assert.equal(runtime.runtimeProblem, undefined, 'problem cleared once burn replan succeeded');
  assert.ok(syncs >= 2);
});

test('advancePreplay applies pending subtitle choice from snapshot.offers.subtitlePlan when stack.subtitles is absent', async () => {
  const runtime = Object.create(WebShellPlaybackRuntime.prototype) as any;
  const selectedCalls: unknown[][] = [];
  const plan = makePlan({
    mode: 'off', selected: null,
    resources: [{
      id: 'sub_fr', sourceId: 'version', language: 'fr', title: 'French', format: 'vtt',
      enabled: true, forced: false, hearingImpaired: false, renderer: 'external_text',
      offsetUs: '0', revision: 'rev1',
    }],
  });
  const stack: any = {
    preplay: {versionId: 'version', progress: {choice: {itemId: 'film', subtitle: {resourceId: 'sub_fr'}}}},
  };
  Object.assign(runtime, {
    stack,
    snapshot: {session: {versionId: 'version'}, offers: {subtitlePlan: plan}},
    current: (s: unknown) => s === stack,
    matches: () => true,
    selectSubtitle: async (f: unknown, id: string | null) => { selectedCalls.push([f, id]); },
    sync: () => {},
  });

  runtime.advancePreplay(stack, {itemId: 'film', session: {id: 'session', generation: 1}}, fence);
  assert.equal(stack.preplay, undefined, 'preplay choice consumed');
  await settle();
  assert.deepEqual(selectedCalls, [[fence, 'sub_fr']]);
});

test('setSubtitleOffset refreshes offers and re-renders subtitles before sync', async () => {
  const runtime = Object.create(WebShellPlaybackRuntime.prototype) as any;
  const order: string[] = [];
  const plan = makePlan();
  let active = true;
  const stack: any = {
    life: new AbortController(),
    service: {changePlayback: async (request: unknown) => { order.push(`change:${JSON.stringify(request)}`); }},
    offers: {refresh: async (options: unknown) => { order.push(`refresh:${JSON.stringify(options)}`); }},
  };
  Object.assign(runtime, {
    stack,
    snapshot: {offers: {subtitlePlan: plan}, playback: {playbackRun: 1}},
    current: (s: unknown) => active && s === stack,
    require: () => stack,
    retainMediaStart: () => () => { order.push('release'); },
    renderSubtitles: () => { order.push('render'); },
    sync: () => { order.push('sync'); },
    airPlayCanReplan: () => false,
  });

  await runtime.setSubtitleOffset(fence, 250);
  assert.deepEqual(order, [
    'change:{"subtitleTrackId":"sub_ass","subtitleOffsetMs":250}',
    'refresh:{"quiet":true}',
    'render',
    'sync',
    'release',
  ]);

  // If stack retires during changePlayback, refresh/render/sync are skipped cleanly.
  order.length = 0;
  stack.service.changePlayback = async () => { order.push('change'); active = false; };
  await runtime.setSubtitleOffset(fence, 500);
  assert.deepEqual(order, ['change', 'release']);
});
