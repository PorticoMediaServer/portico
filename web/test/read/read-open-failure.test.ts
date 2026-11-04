import assert from 'node:assert/strict';
import {test} from 'node:test';
import {createElement} from 'react';
import {renderToStaticMarkup} from 'react-dom/server';
import {registerHooks} from 'node:module';
import {transformTsx} from '../transform-tsx.mjs';
import {defaultI18n} from '../../../packages/i18n/src/index.ts';

registerHooks({load(url, context, next) {
  if (url.endsWith('.module.css')) return {format: 'module', source: 'export default new Proxy({}, {get: (_, key) => String(key)});', shortCircuit: true};
  if (url.endsWith('.tsx')) return {format: 'module', source: transformTsx(url), shortCircuit: true};
  return next(url, context);
}});
const {ReadOpenFailure, readFailureKind, readFailureReported} = await import('../../src/screens/read/ReadOpenFailure.tsx');
const {t} = defaultI18n;
const escape = (text: string) => text.replace(/&/g, '&amp;').replace(/</g, '&lt;').replace(/>/g, '&gt;').replace(/"/g, '&quot;').replace(/'/g, '&#x27;');
const render = (kind: 'failed' | 'busy' | 'ended' | 'tooLarge', page = true) => renderToStaticMarkup(createElement(ReadOpenFailure, {kind, page, onRetry() {}, onBack() {}}));

test('RD-8 (#693 review): the busy page says the server is busy in the catalogue\'s words and keeps Try again and Back', () => {
  const html = render('busy');
  assert.ok(html.includes(escape(t('error.busy'))), 'the busy line');
  assert.ok(!html.includes(escape(t('next.states.pageFailedBody'))), 'not the generic body');
  assert.ok(html.includes(escape(t('next.states.tryAgain')))); assert.ok(html.includes(escape(t('next.reader.back'))));
});

test('RD-7: an ended session says so and offers Open again; any other failure keeps the generic page', () => {
  const ended = render('ended');
  assert.ok(ended.includes(escape(t('next.reader.sessionEndedTitle')))); assert.ok(ended.includes(escape(t('next.reader.sessionEndedBody'))));
  assert.ok(ended.includes(escape(t('next.reader.openAgain')))); assert.ok(!ended.includes(escape(t('next.states.tryAgain'))));
  const failed = render('failed', false);
  assert.ok(failed.includes(escape(t('next.states.pageFailedBody')))); assert.ok(failed.includes(escape(t('next.states.tryAgain'))));
});

test('busy and ended explain themselves without a toast; every other failure is still reported', () => {
  const coded = (code: string) => Object.assign(new Error(code), {code});
  assert.equal(readFailureKind(coded('reading_sessions_busy')), 'busy');
  assert.equal(readFailureKind(coded('reading_session_expired')), 'ended');
  assert.equal(readFailureKind(coded('reading_source_changed')), 'failed'); assert.equal(readFailureKind(new Error('network')), 'failed');
  assert.equal(readFailureKind(coded('reading_publication_too_large')), 'tooLarge');
  assert.deepEqual((['busy', 'ended', 'tooLarge', 'failed'] as const).map(readFailureReported), [false, false, false, true]);
});

test('RD-5: a book too large to read says so with only Back, never Try again', () => {
  const html = render('tooLarge');
  assert.ok(html.includes(escape(t('next.reader.publicationTooLargeTitle')))); assert.ok(html.includes(escape(t('next.reader.publicationTooLargeBody'))));
  assert.ok(!html.includes(escape(t('next.states.tryAgain')))); assert.ok(html.includes(escape(t('next.reader.back'))));
});
