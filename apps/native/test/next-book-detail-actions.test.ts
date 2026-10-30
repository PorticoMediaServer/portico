import test from 'node:test';
import assert from 'node:assert/strict';
import React, {type ReactElement} from 'react';
import {readFileSync} from 'node:fs';
import {fileURLToPath} from 'node:url';
import {registerHooks} from 'node:module';
import {transformSync} from 'rolldown/utils';
import type {ReadingListMembership, ReadingReadMark} from '../../../packages/client-core/src';

const membership = (listed: boolean): ReadingListMembership => Object.freeze({itemId: 'book', listed, revision: 4, viewerFence: 'a'.repeat(64)});
const mark = (read: boolean): ReadingReadMark => Object.freeze({itemId: 'book', read, revision: 9, viewerFence: 'a'.repeat(64)});
const target = new URL('../src-next/screens/read/book-detail-actions.tsx', import.meta.url).href;
const moduleURL = (source: string) => `data:text/javascript,${encodeURIComponent(source)}`;
registerHooks({
  resolve(specifier, context, next) {
    if (context.parentURL === target) {
      if (specifier === 'react-native') return {url: moduleURL(`export const View='View';`), shortCircuit: true};
      if (specifier.endsWith('/kit/controls')) return {url: moduleURL(`export const Button='Button',ResumeButton='ResumeButton';`), shortCircuit: true};
      if (specifier.endsWith('/kit/theme')) return {url: moduleURL(`export const useTheme=()=>({colors:{accentStrong:'accent'}});`), shortCircuit: true};
      if (specifier.endsWith('/i18n/src')) return {url: moduleURL(`export const defaultI18n={t:key=>key};`), shortCircuit: true};
    }
    return next(specifier, context);
  },
  load(url, context, next) {
    if (url !== target) return next(url, context);
    const filename = fileURLToPath(url), result = transformSync(filename, readFileSync(filename, 'utf8'), {lang: 'tsx', jsx: {runtime: 'automatic'}, target: 'es2022'});
    if (result.errors.length) throw new AggregateError(result.errors);
    return {format: 'module', source: result.code, shortCircuit: true};
  },
});
const {BookDetailActions} = await import('../src-next/screens/read/book-detail-actions.tsx');
type Props = Parameters<typeof BookDetailActions>[0];
function buttons(props: Partial<Props> = {}) {
  const tree = BookDetailActions({format: 'epub', canDownload: true, onRead() {}, onReadMark() {}, onList() {}, onDownload() {},
    downloadRef: {current: null}, ...props});
  const row = React.Children.toArray(tree.props.children)[0] as ReactElement<{children: React.ReactNode}>;
  return React.Children.toArray(row.props.children) as ReactElement<Record<string, any>>[];
}

// Actual presenter vectors; not a device/frame or service-mutation acceptance test.
test('EPUB frame3 offers primary, exact My List, Download, More in authored order, not audiobook Finished', () => {
  const listed = membership(false);
  const calls: unknown[] = [], anchor = {current: null};
  const row = buttons({listed, readMark: mark(true), downloadRef: anchor,
    onList: offer => {calls.push(offer);}, onDownload: () => {calls.push('download');}, onMore: () => {calls.push('more');}});
  assert.deepEqual(row.map(button => button.props.icon), ['i-book', 'i-plus', 'i-dl', 'i-more']);
  for (const round of row.slice(1)) assert.deepEqual(round.props.style, {height: 46, width: 46});
  assert.equal(row[2].props.ref, anchor);
  row[1].props.onPress();row[2].props.onPress();row[3].props.onPress();
  assert.equal(calls[0], listed);assert.deepEqual(calls.slice(1), ['download', 'more']);
});

test('listed EPUB uses the real membership boolean and Resume keeps its original callback', () => {
  const listed = membership(true);let reads = 0;
  const row = buttons({listed, saved: .4, onRead: () => {reads++;}, onMore() {}});
  assert.equal(row[0].type, 'ResumeButton');assert.equal(row[0].props.progress, .4);
  assert.equal(row[1].props.icon, 'i-check');assert.equal(row[1].props.accessibilityLabel, 'next.home.removeMyList');
  row[0].props.onPress();assert.equal(reads, 1);
});

test('missing membership or menu offer never creates an invented or inert action', () => {
  const row = buttons({canDownload: false});
  assert.deepEqual(row.map(button => button.props.icon), ['i-book', 'i-dl']);
  assert.equal(row[1].props.disabled, true);
});

test('non-EPUB reading keeps its existing offered read mark and download row', () => {
  const offeredMark = mark(true);let offered: unknown;
  const row = buttons({format: 'pdf', readMark: offeredMark, onReadMark: value => {offered = value;},
    listed: membership(false), onMore() {}});
  assert.deepEqual(row.map(button => button.props.icon), ['i-book', 'i-check', 'i-dl']);
  assert.equal(row[1].props.accessibilityLabel, 'next.reader.markUnread');row[1].props.onPress();assert.equal(offered, offeredMark);
});
