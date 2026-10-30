import test from 'node:test';
import {defaultI18n} from '@i18n';
import assert from 'node:assert/strict';
import type {PlayerChapter, PlayerChaptersSnapshot} from '@core/player-chapters.ts';
import {readFileSync} from 'node:fs';
import {registerHooks} from 'node:module';
import {Children, isValidElement, type ReactNode} from 'react';
import {transformTsx} from './transform-tsx.mjs';
const loader = registerHooks({load(url, context, next) {
  const module = (source: string) => ({format: 'module' as const, source, shortCircuit: true});
  if (url.endsWith('.module.css')) return module('export default new Proxy({}, {get:(_,k)=>String(k)});');
  if (url.endsWith('/kit/controls/index.ts')) return module("export const Button='button',Segmented='segmented',Slider='slider',TextField='input';");
  if (url.endsWith('/kit/overlays/index.ts')) return module("export const Banner='banner',Dialog='dialog';");
  if (url.endsWith('/kit/artwork/index.ts')) return module("export const Artwork='artwork',AmbientArt='ambient';");
  if (url.endsWith('/kit/states/index.ts')) return module("export const StateView='state';");
  if (url.endsWith('/kit/Icon.tsx')) return module("export const Icon='icon';");
  if (url.endsWith('.tsx')) return module(transformTsx(url));
  return next(url, context);
}});
const {AudiobookListening} = await import('../src/screens/watch/listening/AudiobookListening.tsx');
const {LyricsPanel} = await import('../src/screens/watch/listening/LyricsPanel.tsx');
const {ListeningQueue} = await import('../src/screens/watch/listening/ListeningQueue.tsx');
const {ListeningQueueWindow} = await import('../src/screens/watch/listening/ListeningQueueWindow.tsx');
const {ListeningSeek} = await import('../src/screens/watch/listening/ListeningSeek.tsx');
function elements(tree: ReactNode): any[] {const result: any[] = [];Children.forEach(tree, child => {if (isValidElement<any>(child)) {result.push(child);result.push(...elements((child as import('react').ReactElement<{children?: ReactNode}>).props.children));}});return result;}
const noop = () => {};
const chapter = (index: number, start: number, end: number): PlayerChapter => ({id: `source:${index}`, index, title: `Chapter ${index}`, startSeconds: start, endSeconds: end, action: {id: 'seek', positionSeconds: start}});
const chapters: PlayerChaptersSnapshot = {generation: 1, phase: 'ready', target: {libraryId: 'library', itemId: 'book', sessionId: 'session', sessionGeneration: 1, sourceId: 'source'}, scope: {serverId: 'server', viewerId: 'viewer'}, pagination: {cursor: null, canPrevious: true}, error: null,
  data: {scope: {serverId: 'server', libraryId: 'library', itemId: 'book', viewerFence: 'viewer', sessionId: 'session', generation: 1, sourceId: 'source'}, reason: null, status: 'available', totalCount: 101, nextCursor: 'opaque-2', revision: 'original', duration: 1010, chapters: [chapter(1, 0, 10), chapter(2, 10, 20)]}};
const playback: any = {disposed: false, dock: {}, presentation: {kind: 'audiobook', expanded: true, playing: true, positionSeconds: 12, durationSeconds: 1010, rate: 1.25, volume: .5, muted: false, controls: ['seek', 'pause', 'volume']}};
const book = (patch: any = {}) => ({snapshot: playback, title: 'A real title', subtitle: 'A real author', chapters, speedChoices: [.75, 1, 1.25], sleepChoices: [0, 15, 30], sleepMinutes: 0, skips: {back: 15, forward: 30}, onIntent: noop, onSpeed: noop, onSleep: noop, onChapter: noop, onChapterPage: noop, onChapterRetry: noop, onCollapse: noop, ...patch});

test('audiobook view uses actual part-local chapters and only offered pages/rates with original witnesses', () => {
  const calls: unknown[] = [], props = book({onChapter: (...args: unknown[]) => calls.push(args), onChapterPage: (...args: unknown[]) => calls.push(args), onSpeed: (...args: unknown[]) => calls.push(args)});
  const tree = elements(AudiobookListening(props));
  tree.find(e => e.type === 'button' && e.props['aria-current']).props.onClick();
  assert.deepEqual(calls.shift(), [chapters, chapters.data!.chapters[1]]);
  const pages = tree.filter(e => e.type === 'button' && e.props.children === 'Show more');pages[0].props.onClick();
  assert.deepEqual(calls.shift(), [chapters, 'next']);
  const speed = tree.find(e => e.type === 'segmented' && e.props['aria-label'] === 'Speed');speed.props.onValueChange('2');assert.equal(calls.length, 0);
  speed.props.onValueChange('1.25');assert.deepEqual(calls.shift(), [playback, 1.25]);
  assert.equal(elements(AudiobookListening(book({snapshot: {...playback, dock: {remote: {phase: 'playing'}}}}))).length, 0);
});

