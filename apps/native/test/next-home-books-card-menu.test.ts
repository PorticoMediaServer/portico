import test from 'node:test';
import assert from 'node:assert/strict';
import React from 'react';
import {screenModule} from './helpers/screen-harness.mjs';

const react = {...React, default: React, useRef: () => ({current: null}), useSyncExternalStore: (_s: unknown, get: () => unknown) => get(), useEffect: () => {}};

async function card(reading: boolean) {
  const {HomeCard} = await screenModule(new URL('../src-next/kit/home/HomeRows.tsx', import.meta.url), {
    react, 'react-native': {Text: 'Text', View: 'View'},
    '../../platform/home-action-diagnostics': {},
    '../states': {},
    '../layout/types': {},
    '../layout': {},
    '../cards/caption-metrics': {},
    '../../../../../packages/client-core/src/presentation/kit-model': {},
    '../../../../../packages/client-core/src/presentation/server-text': {},
    '../../../../../packages/client-core/src/presentation/home-spotlight': {},
    '../../../../../packages/i18n/src': {},
    '../../../../../packages/client-core/src/presentation/continue-books': {continueBookFacts: () => ({reading, progress: .2})},
    '../../../../../packages/client-core/src/presentation/home-continuation': {homeContinuationCard: (_id: string, text: unknown) => text, homeContinuationSource: (entry: unknown) => entry},
    '../../../../../packages/client-core/src/presentation/home-card': {homeCardText: () => ({title: 'x', caption: [], detail: []}), homePlayTarget: () => undefined},
    '../../../../../packages/client-core/src/home-selection': {homeEntryCapabilities: () => ({playable: false})},
    '../theme': {useTheme: () => ({isTV: false, platform: 'phone'})},
    '@portico/design': {homeRowsSpec: {continueBooks: {phone: {width: 300, height: 120}}}},
    '../artwork': {ArtworkPlacementContext: React.createContext(null), ArtworkArrivalContext: React.createContext(0), generatedPoster: () => undefined},
    '../../platform/ArtworkPipeline': {ArtworkPlacementContext: React.createContext(null)},
    '../cards': {ReadResumeCard: 'ReadResumeCard', ResumeCard: 'ResumeCard', PosterCard: 'PosterCard', RankedCard: 'RankedCard', WideCard: 'WideCard', SquareCard: 'SquareCard', LiveCard: 'LiveCard', CollectionBanner: 'CollectionBanner'},
  });
  const runtime = {text: {t: (key: string) => key}, playback: undefined, selection: () => ({}), artwork: () => undefined, actions: {navigate() {}, more() {}}};
  return HomeCard({runtime, row: {id: 'continue_books', kind: 'continue', entries: []}, entry: {id: 'b1', title: 'Alice', kind: reading ? 'ebook' : 'audiobook'},
    context: {}, width: 300, rowIndex: 0, visible: true, preferred: false, current: () => true});
}

test('a book or comic in Continue your books has no menu (Home can\'t act on it), so its card offers no ⋯ or hold', async () => {
  const tree = await card(true);
  assert.equal(tree.type, 'ReadResumeCard');
  assert.equal(tree.props.onMore, undefined);
});

test('an audiobook there keeps its menu', async () => {
  assert.equal(typeof (await card(false)).props.onMore, 'function');
});
