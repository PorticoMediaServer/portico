import test from 'node:test';
import assert from 'node:assert/strict';
import {screenModule} from './helpers/screen-harness.mjs';
import {lifecycleHooks} from './helpers/owner-server-settings.mjs';
import {presentError} from '../../../packages/client-core/src/presentation/errors.ts';
import {settingsSpec} from '../../../packages/client-core/src/presentation/settings-spec.ts';
import * as model from '../src-next/screens/settings-live/owner-libraries/library-settings-model.ts';

type Node = {type: unknown; props: Record<string, any>};
const elements = (node: any): Node[] => Array.isArray(node) ? node.flatMap(elements) : node?.props ? [node, ...elements(node.props.children), ...elements(node.props.trailing), ...elements(node.props.footer)] : [];
const find = (node: unknown, id: string) => elements(node).find(element => element.props.testID === id);
const t = (id: string) => id;

async function fixture(tab: string, kind: string, facts: Record<string, unknown>, failure?: Error) {
  const h = lifecycleHooks(), writes: unknown[] = [], lyrics: unknown[] = [];
  const document = {libraryId: 'library', libraryKind: kind, eligibleControls: Object.keys(facts), settings: {lyricsSource: facts['libraries.lyrics']},
    availableProviders: [{id: 'tmdb', mediaKinds: ['movie', 'show']}, {id: 'anilist', mediaKinds: ['show']} ]};
  const module = await screenModule(new URL('../src-next/screens/settings-live/owner-libraries/LibrarySettingsRows.tsx', import.meta.url), {
    react: h.react, 'react-native': {Text: 'Text', View: 'View'}, '../../../../../../packages/client-core/src/presentation/settings-spec': {settingsSpec},
    '../../../../../../packages/client-core/src/presentation/errors': {presentError},
    '../../../../../../packages/client-core/src/presentation/settings-bindings': {
      readLibrarySettingsBinding: (id: string, original: unknown) => {assert.equal(original, document); return id in facts ? {current: facts[id]} : undefined;},
      patchLibrarySettingsBinding: (id: string, value: unknown, original: unknown) => {assert.equal(original, document); if (failure) throw failure; writes.push([id, value]); return {written: id, value};},
    },
    '../../../../../../packages/i18n/src': {defaultI18n: {t}}, '../../../kit/controls/Button': {Button: 'Button'}, '../../../kit/controls/Select': {Select: 'Select'},
    '../../../kit/controls/Switch': {Switch: 'Switch'}, '../../../kit/controls/TextField': {TextField: 'TextField'}, '../../../kit/overlays/BottomSheet': {BottomSheet: 'BottomSheet'},
    '../../../kit/theme': {useTheme: () => ({colors: {record: '#f00'}, type: {caption: {}}})}, './controls': {LibraryGroup: 'LibraryGroup', LibraryRow: 'LibraryRow', SectionTitle: 'SectionTitle'},
    './library-settings-model': model,
  });
  const saves: unknown[] = [];
  return {writes, saves, lyrics, flush: h.flush, dispose: h.dispose, render: () => h.render(() => module.LibrarySettingsRows({tab, document, busy: false, t, save: (value: unknown) => saves.push(value), fetchLyrics: async (current: () => boolean) => {if (current()) lyrics.push(document);}}))};
}

test('movie Collections dispatches its bound setting and remains absent from a TV General page', async () => {
  const movie = await fixture('general', 'movie', {'libraries.collections': false});
  const row = find(movie.render(), 'libraries.collections')!;
  const control = elements(row).find(value => value.type === 'Switch')!; control.props.onCheckedChange(true);
  assert.deepEqual(movie.writes, [['libraries.collections', true]]);
  assert.deepEqual(movie.saves, [{written: 'libraries.collections', value: true}]);
  const tv = await fixture('general', 'tv', {'libraries.collections': false}); assert.equal(find(tv.render(), 'libraries.collections'), undefined);
});