test('chapter unavailable/none/error remain distinct, and paging never invents totals or requests', () => {
  for (const status of ['none', 'unavailable']) {
    const tree = elements(AudiobookListening(book({chapters: {...chapters, data: {...chapters.data, status, totalCount: 0, nextCursor: '', chapters: []}}})));
    assert.equal(tree.filter(e => e.type === 'state').length, 1);assert.equal(tree.some(e => e.props.children === 'Show more'), false);
  }
  let retried: unknown;
  const failed: any = {...chapters, phase: 'error', data: null};
  elements(AudiobookListening(book({chapters: failed, onChapterRetry: (shown: unknown) => {retried = shown;}}))).find(e => e.type === 'banner').props.action.onAction();
  assert.equal(retried, failed);
});

function lyrics(format: 'lrc' | 'text' = 'lrc'): any {
  const document = {format, text: 'First\nSecond', embeddedOffsetMs: 750, lines: [{atMs: format === 'lrc' ? 10000 : null, text: 'First'}, {atMs: format === 'lrc' ? 12000 : null, text: 'Second'}]};
  const resource = {id: 'resource-original', revision: 7, offsetMs: 500, language: 'en', provenance: {label: 'Supplied'}, document};
  return {busy: false, generation: 1, error: '', data: {selection: {resource}, source: {startSeconds: 10, duration: 20}, resources: [resource]}, warnings: [], candidates: [], preview: null};
}
test('synced lyrics highlight accepted source time once and line callbacks retain the original service snapshot', () => {
  const snapshot = lyrics(), calls: unknown[] = [];
  const tree = elements(LyricsPanel({snapshot, positionSeconds: 2.5, enabled: true, canSeek: true, onRetry: noop, onChoose: (...args: unknown[]) => calls.push(args), onSeek: (...args: unknown[]) => calls.push(args)}));
  const active = tree.find(e => e.props['aria-current']);assert.equal(active.props.children, 'Second');active.props.onClick();assert.deepEqual(calls.shift(), [snapshot, 1]);
  tree.find(e => e.type === 'button' && e.props['aria-pressed']).props.onClick();assert.deepEqual(calls.shift(), [snapshot, snapshot.data.resources[0]]);
});
test('plain/out-of-source lyrics never offer seek; disabled preference renders no lyrics or reads', () => {
  const props = {snapshot: lyrics('text'), positionSeconds: 2.5, enabled: true, canSeek: true, onRetry: noop, onChoose: noop, onSeek: noop};
  assert.equal(elements(LyricsPanel(props)).some(e => e.type === 'button' && e.props.className === 'line'), false);
  props.snapshot = lyrics();props.snapshot.data.source.startSeconds = 30;
  assert.equal(elements(LyricsPanel(props)).some(e => e.type === 'button' && e.props.className === 'line'), false);
  assert.equal(LyricsPanel({...props, enabled: false}), null);
});
test('real empty, loading and failed lyrics do not turn one another into a false no-lyrics answer', () => {
  const props: any = {snapshot: {...lyrics(), data: null}, positionSeconds: 0, enabled: true, canSeek: false, onRetry: noop, onChoose: noop, onSeek: noop};
  assert.equal(elements(LyricsPanel(props)).filter(e => e.type === 'state').length, 1);
  assert.equal(elements(LyricsPanel({...props, snapshot: {...props.snapshot, busy: true}})).some(e => e.props.role === 'status'), true);
  const failed = elements(LyricsPanel({...props, snapshot: {...props.snapshot, error: 'private transport error'}}));
  assert.equal(failed.filter(e => e.type === 'state').length, 0);assert.equal(failed.filter(e => e.type === 'banner').length, 1);
});

