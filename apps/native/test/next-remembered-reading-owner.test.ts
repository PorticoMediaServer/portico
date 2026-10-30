import test from 'node:test';
import assert from 'node:assert/strict';
import * as React from 'react';
import {screenModule} from './helpers/screen-harness.mjs';
import {closeReadingResource, createReadingSessionOwner} from '../src-next/screens/read/reading-session-owner';
import {rememberedReadingHistoryPaused} from '../src-next/shell/remembered-reading-settings';

test('remembered publication keeps its original lease and withholds unknown history on guest remount', async () => {
  const {createRememberedReadingOwner} = await screenModule(new URL('../src-next/shell/remembered-reading-owner.ts', import.meta.url), {
    react: {...React, default: React},
    '../screens/read/reading-session-owner': {closeReadingResource, createReadingSessionOwner},
    './remembered-reading-settings': {rememberedReadingHistoryPaused},
  });
  let root = true, entryCurrent = true, opens = 0, closes = 0, paused: boolean | undefined;
  const listeners = new Set<() => void>(), signal = new AbortController();
  const entry = {id: 'original-entry', itemId: 'original-item'}, source = {sourceId: 'original-source', revision: 'a'.repeat(64)};
  const session = {manifest: {itemId: entry.itemId, ...source}, signal: signal.signal,
    progress: async () => ({progress: null}), close: async () => {closes++;},
    headPDF: async () => ({}), readRange: async () => new Uint8Array(), readAsset: async () => new Uint8Array(),
    page: async () => ({}), saveProgress: async () => ({saved: false, progress: null})};
  const device = {current: () => root, publicationSource: () => {if (!entryCurrent) throw new Error('removed'); return source;},
    device: {playabilityReason: () => entryCurrent ? null : 'unavailable', subscribe: (listener: () => void) => {listeners.add(listener); return () => {listeners.delete(listener);};}},
    readingSession: async (captured: unknown, isPaused: () => boolean) => {assert.equal(captured, entry); opens++; paused = isPaused(); return session;}};
  const reading = createRememberedReadingOwner(device, entry, {effective: {}}, () => root);
  await reading.owner.ensure(); assert.equal(paused, true); const first = reading.owner.attach(); first.release();
  const second = reading.owner.attach(); assert.equal(second.session.manifest, session.manifest); assert.equal(opens, 1); assert.equal(closes, 0);
  entryCurrent = false; for (const listener of [...listeners]) listener(); await reading.owner.dispose();
  assert.equal(second.current(), false); assert.equal(closes, 1); assert.equal(reading.owner.snapshot().phase, 'retired');
});

test('a changed retained source cannot open under a borrowed original owner', async () => {
  const {createRememberedReadingOwner} = await screenModule(new URL('../src-next/shell/remembered-reading-owner.ts', import.meta.url), {
    react: {...React, default: React}, '../screens/read/reading-session-owner': {closeReadingResource, createReadingSessionOwner},
    './remembered-reading-settings': {rememberedReadingHistoryPaused},
  });
  let revision = 'a'.repeat(64), opens = 0;
  const device = {current: () => true, publicationSource: () => ({sourceId: 'source', revision}),
    device: {playabilityReason: () => null, subscribe: () => () => {}}, readingSession: async () => {opens++; throw new Error('must not open');}};
  const reading = createRememberedReadingOwner(device, {id: 'entry', itemId: 'item'}, {effective: {}}, () => true);
  revision = 'b'.repeat(64);
  assert.equal(reading.owner.current(), false); await assert.rejects(async () => reading.owner.ensure()); assert.equal(opens, 0);
  await reading.owner.dispose();
});
