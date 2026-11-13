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

test('the owner sees Details (what the browser did, the code and when) behind the owner mark; anyone else does not', async context => {
  const loader = registerHooks({load(url, ctx, next) {
    if (url.endsWith('.module.css')) return {format: 'module', source: 'export default new Proxy({}, {get: (_, key) => String(key)});', shortCircuit: true};
    if (url.endsWith('.tsx')) return {format: 'module', source: transformTsx(url), shortCircuit: true};
    return next(url, ctx);
  }});
  context.after(() => loader.deregister());
  const {PlaybackFailure} = await import('../src/kit/states/PlaybackFailure.tsx');
  const owner = {paragraphs: ['Chrome on a Mac stopped playing this at 1:12:40.', 'It was playing the original file (HEVC Main 10, 4K), with no converting.'], codes: ['playback_failed'], time: '4:05 PM', copyText: 'x'};
  const draw = (props: Record<string, unknown>) => renderToStaticMarkup(createElement(PlaybackFailure, {error: 'playback_failed', onRetry: async () => {}, onClose() {}, onError() {}, ...props}));
  const desktop = draw({owner});
  assert.match(desktop, /data-owner-diagnostics/); assert.match(desktop, /For you, as the owner/);
  assert.match(desktop, /stopped playing this at 1:12:40/); assert.match(desktop, /HEVC Main 10, 4K/); assert.match(desktop, /<code>playback_failed<\/code>/); assert.match(desktop, /Copy details/);
  assert.ok(desktop.indexOf('>Close<') < desktop.indexOf('data-owner-diagnostics'), 'under the buttons, as frame 8 draws it');
  const phone = draw({owner, phone: true});
  assert.match(phone, /aria-expanded="false"[^>]*>Details/, 'a phone keeps it behind Details, as frame 10 draws it');
  assert.doesNotMatch(phone, /data-owner-diagnostics/);
  assert.match(draw({owner, layout: 'compact'}), /data-owner-diagnostics/, 'the title page\'s card has it too');
  for (const props of [{}, {phone: true}, {layout: 'compact'}]) assert.doesNotMatch(draw(props), /data-owner-diagnostics|For you, as the owner|>Details</, 'nobody else sees it');
});