test('queue renders occurrence identity and forwards only that original witness; no synthetic More or play grant', () => {
  const snapshot: any = {current: true, queueId: 'queue', revision: '7', change: 'idle', more: 151,
    entries: [{entryId: 'one', title: 'Same title', playing: true}, {entryId: 'two', title: 'Same title', hidden: false, pending: false, canRemove: true, canMoveUp: false, canMoveDown: true}, {entryId: 'hidden', hidden: true}]};
  const calls: unknown[] = [], props: any = {snapshot, policy: {repeat: 'off', shuffled: false}, onEdit: (...args: unknown[]) => calls.push(args), onPolicy: (...args: unknown[]) => calls.push(args), onRefresh: noop};
  const tree = elements(ListeningQueue(props));
  const remove = tree.find(e => e.props['aria-label'] === 'Remove from queue');remove.props.onClick();assert.deepEqual(calls.shift(), [snapshot, 'two', 'remove']);
  assert.equal(tree.some(e => e.props.children === 'Show more'), false);
  assert.equal(tree.some(e => String(e.props['aria-label'] ?? '').startsWith('Play ')), false);
  assert.equal(tree.filter(e => e.props.role === 'listitem').length, 3);
  tree.find(e => e.type === 'button' && e.props.children === 'Shuffle off').props.onClick();assert.deepEqual(calls.shift(), [snapshot, {action: 'shuffle', shuffled: true}]);
});
test('phone audiobook uses the authored one-tap panels and no device volume slider', () => {
  const calls: unknown[] = [], tree = elements(AudiobookListening(book({phone: true, onShowPanel: (...args: unknown[]) => calls.push(args)})));
  assert.equal(tree.some(e => e.type === 'slider' || e.type === 'segmented'), false);
  const options = tree.filter(e => e.type === 'button' && typeof e.props.children !== 'string' && e.props.onClick && elements(e.props.children).some(child => child.type === 'b'));
  options[0].props.onClick();assert.deepEqual(calls.shift(), [playback, 'speed']);
  assert.equal(tree.some(e => e.type === 'aside'), false);
});

test('queue save uses canonical progress and the rendered occurrence/revision witness', () => {
  const snapshot: any = {current: true, entries: [], revision: '7', change: 'idle', more: null}, calls: unknown[] = [];
  const save = {open: true, name: '  Original name  ', progress: null, openDialog: noop, close: noop, changeName: noop,
    confirm: (...args: unknown[]) => calls.push(args)};
  const props: any = {snapshot, onEdit: noop, onPolicy: noop, onRefresh: noop, save};
  const dialog = elements(ListeningQueue(props)).find(e => e.type === 'dialog');
  const confirm = elements(dialog.props.footer).find(e => e.props.variant === 'primary');confirm.props.onClick();
  assert.deepEqual(calls.shift(), [snapshot, 'Original name']);
  const progress: any = {state: 'saving', entries: 50, total: 101, playlistId: 'original', revision: '7'};
  const active = elements(ListeningQueue({...props, save: {...save, progress}}));
  assert.equal(active.find(e => e.props.role === 'status').props.children, 'Saving… 50 of 101');
  const activeDialog = active.find(e => e.type === 'dialog');assert.equal(elements(activeDialog.props.footer).find(e => e.props.variant === 'primary').props.disabled, true);
});
test.after(() => loader.deregister());


test('published one-based chapter numbers stay exact on the first and later pages', () => {
  const first = elements(AudiobookListening(book()));
  assert.ok(first.some(e => e.type === 'span' && e.props.children === 1));
  const later: PlayerChaptersSnapshot = {...chapters, data: {...chapters.data!, chapters: [chapter(101, 1000, 1010)]}};
  let received: unknown[] = [];
  const tree = elements(AudiobookListening(book({chapters: later, onChapter: (...args: unknown[]) => {received = args;}})));
  const number = tree.find(e => e.type === 'span' && e.props.children === 101);assert.ok(number);
  const row = tree.find(e => e.type === 'button' && elements(e.props.children).includes(number));row.props.onClick();
  assert.deepEqual(received, [later, later.data!.chapters[0]]);
});

