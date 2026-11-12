import test from 'node:test';
import assert from 'node:assert/strict';
import {registerHooks} from 'node:module';
import {createElement} from 'react';
import {renderToStaticMarkup} from 'react-dom/server';
import {transformTsx} from './transform-tsx.mjs';

// states.html frames 8 and 10: a playback that stopped inside the full player is drawn as the kit's state: its icon,
// title and sentence, a white Try again, a glass button, and Report a problem as a text link.
test('the full player draws a stopped playback as the drawn state, on a desktop and on a phone', async context => {
  const loader = registerHooks({load(url, ctx, next) {
    if (url.endsWith('.module.css')) return {format: 'module', source: 'export default new Proxy({}, {get: (_, key) => String(key)});', shortCircuit: true};
    if (url.endsWith('.tsx')) return {format: 'module', source: transformTsx(url), shortCircuit: true};
    return next(url, ctx);
  }});
  context.after(() => loader.deregister());
  const {PlaybackFailure} = await import('../src/kit/states/PlaybackFailure.tsx');
  const draw = (phone: boolean, versions = false) => renderToStaticMarkup(createElement(PlaybackFailure, {error: 'playback_failed', phone, onRetry: async () => {}, onClose() {}, onReport() {}, onError() {},
    ...(versions ? {onVersions() {}} : {})}));
  for (const phone of [false, true]) {
    const html = draw(phone);
    assert.match(html, phone ? /class="layout landscape"/ : /class="layout player"/);
    assert.match(html, /class="stateIcon"/, 'the icon in its circle');
    assert.match(html, /<h1[^>]*>Playback stopped<\/h1>/);
    const order = ['Try again', 'Close', 'Report a problem'].map(label => html.indexOf(`>${label}<`));
    assert.ok(order.every(index => index > 0) && order[0] < order[1] && order[1] < order[2], 'Try again, then Close, then Report a problem');
    assert.match(html, /data-variant="primary"[^>]*>(?:(?!<\/button>).)*Try again/, 'Try again is the white button');
  }
  const withVersions = draw(false, true);
  assert.ok(withVersions.indexOf('data-variant="glass"') < withVersions.indexOf('>Close<'), 'another version takes the glass button; Close becomes a link');
});
