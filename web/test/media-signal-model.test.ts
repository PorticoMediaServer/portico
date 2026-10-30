import test from 'node:test';
import assert from 'node:assert/strict';
import {registerHooks} from 'node:module';
import {createElement} from 'react';
import {renderToStaticMarkup} from 'react-dom/server';
import {defaultI18n} from '@i18n';
import {musicSignal, musicSignalAccessibleName, formatSignalText, type MusicSignalInput} from '@core/music-signal.ts';
import {transformTsx} from './transform-tsx.mjs';

const base: MusicSignalInput = {source: {codec: 'flac', lossless: true, bitDepth: 24, sampleRate: 96000},
  server: {route: 'original'}, output: {platform: 'browser', device: 'browser'}, eqOn: false, eqApplied: false, normalization: 'off', levelingApplied: false};

test('the media line consumes canonical Music models, including folded DSD and loss text', async context => {
  const loader = registerHooks({load(url, context, next) {
    if (url.endsWith('.module.css')) return {format: 'module', source: 'export default new Proxy({}, {get: (_, key) => String(key)});', shortCircuit: true};
    if (url.endsWith('.tsx')) return {format: 'module', source: transformTsx(url), shortCircuit: true};
    return next(url, context);
  }});
  context.after(() => loader.deregister());
  const {SignalLine} = await import('../src/kit/media/SignalLine.tsx');
  for (const input of [base, {...base, source: {codec: 'dsd', dsdMultiple: 64}, server: {route: 'converted' as const, dsdConverted: true, delivered: {codec: 'pcm', sampleRate: 48000}}},
    {...base, source: {codec: 'truehd', atmos: true}, output: {...base.output, decodedLayout: '7.1', atmosLost: true}}]) {
    const model = musicSignal(input);
    const html = renderToStaticMarkup(createElement(SignalLine, {model, translate: defaultI18n.t}));
    assert.ok(html.includes(musicSignalAccessibleName(model, defaultI18n.t)!.replaceAll('&', '&amp;').replaceAll('"', '&quot;')));
    if (model.stateLoss) assert.ok(html.includes(formatSignalText(model.stateLoss, defaultI18n.t)));
    if (model.foldSourceIntoState) assert.equal((html.match(/DSD64/g) ?? []).length, 2, 'one accessible name and one drawn source, without a duplicate source column');
    assert.doesNotMatch(html, /data-healthy="true"/);
  }
  assert.equal(renderToStaticMarkup(createElement(SignalLine, {model: musicSignal({...base, source: {}, server: undefined}), translate: defaultI18n.t})), '');
});