test('desktop/phone transports use local authored geometry and explicit icon sizes', () => {
  for (const phone of [false, true]) {
    const tree = elements(AudiobookListening(book({phone})));
    for (const icon of ['i-prev', 'i-next']) {const button = tree.find(e => e.props.icon === icon);assert.equal(button.props.className, 'control');assert.equal(button.props.iconOnly, true);assert.equal(button.props.iconSize, phone ? 28 : 24);}
    const play = tree.find(e => e.props.className === 'playPause');assert.equal(play.props.iconSize, phone ? 34 : 30);assert.equal(play.props.iconOnly, true);assert.equal(play.props.variant, 'primary');
  }
  const css = readFileSync(new URL('../src/screens/watch/listening/audiobook-listening.module.css', import.meta.url), 'utf8');
  assert.match(css, /\.frame \.transport \.control \{ width: 50px; height: 50px; padding: 0; border-radius: 50%;/);
  assert.match(css, /\.frame \.transport \.playPause \{ width: 74px; height: 74px; padding: 0; border-radius: 50%;/);
});

test('absent canonical lyrics choice capability offers no resource action', () => {
  const snapshot = lyrics(), tree = elements(LyricsPanel({snapshot, positionSeconds: 2.5, enabled: true, canSeek: false, onRetry: noop, onSeek: noop}));
  assert.equal(tree.some(e => e.type === 'button'), false);
  assert.equal(tree.some(e => e.props['aria-pressed'] !== undefined), false);
  assert.ok(tree.some(e => e.props.children === 'Second'));
});


test('queue rows use supplied art/artist/duration while hidden and unavailable rows reveal none', () => {
  const calls: string[] = [];
  const page: any = {start: 0, total: 3, revision: '7', canNext: false, canPrevious: false, rows: [
    {entryId: 'one', itemId: 'one', position: 0, title: 'First', artist: 'Real artist', durationMs: 123000, artworkUrl: '/art/one', available: true, hidden: false, current: true},
    {entryId: 'hidden', itemId: 'hidden', position: 1, title: 'Hidden title', artist: 'Hidden artist', durationMs: 456000, artworkUrl: '/art/hidden', available: true, hidden: true, current: false},
    {entryId: 'absent', itemId: 'absent', position: 2, title: 'Unavailable title', artist: 'Unavailable artist', durationMs: 789000, artworkUrl: '/art/absent', available: false, hidden: false, current: false},
  ]};
  const tree = elements(ListeningQueueWindow({snapshot: {phase: 'ready', page, busy: false}, retained: null, anchor: 0, error: false, busy: false,
    artwork: {foreground: (url: string) => {calls.push(url);return undefined;}} as any, onRetry: noop, onEdit: noop}));
  assert.deepEqual(calls, ['/art/one']);
  const text = tree.flatMap(element => Children.toArray(element.props.children).filter(child => typeof child === 'string'));
  assert.ok(text.includes('Real artist'));assert.ok(text.includes('2:03'));
  for (const privateText of ['Hidden title', 'Hidden artist', 'Unavailable title', 'Unavailable artist']) assert.equal(text.includes(privateText), false);
});
test('listening seek renders native range semantics over the drawn rail and puts remaining labels below on phone', () => {
  const sought: number[] = [];
  const tree = elements(ListeningSeek({position: 60, duration: 180, bounds: {start: 0, end: 180}, phone: true,
    chapters: [{id: 'one', startSeconds: 0, endSeconds: 90}, {id: 'two', startSeconds: 90, endSeconds: 180}], onSeek: value => sought.push(value)}));
  assert.equal(tree[0].props['data-phone'], true);
  const input = tree.find(element => element.type === 'input');assert.equal(input.props['aria-label'], defaultI18n.t('player.position'));
  input.props.onChange({currentTarget: {valueAsNumber: 120}});assert.deepEqual(sought, [120]);
  assert.ok(tree.some(element => element.props.children === '−2:00'));
  assert.equal(tree.filter(element => element.props.className === 'tick').length, 1);
});


test('Up next footer displays supplied canonical count and total duration separately', () => {
  const snapshot: any = {current: true, queueId: 'queue', revision: '7', change: 'idle', more: null, entries: []};
  const tree = elements(ListeningQueue({snapshot, onPolicy: noop, onRefresh: noop, summary: {count: 42, pivot: 'songs', durationSeconds: 4000}}));
  assert.ok(tree.some(element => element.props.children === defaultI18n.t('lib.count', {count: 42, pivot: 'songs'})));
  assert.ok(tree.some(element => element.props.children === '1:06:40'));
  const unknown = elements(ListeningQueue({snapshot, onPolicy: noop, onRefresh: noop, summary: {count: 42, pivot: 'songs'}}));
  assert.equal(unknown.some(element => element.props.children === '0:00'), false);
});
