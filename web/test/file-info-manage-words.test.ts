import test from 'node:test';
import assert from 'node:assert/strict';
import {registerHooks} from 'node:module';
import {createElement} from 'react';
import {renderToStaticMarkup} from 'react-dom/server';
import {defaultI18n} from '@i18n';
import {ApiError} from '@core/http.ts';
import {transformTsx} from './transform-tsx.mjs';

const hooks = () => registerHooks({load(url, ctx, next) {
  if (url.endsWith('.module.css')) return {format: 'module', source: 'export default new Proxy({}, {get: (_, key) => String(key)});', shortCircuit: true};
  if (url.endsWith('.tsx')) return {format: 'module', source: transformTsx(url), shortCircuit: true};
  return next(url, ctx);
}});

test('File info counts one version as "1 version"', () => {
  assert.equal(defaultI18n.t('next.overlays.versionCount', {count: 1}), '1 version');
  assert.equal(defaultI18n.t('next.overlays.versionCount', {count: 3}), '3 versions');
});

// Rule 25: the owner and administrators both receive the location and manage block, so it says so to both.
test('the location and manage block says who sees it, with no Owner only label, at both layouts', async context => {
  const loader = hooks(); context.after(() => loader.deregister());
  const {FileInfoBody} = await import('../src/kit/overlays/FileInfo.tsx');
  const model = {heading: 'File info', files: [], owner: {heading: 'Location and manage', locations: [{id: 'f1', versionId: 'v1', path: '/media/Film (2026)/Film (2026).mkv', actions: []}], actions: []}};
  for (const layout of ['desktop', 'phone'] as const) {
    const markup = renderToStaticMarkup(createElement(FileInfoBody, {model: model as never, layout, onManage() {}}));
    assert.match(markup, /Owner and administrators · Members never see this\./, layout);
    assert.doesNotMatch(markup, />Owner only</, layout);
  }
});

// A refused Split, Merge or Delete says why in one line with Try again: the server's own reason when it sent one.
test('a refused manage command shows the server\'s reason, or what did not happen, with Try again', async context => {
  const loader = hooks(); context.after(() => loader.deregister());
  const {ManageRefusal} = await import('../src/screens/detail/FileManagement.tsx');
  const reason = renderToStaticMarkup(createElement(ManageRefusal, {error: new ApiError(409, 'version_in_use', 'Someone is watching this version.', false), fallback: 'web.manage.splitRefused', retry() {}}));
  assert.match(reason, /Someone is watching this version\./); assert.match(reason, />Try again</);
  const bare = renderToStaticMarkup(createElement(ManageRefusal, {error: new ApiError(500, 'request_failed', 'Request failed (500).', true), fallback: 'web.manage.mergeRefused', retry() {}}));
  assert.match(bare, /The titles could not be merged\./); assert.doesNotMatch(bare, /Request failed/);
  const unknown = renderToStaticMarkup(createElement(ManageRefusal, {error: new Error('offline'), fallback: 'web.manage.deleteRefused', retry() {}}));
  assert.match(unknown, /The version could not be deleted\./);
});
