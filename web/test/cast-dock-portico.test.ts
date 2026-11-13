import test from 'node:test';
import assert from 'node:assert/strict';
import {registerHooks} from 'node:module';
import {createElement} from 'react';
import {renderToStaticMarkup} from 'react-dom/server';
import {transformTsx} from './transform-tsx.mjs';

// Rule 27: the "Playing on" bar for one of the account's Portico devices has Next while its queue has one, and
// Subtitles and audio from its own tracks.
test('a Portico device\'s bar draws Next and Subtitles and audio only when it has them', async context => {
  const loader = registerHooks({load(url, ctx, next) {
    if (url.endsWith('.module.css')) return {format: 'module', source: 'export default new Proxy({}, {get: (_, key) => String(key)});', shortCircuit: true};
    if (url.endsWith('.tsx')) return {format: 'module', source: transformTsx(url), shortCircuit: true};
    return next(url, ctx);
  }});
  context.after(() => loader.deregister());
  const {CastDock} = await import('../src/kit/player/CastDock.tsx');
  const remote = {id: 'portico:pbs_1', sequence: 1, kind: 'portico', phase: 'playing', confirmed: true, itemId: 'itm_1', title: 'Walk Film', deviceName: 'Living room TV',
    positionKnown: true, positionSeconds: 90, durationSeconds: 180, paused: false};
  const tracks = {audio: [{id: 'a1', name: 'English'}, {id: 'a2', name: 'French'}], subtitles: [{id: 's1', name: 'English'}], audioId: 'a2', subtitleId: null};
  const props = (value: object) => ({controller: {} as never, visuals: {skipBackSeconds: 10, skipForwardSeconds: 30} as never, phone: false, reportError() {},
    snapshot: {remote: value, panel: null, disposed: false, cast: {audioTracks: [], textTracks: []}} as never});
  const full = renderToStaticMarkup(createElement(CastDock, props({...remote, next: {title: 'Next Film'}, tracks})));
  assert.match(full, /aria-label="Next"/); assert.match(full, /aria-label="Subtitles and audio"/); assert.match(full, />Play here</);
  const bare = renderToStaticMarkup(createElement(CastDock, props(remote)));
  assert.doesNotMatch(bare, /aria-label="Next"|Subtitles and audio/, 'neither without a next title or tracks');
  assert.match(bare, />Play here</, 'Play here while it plays (rule 27)');
});

test('while this browser plays too, a Portico device is one line with what failed and its Stop; a stopped one has none', async context => {
  const loader = registerHooks({load(url, ctx, next) {
    if (url.endsWith('.module.css')) return {format: 'module', source: 'export default new Proxy({}, {get: (_, key) => String(key)});', shortCircuit: true};
    if (url.endsWith('.tsx')) return {format: 'module', source: transformTsx(url), shortCircuit: true};
    return next(url, ctx);
  }});
  context.after(() => loader.deregister());
  const {CastDock} = await import('../src/kit/player/CastDock.tsx');
  const remote = {id: 'portico:pbs_1', sequence: 1, kind: 'portico', phase: 'playing', confirmed: true, itemId: 'itm_1', title: 'Walk Film', deviceName: 'Living room TV',
    positionKnown: true, positionSeconds: 90, durationSeconds: 180, paused: false, problem: 'The TV couldn\u2019t be reached.'};
  const props = (value: object, phone: boolean) => ({controller: {} as never, visuals: {skipBackSeconds: 10, skipForwardSeconds: 30} as never, phone, reportError() {}, alongside: true,
    snapshot: {remote: value, panel: null, disposed: false, cast: {audioTracks: [], textTracks: []}} as never});
  for (const phone of [true, false]) {
    const line = renderToStaticMarkup(createElement(CastDock, props(remote, phone)));
    assert.match(line, /data-cast-alongside/); assert.match(line, /Still playing on Living room TV · The TV couldn’t be reached\./); assert.match(line, />Stop</);
    assert.doesNotMatch(line, /data-mode="cast"|Play here/, 'not the full bar: this browser\'s own player keeps the screen');
    assert.equal(renderToStaticMarkup(createElement(CastDock, props({...remote, phase: 'stopped'}, phone))), '', 'a device that stopped needs no line');
  }
});
