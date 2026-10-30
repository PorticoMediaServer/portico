import test from 'node:test';
import assert from 'node:assert/strict';
import {hooks, screenModule} from './helpers/screen-harness.mjs';

async function fixture(noService = false) {
  const ui = hooks(), theme = {colors: {text: '#fff', textSecondary: '#ccc', textTertiary: '#aaa'}};
  const core = await screenModule(new URL('../../../packages/client-core/src/lyrics.ts', import.meta.url), {'../../contracts/src/route-paths.ts': {}, 
    react: {default: {}}, '../../contracts/src/routes.ts': {}, './server-messages.ts': {unreadableServerResponse: 'unreadable'},
    './tolerant.ts': {}, './wire-units.ts': {},
  });
  const source = {id: 'source', version: 'v1', startSeconds: 0, duration: 100, sourceDuration: 100, local: false};
  const document = {format: 'lrc', text: '', embeddedOffsetMs: 0, lines: [{atMs: 0, text: 'one'}, {atMs: 10_000, text: 'two'}]};
  let snapshot: any = {data: {selection: {resource: {id: 'lyrics', document, offsetMs: 0}}, source, resources: []}, error: '', busy: false};
  let publication: any, seekDerivations = 0, snapshots = 0;
  const service = {subscribe: () => () => {}, getSnapshot: () => snapshot, refresh() {}, choose() {}, dispose() {}};
  const react = {...ui.react, useSyncExternalStore: (_subscribe: unknown, get: () => unknown) => {
    const first = get(); assert.equal(get(), first, 'useSyncExternalStore snapshot must be cached'); snapshots++; return first;
  }};
  const {NowPlayingLyrics} = await screenModule(new URL('../src-next/screens/destinations/music/NowPlayingLyrics.tsx', import.meta.url), {
    react, 'react-native': {ScrollView: 'ScrollView', Text: 'Text', View: 'View'},
    '../../../../../../packages/client-core/src/lyrics': {activeLyricLine: core.activeLyricLine,
      lyricSeekPosition: (...args: unknown[]) => {seekDerivations++; return core.lyricSeekPosition(...args);}},
    '../../../../../../packages/i18n/src': {defaultI18n: {t: (key: string) => key}},
    '../../../kit/controls': {Button: 'Button', Focusable: 'Focusable'}, '../../../kit/theme': {useTheme: () => theme},
    '../../../kit/browse/presentation': {browseType: () => ({})},
    '../../../kit/overlays/CommittedPaint': {committedPaintScope: () => ({}), useCommittedPaintPublication: (_owner: unknown, _channel: unknown, _scope: unknown, _semantic: unknown, value: unknown) => {publication = value;}},
  });
  const owner = {}, target = {}, seeks: string[] = [];
  const props: any = {read: () => noService ? undefined : {enabled: true, service}, target, paintOwner: owner,
    paintScope: 'source', positionSeconds: 1, playing: true, onSeek: (at: number) => seeks.push(`original:${at}`)};
  const render = () => ui.render(() => NowPlayingLyrics(props));
  return {props, render, seeks, publication: () => publication, seekDerivations: () => seekDerivations, snapshots: () => snapshots,
    replace: (value: any) => {snapshot = value;}, snapshot: () => snapshot};
}

test('lyrics reuse line/seek projections and paint packet between active-line changes', async () => {
  const f = await fixture(); f.render(); const first = f.publication();
  assert.equal(f.seekDerivations(), 2);
  for (let tick = 11; tick < 90; tick++) {
    f.props.positionSeconds = tick / 10;
    f.props.onSeek = (at: number) => f.seeks.push(`later:${at}`);
    f.render(); assert.equal(f.publication(), first);
  }
  assert.equal(f.seekDerivations(), 2);
  first.actions.seek(1); assert.deepEqual(f.seeks, ['original:10'], 'a stable document lifetime keeps its original command');
  f.props.positionSeconds = 10; f.render();
  assert.notEqual(f.publication(), first);
  assert.equal(f.publication().data.activeIndex, 1);
  assert.deepEqual(f.publication().data.lines.map((line: any) => line.active), [false, true]);
  assert.equal(f.seekDerivations(), 2, 'only highlights change at a line boundary');
});

test('a changed selected document/source rebuilds seek positions once', async () => {
  const f = await fixture(); f.render(); const before = f.publication();
  const previous = f.snapshot();
  f.replace({...previous, data: {...previous.data, source: {...previous.data.source, startSeconds: 5}}});
  f.render();
  assert.equal(f.seekDerivations(), 4);
  assert.notEqual(f.publication(), before);
  assert.equal(f.publication().data.lines[0].seekable, false);
  f.publication().actions.seek(1); assert.deepEqual(f.seeks, ['original:5']);
});

test('a missing or throwing lyrics service retains one frozen empty snapshot', async () => {
  const f = await fixture(true);
  assert.equal(f.render(), null); assert.equal(f.render(), null);
  f.props.read = () => {throw new Error('unavailable');};
  assert.equal(f.render(), null); assert.equal(f.render(), null);
  assert.equal(f.publication(), undefined); assert.equal(f.snapshots(), 4);
});
