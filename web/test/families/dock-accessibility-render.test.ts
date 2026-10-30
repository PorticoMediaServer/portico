import test from 'node:test';
import assert from 'node:assert/strict';
import {registerHooks} from 'node:module';
import {createElement} from 'react';
import {renderToStaticMarkup} from 'react-dom/server';
import {PlaybackDock, type DockPlayback, type QueuePlaybackFence} from '../../../packages/player-core/src/index.ts';
import {dockView} from '../../src/kit/dock/model.ts';
import type {DockPresentation, DockTranslate} from '../../src/kit/dock/types.ts';
import {transformTsx} from '../transform-tsx.mjs';

registerHooks({load(url, context, next) {
  if (url.endsWith('.module.css')) return {format: 'module', source: 'export default new Proxy({}, {get: (_, key) => String(key)});', shortCircuit: true};
  if (url.endsWith('.tsx')) return {format: 'module', source: transformTsx(url), shortCircuit: true};
  return next(url, context);
}});
const {Identity} = await import('../../src/kit/dock/parts.tsx');
const scope = {serverId: 'server', authority: 'local' as const, accountId: 'viewer', profileId: 'profile', controllerId: 'controller', controllerEpoch: 'epoch', commandLaneId: 'lane'};
const fence: QueuePlaybackFence = {kind: 'queue', playback: {scope, run: 1}, sessionId: 'session', itemId: 'item', sourceId: 'source', entryId: 'entry', generation: 1};
const playback: DockPlayback = {kind: 'video', fence, queue: null, positionSeconds: 14, playing: true, expanded: false};
const presentation: DockPresentation = {fence, identity: {id: 'item', kind: 'video', title: 'Current title', subtitle: [{id: 'year', role: 'context', text: '2021'}]}, durationSeconds: 100, controls: ['play', 'pause', 'seek'], skipBackSeconds: 10, skipForwardSeconds: 30, deviceName: 'Room TV'};
const t: DockTranslate = (id, values) => `${id}${values?.device ? ` ${values.device}` : ''}`;

test('the dock action names the actual current title and describes its subtitle', () => {
  const dock = new PlaybackDock(scope); dock.start(playback);
  const view = dockView(dock.getSnapshot(), [presentation])!;
  const html = renderToStaticMarkup(createElement(Identity, {view, binding: {getSnapshot: dock.getSnapshot, onAction() {}}, t, videoSurface: true}));
  const action = html.match(/<button[^>]*class="identity"[^>]*>/)?.[0];
  assert.ok(action);
  const labelledBy = action.match(/aria-labelledby="([^"]+)"/)?.[1]?.split(' ');
  const describedBy = action.match(/aria-describedby="([^"]+)"/)?.[1];
  assert.equal(labelledBy?.length, 2);
  assert.ok(describedBy);
  assert.ok(html.includes(`<span id="${labelledBy![0]}" class="accessible">next.dock.expand</span>`));
  assert.ok(html.includes(`<b id="${labelledBy![1]}">Current title</b>`));
  assert.ok(html.includes(`<small id="${describedBy}"><span data-fact-role="context">2021</span></small>`));
});

test('remote elapsed time remains visible while only phase copy belongs to the live region', () => {
  const dock = new PlaybackDock(scope);
  const remote = {id: 'remote', kind: 'cast' as const, sequence: '1', phase: 'playing' as const, playback, positionSeconds: 21};
  dock.beginRemote(remote, 0);
  const render = () => renderToStaticMarkup(createElement(Identity, {view: dockView(dock.getSnapshot(), [presentation])!, binding: {getSnapshot: dock.getSnapshot, onAction() {}}, t, phone: true}));
  const first = render();
  dock.remote({...remote, sequence: '2', positionSeconds: 22}, 1);
  const second = render();
  const status = (html: string) => html.match(/<span class="accessible" role="status" aria-atomic="true">([^<]*)<\/span>/)?.[1];
  assert.equal(status(first), 'next.dock.onDevice Room TV');
  assert.equal(status(second), status(first));
  assert.match(first, /<small[^>]*>Room TV<span class="gap" aria-hidden="true"><\/span>0:21<\/small>/);
  assert.match(second, /<small[^>]*>Room TV<span class="gap" aria-hidden="true"><\/span>0:22<\/small>/);
  assert.doesNotMatch(first, /<small[^>]*aria-live/);
  dock.remote({...remote, sequence: '3', phase: 'stopped', positionSeconds: 22}, 2);
  assert.equal(status(render()), 'next.dock.stopped');
});
