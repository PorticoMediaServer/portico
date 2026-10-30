import test from 'node:test';
import assert from 'node:assert/strict';
import * as React from 'react';
import {screenModule} from './helpers/screen-harness.mjs';
import {NativeShellNavigation, nativeEntryRoute, nativeEntryRouteSupported} from '../src-next/shell/routes.ts';
import {nativeDestinationLifetime} from '../src-next/screens/destinations/lifetime.ts';
import {openNativeReadingBookEntry} from '../src-next/screens/read/reading-books-open.ts';
const authority = {serverId: 'server', accountId: 'account', profileId: 'profile', epoch: 'epoch', viewerFence: 'fence'};

for (const destination of ['search', 'saved'] as const) test(`${destination} Reading and collection preserve their original Back target`, async () => {
  const nav = new NativeShellNavigation(authority, () => authority);
  nav.open({kind: 'shell', destination: {kind: destination}}, authority);
  const origin = nav.getSnapshot().route;
  const listeners = new Set<() => void>();
  let member: any = {kind: 'ebook', id: 'item', libraryId: 'books', title: 'Actual'};
  const context: any = {navigation: nav, current: () => true, authority: () => authority, subscribeAuthority: nav.subscribe,
    session: () => ({sessionFamilyId: 'family', viewer: {...authority, authority: 'local'}}), libraries: () => [{id: 'books', kind: 'book'}],
    api: {reading: () => ({versions: async () => ({chosenSourceId: 'actual', versions: [{sourceId: 'actual', format: 'epub', inventoryAvailable: true}]}), dispose() {}})}};
  const common = {navigate: (entry: any) => {assert.equal(nativeEntryRouteSupported(entry), true); nav.open(nativeEntryRoute(entry), authority);}, reportError: assert.fail};
  const {createNativeEntryActions} = await screenModule(new URL('../src-next/screens/destinations/entry-actions.ts', import.meta.url), {
    react: {...React, default: React}, './lifetime': {nativeDestinationLifetime}, '../read/reading-books-open': {openNativeReadingBookEntry},
    '../native-screen-services': {nativeScreenServices: () => common}, '../watch': {nativeWatchEntryMenu: () => undefined},
  });
  const actions = createNativeEntryActions(context, {current: () => nav.getSnapshot().route === origin, contains: (value: any) => value === member,
    subscribe: (fn: () => void) => {listeners.add(fn); return () => listeners.delete(fn);}});
  try {
    actions.open(member);
    for (let i = 0; i < 8; i++) await Promise.resolve();
    assert.equal(nav.getSnapshot().route.kind, 'read-book');
    nav.back(authority); assert.equal(nav.getSnapshot().route, origin);
    member = {kind: 'collection', id: 'collection', libraryId: 'books', title: 'Collection'};
    actions.open(member); assert.equal(nav.getSnapshot().route.kind, 'collection');
    nav.back(authority); assert.equal(nav.getSnapshot().route, origin);
  } finally {actions.dispose(); nav.dispose();}
});
