import assert from 'node:assert/strict';
import test from 'node:test';
import {createElement} from 'react';
import {renderToStaticMarkup} from 'react-dom/server';
import {Transport} from '../src/kit/dock/parts.tsx';
import {playbackPresentation} from '../src/shell/playback-model.ts';
import type {DockView} from '../src/kit/dock/types';

// The dock's Previous/Next say "chapter" only when they move by chapter: a book part the book's
// chapter list covers. A part without chapter data moves by part, as a track does.
const fence = {kind: 'queue', queueId: 'q1', entryId: 'e1', itemId: 'part-b', playbackRun: 1} as never;
const view = (byChapter: boolean, mode: DockView['mode'] = 'audiobook'): DockView => ({mode,
  playback: {kind: 'audiobook', fence, queue: null, positionSeconds: 10, playing: true, expanded: false},
  presentation: {fence, identity: {title: 'Book', subtitle: []} as never, controls: ['pause', 'seek', 'previous', 'next'], skipBackSeconds: 15, skipForwardSeconds: 30, ...(byChapter ? {byChapter} : {})}} as DockView);
const labels = (v: DockView) => [...renderToStaticMarkup(createElement(Transport, {view: v, binding: {getSnapshot: () => ({}) as never, onAction: () => {}}, t: (id: string) => id} as never))
  .matchAll(/aria-label="([^"]+)"/g)].map(m => m[1]).filter(label => /\.(previous|next)(Chapter)?$/.test(label!));

test('the dock says Previous/Next chapter on a book part its chapter list covers, and Previous/Next otherwise', () => {
  assert.deepEqual(labels(view(true)), ['next.dock.previousChapter', 'next.dock.nextChapter']);
  assert.deepEqual(labels(view(false)), ['next.dock.previous', 'next.dock.next']);
});

test('the dock presentation says a book part moves by chapter only when the book list is ready for it', () => {
  const ready = {bookId: 'book', itemId: 'part-b', phase: 'ready', current: 0, chapters: []};
  const snapshot = (kind: string, bookChapters: unknown) => ({presentation: {fence, kind, subject: {kind: 'audiobook', item: {id: 'part-b', title: 'Book'}}, durationSeconds: 60, controls: [], finished: false, rate: 1, volume: 1, muted: false}, bookChapters}) as never;
  const by = (s: never) => playbackPresentation(s, {t: (id: string) => id} as never, {foreground: () => null, ambient: () => null} as never, {back: 15, forward: 30})?.byChapter;
  assert.equal(by(snapshot('audiobook', ready)), true);
  for (const phase of ['loading', 'error']) assert.equal(by(snapshot('audiobook', {...ready, phase})), undefined, phase);
  assert.equal(by(snapshot('audiobook', null)), undefined);
  assert.equal(by(snapshot('music', ready)), undefined);
});