test('Metadata renders every published field and excludes the music-only controls for TV', async () => {
  const facts = {'libraries.titlesFrom': 'tmdb', 'libraries.language': 'en', 'libraries.region': 'US', 'libraries.localTags': 'prefer', 'libraries.titleSource': 'tags',
    'libraries.episodeNumbering': 'aired', 'libraries.extras': true, 'libraries.sortArticles': true, 'libraries.refreshMode': 'replace_unlocked', 'libraries.lyrics': 'files', 'libraries.acoustid': true};
  const f = await fixture('metadata', 'tv', facts), tree = f.render();
  for (const id of Object.keys(facts).filter(id => id !== 'libraries.lyrics' && id !== 'libraries.acoustid')) assert.ok(find(tree, id), id);
  assert.equal(find(tree, 'libraries.lyrics'), undefined); assert.equal(find(tree, 'libraries.acoustid'), undefined);
  find(tree, 'libraries.language')!.props.onValueChange('fr');
  assert.deepEqual(f.writes, [['libraries.language', 'fr']]);
});

test('Custom analysis exposes only published matrix operations, preserving the typed patch boundary', async () => {
  const f = await fixture('scanning', 'movie', {'libraries.scanDepth': 'basic', 'libraries.analysisSteps': {local_metadata: 'run', checksum: 'skip'}, 'libraries.scrub': 'standard', 'libraries.scrubSpacing': 10, 'libraries.scrubWidth': 320, 'libraries.scrubMax': '3', 'libraries.chapterPictures': 'embedded'});
  let tree = f.render(); assert.equal(find(tree, 'libraries.analysisSteps.local_metadata'), undefined); assert.equal(find(tree, 'libraries.scrubWidth'), undefined);
  find(tree, 'libraries.scanDepth')!.props.onValueChange('custom'); tree = f.render();
  assert.ok(find(tree, 'libraries.analysisSteps.local_metadata')); assert.equal(find(tree, 'libraries.analysisSteps.waveform'), undefined);
  find(tree, 'libraries.analysisSteps.checksum')!.props.onValueChange('run'); assert.deepEqual(f.writes.pop(), ['libraries.analysisSteps', {checksum: 'run'}]);
  find(tree, 'libraries.scrub')!.props.onValueChange('custom'); tree = f.render();
  assert.ok(find(tree, 'libraries.scrubWidth')); assert.ok(find(tree, 'libraries.scrubSpacing')); assert.ok(find(tree, 'libraries.scrubMax'));
});


test('library patch failures use the shared error presenter without exposing the raw message', async () => {
  const failure = new Error('internal_fixture_diagnostic');
  const f = await fixture('general', 'movie', {'libraries.collections': false}, failure);
  elements(find(f.render(), 'libraries.collections')).find(node => node.type === 'Switch')!.props.onCheckedChange(true);
  const alert = elements(f.render()).find(node => node.props.accessibilityRole === 'alert');
  assert.ok(alert);
  assert.deepEqual(alert.props.children, [presentError(failure, 'library', {operation: 'save'}).body]);
  assert.doesNotMatch(String(alert.props.children), /internal_fixture_diagnostic/);
  assert.equal(f.saves.length, 0);
});

test('Lyrics renders its child Fetch now only for eligible LRCLIB music and expires the row with its opening', async () => {
  const f = await fixture('metadata', 'music', {'libraries.lyrics': 'lrclib'});
  try {
    const tree = f.render(); f.flush();
    const row = find(tree, 'libraries.fetchLyrics')!; assert.ok(row);
    const action = elements(row).find(value => value.type === 'Button')!;
    action.props.onPress(); assert.equal(f.lyrics.length, 1); assert.equal(f.writes.length, 0);
    f.dispose(); action.props.onPress(); assert.equal(f.lyrics.length, 1);
  } finally {f.dispose();}
  for (const [kind, facts] of [['music', {'libraries.lyrics': 'files'}], ['movie', {'libraries.lyrics': 'lrclib'}], ['music', {}]] as const) {
    const unavailable = await fixture('metadata', kind, facts);
    try {assert.equal(find(unavailable.render(), 'libraries.fetchLyrics'), undefined);} finally {unavailable.dispose();}
  }
});
