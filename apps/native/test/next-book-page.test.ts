import {audiobookAuthorInitials} from '../src-next/screens/destinations/audiobooks/presentation.ts';
import test from 'node:test';
import assert from 'node:assert/strict';
import {hooks, screenModule} from './helpers/screen-harness.mjs';
import {expandWindows, musicWindowStub} from './helpers/music-window-stub.ts';
import {LibraryContentService} from '../../../packages/client-core/src/library-content';

const entity = {id: 'book-1', libraryId: 'books', kind: 'book', title: 'The Martian', author: 'Andy Weir', narrators: ['R. C. Bray'],
  duration: 39200, count: 17, overview: 'A stranded astronaut.', bookProgress: {bookId: 'book-1', itemId: 'file-1', partPositionMs: 530000,
    positionMs: 530000, durationMs: 1000000, remainingMs: 470000, chapter: {index: 6, number: 7, title: 'Chapter 6', startMs: 500000, endMs: 560000}}};
const chapters = [
  {id: 'file-1:1', libraryId: 'books', kind: 'chapter', title: 'Chapter 1', duration: 100, playback: {itemId: 'file-1', startSeconds: 0}},
  {id: 'file-1:6', libraryId: 'books', kind: 'chapter', title: 'Chapter 6', duration: 100, playback: {itemId: 'file-1', startSeconds: 500}},
];
const lease = {signal: new AbortController().signal, isCurrent: () => true};
const listening = {target: {kind: 'book', id: 'book-1', libraryId: 'books'}, actions: ['play', 'enqueue'], resume: {itemId: 'file-1', positionSeconds: 530}, resumeUnavailable: false};

class FakeLibraryContentService {
  static page: any;
  static nextCalls: string[] = [];
  next = async (sectionId: string) => {FakeLibraryContentService.nextCalls.push(sectionId);};
  select = async (_route: any) => {};
  subscribe = () => () => {};
  getSnapshot = () => ({...FakeLibraryContentService.page, appended: FakeLibraryContentService.page.appended ?? FakeLibraryContentService.page.sections, pagination: FakeLibraryContentService.page.pagination ?? {cursor: null}});
  dispose = () => {};
}

async function controller(overrides: any = {}) {
  const starts: any[] = [], writes: any[] = [];
  FakeLibraryContentService.page = overrides.page ?? {phase: 'ready', error: null, sections: [{id: 'chapters', entries: chapters}, {id: 'files', entries: []}], appended: [{id: 'chapters', entries: chapters}, {id: 'files', entries: []}], pagination: {cursor: null},
    projection: {entity, sections: [{id: 'chapters', entries: chapters}], listening}};
  const {NativeAudiobookController} = await screenModule(new URL('../src-next/screens/destinations/audiobooks/model.ts', import.meta.url), {
    react: {default: {}},
    '../../../shell/bootstrap': {default: {}},
    '../lifetime': {default: {}, nativeDestinationLifetime: () => overrides.lifetime ?? ({scope: {serverId: 'server', viewerId: 'viewer'}, current: () => true})},
    '../../../../../../packages/client-core/src/library-content': {default: {}, LibraryContentService: FakeLibraryContentService},
    '../../../../../../packages/client-core/src/container-state': {default: {},
      getContainerState: overrides.getContainerState ?? (async () => ({revision: 4, watched: false})),
      setContainerState: async (_api: any, _kind: any, _id: any, input: any) => {writes.push(input); return {revision: 5, watched: input.watched};}},
  });
  const context = {api: {}, playback: {start: async (input: any) => {starts.push(input);}}};
  const value = new NativeAudiobookController({context, kind: overrides.kind ?? 'book', libraryId: overrides.libraryId ?? 'books', id: overrides.id ?? 'book-1'});
  return {value, starts, writes};
}

test('the book page reads the real catalog entity and chapters', async () => {
  const {value} = await controller();
  await value.load(lease);
  const state = value.getSnapshot();
  assert.equal(state.phase, 'ready');
  assert.equal(state.entity?.title, 'The Martian');
  assert.equal(state.entity?.narrators?.[0], 'R. C. Bray');
  assert.equal(state.sections.find((section: any) => section.id === 'chapters')?.entries.length, 2);
  assert.equal(state.listening?.resume?.positionSeconds, 530);
  value.dispose();
});

test('resume starts the chapter at the real server position', async () => {
  const {value, starts} = await controller();
  await value.load(lease);
  await value.resume(lease);
  assert.equal(starts.length, 1);
  assert.equal(starts[0].entry.id, 'book-1');
  assert.deepEqual(starts[0].target, {itemId: 'file-1', startSeconds: 530});
  value.dispose();
});

test('start over starts the first real chapter at zero', async () => {
  const {value, starts} = await controller();
  await value.load(lease);
  await value.startOver(lease);
  assert.equal(starts[0].entry.id, 'file-1:1');
  assert.deepEqual(starts[0].target, {itemId: 'file-1', startSeconds: 0});
  value.dispose();
});

test('an author page uses the canonical book rows and resumes the book with progress', async () => {
  const authorEntity = {id: 'author-1', libraryId: 'books', kind: 'author', title: 'Andy Weir', count: 1, duration: 5400, narrators: ['R. C. Bray']};
  const book = {...entity, bookProgress: {bookId: 'book-1', itemId: 'file-1', partPositionMs: 500000, positionMs: 500000, durationMs: 5400000, remainingMs: 4900000}};
  const page = {phase: 'ready', error: null, sections: [{id: 'books', entries: [book], totalCount: 1, nextCursor: '', start: 0}],
    projection: {entity: authorEntity, sections: [{id: 'books', entries: [book], totalCount: 1, nextCursor: '', start: 0}]}};
  const {value, starts} = await controller({kind: 'author', id: 'author-1', page});
  await value.load(lease);
  assert.equal(value.getSnapshot().entity?.kind, 'author');
  assert.equal(value.getSnapshot().entity?.count, 1);
  assert.equal(value.contains(book), true);
  assert.equal(value.contains({...book}), false);
  await value.resumeBook(lease, book);
  assert.deepEqual(starts[0].target, {itemId: 'file-1', startSeconds: 500});
  value.dispose();
});


test('retired opening cannot dispatch and disposal aborts an in-flight finished mutation', async () => {
  let resolveRead!: (value: any) => void;
  let mutationSignal: AbortSignal | undefined;
  let reads = 0;
  const {value, starts, writes} = await controller({getContainerState: async (_api: any, _kind: any, _id: any, signal: AbortSignal) => {
    if (++reads === 1) return {revision: 4, watched: false};
    mutationSignal = signal;
    return new Promise(resolve => {resolveRead = resolve;});
  }});
  await value.load(lease);
  const abort = new AbortController();
  const opening = {signal: abort.signal, isCurrent: () => !abort.signal.aborted};
  const pending = value.markFinished(opening);
  abort.abort();
  assert.equal(mutationSignal?.aborted, true);
  resolveRead({revision: 4, watched: false});
  await pending;
  await value.playChapter(opening, chapters[0]);
  assert.equal(writes.length, 0);
  assert.equal(starts.length, 0);
  value.dispose();
});

test('renderer commands retain their original navigation occurrence across cache reuse', async () => {
  const starts: any[] = [];
  const callbacks = new Set<() => void>();
  const routeA = {kind: 'book', entry: entity};
  let snapshot: any = {route: routeA, revision: 1};
  let navProbe: (() => boolean) | undefined;
  FakeLibraryContentService.page = {phase: 'ready', error: null, sections: [{id: 'chapters', entries: chapters}],
    projection: {entity, sections: [{id: 'chapters', entries: chapters}], listening}};
  const lifetime = {scope: {serverId: 'server', viewerId: 'viewer'}, current: () => true};
  const {NativeAudiobookController} = await screenModule(new URL('../src-next/screens/destinations/audiobooks/model.ts', import.meta.url), {
    react: {default: {}}, '../lifetime': {nativeDestinationLifetime: () => lifetime},
    '../../../../../../packages/client-core/src/library-content': {LibraryContentService: FakeLibraryContentService},
    '../../../../../../packages/client-core/src/container-state': {getContainerState: async () => ({revision: 4, watched: false}), setContainerState: async () => ({watched: true})},
  });
  const react = {createElement: (type: any, props: any) => ({type, props})};
  const {createNativeBookRenderer} = await screenModule(new URL('../src-next/screens/destinations/audiobooks/register.tsx', import.meta.url), {
    react: {default: react}, '../../../shell/route-registry': {nativeShellRoutes: {registerRoute() {}}},
    '../lifetime': {nativeDestinationLifetime: () => lifetime}, './model': {NativeAudiobookController},
    '../entry-actions': {createNativeEntryActions: () => ({menu() {}, dispose() {}})},
    './BookScreen': {BookScreen: 'book'}, './AuthorSeriesScreen': {AuthorSeriesScreen: 'author'},
  });
  const context = {api: {}, authority: () => ({}), playback: {start: async (input: any) => {starts.push(input);}},
    subscribeAuthority: (fn: () => void) => {callbacks.add(fn); return () => callbacks.delete(fn);},
    navigation: {getSnapshot: () => snapshot, isCurrent: () => navProbe ? navProbe() : true,
      subscribe: (fn: () => void) => {callbacks.add(fn); return () => callbacks.delete(fn);}}};
  const renderer = createNativeBookRenderer(context);
  const first = renderer.render(routeA, {}).props.controller;
  await Promise.resolve();
  await first.playChapter(chapters[0]);
  snapshot = {route: {kind: 'home'}, revision: 2}; callbacks.forEach(fn => fn());
  await first.playChapter(chapters[0]);
  snapshot = {route: routeA, revision: 3}; callbacks.forEach(fn => fn());
  const second = renderer.render(routeA, {}).props.controller;
  await first.playChapter(chapters[0]);
  await second.playChapter(chapters[0]);
  assert.equal(starts.length, 2);
  assert.equal(starts[0].lifetime.signal.aborted, true);
  const routeB = {kind: 'book', entry: entity};
  let successor: any;
  starts[1].lifetime.signal.addEventListener('abort', () => {successor = renderer.render(routeB, {}).props.controller;}, {once: true});
  snapshot = {route: routeB, revision: 4}; callbacks.forEach(fn => fn());
  assert.ok(successor);
  await successor.playChapter(chapters[0]);
  assert.equal(starts.length, 3, 'the successor installed during abort remains live');
  const routeC = {kind: 'book', entry: entity};
  let newest: any;
  navProbe = () => {navProbe = undefined; snapshot = {route: routeC, revision: 5}; newest = renderer.render(routeC, {}).props.controller; return false;};
  callbacks.forEach(fn => fn());
  await newest.playChapter(chapters[0]);
  assert.equal(starts.length, 4, 'the successor installed by a navigation getter remains live');
  renderer.dispose();
  assert.equal(starts[1].lifetime.signal.aborted, true);
  assert.equal(starts[2].lifetime.signal.aborted, true);
  assert.equal(starts[3].lifetime.signal.aborted, true);
});

test('disposing a controller aborts a pending finished mutation before its CAS write', async () => {
  let resolveRead!: (value: any) => void;
  let mutationSignal: AbortSignal | undefined;
  let reads = 0;
  const {value, writes} = await controller({getContainerState: async (_api: any, _kind: any, _id: any, signal: AbortSignal) => {
    if (++reads === 1) return {revision: 4, watched: false};
    mutationSignal = signal;
    return new Promise(resolve => {resolveRead = resolve;});
  }});
  await value.load(lease);
  const pending = value.markFinished(lease);
  value.dispose();
  assert.equal(mutationSignal?.aborted, true);
  resolveRead({revision: 4, watched: false});
  await pending;
  assert.equal(writes.length, 0);
});


test('book resume uses the offered part outside the loaded window without paging', async () => {
  const page = {phase: 'ready', error: null, sections: [{id: 'files', entries: chapters}],
    projection: {entity, sections: [{id: 'files', entries: chapters}], listening: {...listening, resume: {itemId: 'file-41', positionSeconds: 120}}}};
  const {value, starts} = await controller({page});
  await value.load(lease);
  await value.resume(lease);
  assert.equal(starts.length, 1);
  assert.deepEqual(starts[0].target, {itemId: 'file-41', startSeconds: 120});
  assert.equal(starts[0].entry.id, entity.id);
  value.dispose();
});

test('book resume refuses unavailable and invalid original offers', async () => {
  for (const journey of [ {...listening, resumeUnavailable: true}, {...listening, actions: []},
    {...listening, resume: undefined}, {...listening, resume: {itemId: 'file-41', positionSeconds: -1}},
    {...listening, target: {...listening.target, libraryId: 'other'}} ]) {
    const page = {phase: 'ready', error: null, sections: [], projection: {entity, sections: [], listening: journey}};
    const {value, starts} = await controller({page}); await value.load(lease); await value.resume(lease);
    assert.equal(starts.length, 0); value.dispose();
  }
});

test('author resume uses the offered part-local position, never the whole-book position', async () => {
  for (const partPositionMs of [120000, 0]) {
    const book = {...entity, bookProgress: {...entity.bookProgress, itemId: 'file-41', positionMs: 5520000, partPositionMs}};
    const page = {phase: 'ready', error: null, sections: [{id: 'books', entries: [book]}],
      projection: {entity: {id: 'author-1', kind: 'author', libraryId: 'books'}, sections: [{id: 'books', entries: [book]}]}};
    const {value, starts} = await controller({kind: 'author', id: 'author-1', page}); await value.load(lease);
    await value.resumeBook(lease, book);
    assert.deepEqual(starts[0].target, {itemId: 'file-41', startSeconds: partPositionMs / 1000});
    await value.resumeBook(lease, {...book});
    assert.equal(starts.length, 1);
    value.dispose();
  }
});


test('authority retirement keeps pending playback tied to the original viewer', async () => {
  let live = true;
  const {value, starts} = await controller({lifetime: {scope: {serverId: 'server', viewerId: 'viewer'}, current: () => live}});
  await value.load(lease);
  await value.resume(lease);
  assert.equal(starts.length, 1);
  assert.equal(starts[0].lifetime.isCurrent(), true);
  live = false;
  assert.equal(starts[0].lifetime.isCurrent(), false);
  await value.resume(lease);
  assert.equal(starts.length, 1);
  value.dispose();
});


test('author and series show a retryable error and retain Back, while ready-empty is not an error', async () => {
  const ui = hooks();
  const ErrorView = () => null;
  let tv = false;
  const theme = {colors: {ink: '#000', text: '#fff', textSecondary: '#aaa', textTertiary: '#aaa',
    raised: '#111', line: '#222', accentStrong: '#fff'}, isTV: false};
  const {AuthorSeriesScreen} = await screenModule(new URL('../src-next/screens/destinations/audiobooks/AuthorSeriesScreen.tsx', import.meta.url), {
    react: ui.react, 'react-native': {ScrollView: 'ScrollView', Text: 'Text', View: 'View'},
    'react-native-safe-area-context': {useSafeAreaInsets: () => ({top: 0})},
    '../../../../../../packages/i18n/src': {defaultI18n: {t: (key: string) => key, duration: String, list: (v: string[]) => v.join(', ')}},
    '../../../kit/artwork': {useWholeBox: (_source: unknown, width: number, height: number) => ({box: {width, height}, onSize: undefined}), missingArt: () => undefined, StandardBackdrop: 'StandardBackdrop', AmbientArt: 'AmbientArt', Artwork: 'Artwork', PersonCircle: 'PersonCircle'},
    '../../../kit/controls': {Button: 'Button', Focusable: 'Focusable'}, '../../../kit/focus': {FocusRegion: 'FocusRegion'},
    '../../../kit/browse/presentation': {alpha: (value: string) => value, browseType: () => ({})},
    '../../../kit/theme': {useTheme: () => ({...theme, isTV: tv})}, './BookScreen': {AudiobookErrorState: ErrorView}, './presentation': {audiobookAuthorInitials},
    '../music/MusicWindow': musicWindowStub,
  });
  const find = (node: any, predicate: (value: any) => boolean): any => {
    if (!node || typeof node !== 'object') return undefined;
    if (predicate(node)) return node;
    for (const child of [node.props?.children].flat(Infinity)) {const result = find(child, predicate); if (result) return result;}
    return undefined;
  };
  for (const kind of ['author', 'book_series']) for (tv of [false, true]) {
    const pageEntity = {id: 'person-1', libraryId: 'books', kind, title: 'Person'};
    const failed = {phase: 'error', error: {code: 'request_failed', retryable: true}, sections: [], projection: null};
    const {value} = await controller({kind, id: 'person-1', page: failed});
    await value.load(lease);
    let back = 0;
    const render = () => expandWindows(ui.render(() => AuthorSeriesScreen({controller: {...value, getSnapshot: value.getSnapshot, subscribe: value.subscribe, load: () => value.load(lease), next: (id: string) => value.next(id, lease)}, entry: pageEntity,
      runtime: {artwork: () => undefined, ambient: () => undefined}, actions: {menu() {}}, openEntry() {}, onBack: () => {back++;}})));
    const errorTree = render();
    const error = find(errorTree, node => node.type === ErrorView);
    assert.ok(error);
    if (!tv) {const bar = find(errorTree, node => node.type === 'TopBar'); assert.equal(bar?.props.back.label, 'action.back'); bar.props.back.onPress(); assert.equal(back, 1);}
    FakeLibraryContentService.page = {phase: 'ready', error: null, sections: [{id: 'books', entries: []}],
      projection: {entity: pageEntity, sections: [{id: 'books', entries: []}]}};
    error.props.retry();
    await Promise.resolve(); await Promise.resolve();
    assert.equal(value.getSnapshot().phase, 'ready');
    assert.equal(find(render(), node => node.type === ErrorView), undefined);
    const books = {id: 'books', entries: [entity], totalCount: 81, nextCursor: 'next'};
    FakeLibraryContentService.page = {phase: 'ready', error: null, sections: [books], projection: {entity: pageEntity, sections: [books]}};
    await value.load(lease); FakeLibraryContentService.nextCalls = [];
    const readyTree = render();
    if (tv) {
      const row = find(readyTree, node => typeof node.type === 'function' && node.props?.entry === entity);
      assert.ok(row);
      const focusable = find(row.type(row.props), node => node.type === 'Focusable');
      focusable.props.onFocus();
    } else {
      // The viewport list's own near-end signal asks for the next page.
      const scroll = find(readyTree, node => node.type === 'MusicScrollView');
      scroll.props.onNearEnd();
    }
    await Promise.resolve(); await Promise.resolve();
    assert.deepEqual(FakeLibraryContentService.nextCalls, ['books']);
    value.dispose();
  }
});

test('resume dispatch is fenced to the exact ready projection and opening', async () => {
  const {value, starts} = await controller();
  await value.load(lease);
  const abort = new AbortController();
  const opening = {signal: abort.signal, isCurrent: () => !abort.signal.aborted};
  await value.resume(opening);
  assert.equal(starts.length, 1);
  const original = FakeLibraryContentService.page;
  FakeLibraryContentService.page = {...original, projection: {...original.projection, entity: {...entity}, listening: {...listening}}};
  await value.load(lease);
  assert.equal(starts[0].lifetime.isCurrent(), false);
  abort.abort();
  await value.resume(opening);
  assert.equal(starts.length, 1);
  value.dispose();
});

test('book progress requires the actual offered row and finite part-local time', async () => {
  const book = {...entity, bookProgress: {...entity.bookProgress, positionMs: undefined, partPositionMs: 0}};
  const page = {phase: 'ready', error: null, sections: [{id: 'books', entries: [book]}],
    projection: {entity: {id: 'series-1', kind: 'book_series', libraryId: 'books'}, sections: [{id: 'books', entries: [book]}]}};
  const {value, starts} = await controller({kind: 'book_series', id: 'series-1', page});
  await value.load(lease);
  await value.resumeBook(lease, book);
  assert.deepEqual(starts[0].target, {itemId: 'file-1', startSeconds: 0});
  await value.resumeBook(lease, {...book, bookProgress: {...book.bookProgress, partPositionMs: Number.NaN}});
  await value.resumeBook(lease, {...book, bookProgress: {...book.bookProgress, bookId: 'other'}});
  assert.equal(starts.length, 1);
  value.dispose();
});

test('continuation coalesces a section and retains first-page entity and listening facts', async () => {
  let resolveNext!: () => void;
  let calls = 0;
  const rows = Array.from({length: 40}, (_, i) => ({id: `file-${i}`, kind: 'audiobook_file', libraryId: 'books', title: `Part ${i}`}));
  const section = {id: 'files', type: 'list', heading: {label: 'Files'}, entries: rows, totalCount: 41, nextCursor: 'cursor'};
  let state: any = {phase: 'ready', error: null, sections: [section], appended: [section], pagination: {cursor: null},
    projection: {entity, listening, sections: [section]}};
  class Service {
    listener = () => {};
    subscribe = (fn: () => void) => {this.listener = fn; return () => {};};
    getSnapshot = () => state;
    select = async () => {};
    next = async (_id: string, context: any) => {
      calls++;
      await new Promise<void>(resolve => {resolveNext = resolve;});
      if (context.signal.aborted || !context.isCurrent()) return;
      const appended = {...section, entries: [...rows, {id: 'file-40', kind: 'audiobook_file', title: 'Part 40'}], nextCursor: ''};
      state = {...state, sections: [{...section, entries: [appended.entries[40]], nextCursor: ''}], appended: [appended],
        pagination: {cursor: 'cursor'}, projection: {sections: []}};
      this.listener();
    };
    dispose = () => {};
  }
  const {NativeAudiobookController} = await screenModule(new URL('../src-next/screens/destinations/audiobooks/model.ts', import.meta.url), {
    react: {default: {}}, '../lifetime': {nativeDestinationLifetime: () => ({scope: {serverId: 'server', viewerId: 'viewer'}, current: () => true})},
    '../../../../../../packages/client-core/src/library-content': {LibraryContentService: Service},
    '../../../../../../packages/client-core/src/container-state': {getContainerState: async () => ({revision: 4, watched: false})},
  });
  const value = new NativeAudiobookController({context: {api: {}, playback: {}}, kind: 'book', libraryId: 'books', id: 'book-1'});
  await value.load(lease);
  const first = value.next('files', lease), duplicate = value.next('files', lease);
  assert.equal(first, duplicate);
  assert.equal(calls, 1);
  resolveNext(); await first;
  assert.equal(value.getSnapshot().sections[0].entries.length, 41);
  assert.equal(value.getSnapshot().entity.id, 'book-1');
  assert.equal(value.getSnapshot().listening.resume.itemId, 'file-1');
  await value.next('files', lease);
  assert.equal(calls, 1);
  value.dispose();
});


/** The rebuilt server's answers for an audiobook library: a book's detail (its chapters or parts, the
 * listener's place) and a series' volumes a page at a time. */
const rebuiltLibrary = {id: 'books', name: 'Audiobooks', kind: 'audiobooks'};
// A book of several files as a real scan sends it: each part a whole file, with no place in the book.
const rebuiltBook = (id: string, parts: number, positionMs = 0) => ({id, kind: 'audiobook', title: id, libraryId: 'books', images: {}, addedAt: '2026-11-12T00:00:00Z',
  durationMs: parts * 60_000, state: positionMs ? {positionMs} : {}, versions: [{id: 'v1', container: 'mp3', durationMs: parts * 60_000,
    parts: Array.from({length: parts}, () => ({size: 1000, streams: [{index: 0, kind: 'audio', codec: 'mp3', channels: 2, sampleRate: 44100}]}))}]});
const rebuiltSeries = {id: 'series-1', kind: 'series', title: 'Series', libraryId: 'books', images: {}, addedAt: '2026-11-12T00:00:00Z', state: {}};
const volume = (id: string) => ({id, kind: 'audiobook', title: id, libraryId: 'books', images: {}, addedAt: '2026-11-12T00:00:00Z', state: {}});

test('a book of eighty-one parts lists them all from its detail, offers no Resume, and Start over plays it from the beginning', async () => {
  const starts: any[] = [], requests: string[] = [];
  const api = {request: async (path: string) => {
    requests.push(path);
    return new URL(path, 'https://server.test').pathname === '/v1/items/book-1' ? rebuiltBook('book-1', 81, 2_520_000) : rebuiltLibrary;
  }};
  const {NativeAudiobookController} = await screenModule(new URL('../src-next/screens/destinations/audiobooks/model.ts', import.meta.url), {
    react: {default: {}}, '../lifetime': {nativeDestinationLifetime: () => ({scope: {serverId: 'server', viewerId: 'viewer'}, current: () => true})},
    '../../../../../../packages/client-core/src/library-content': {LibraryContentService},
    '../../../../../../packages/client-core/src/container-state': {getContainerState: async () => ({revision: 4, watched: false})},
  });
  const value = new NativeAudiobookController({context: {api, playback: {start: async (input: any) => {starts.push(input);}}}, kind: 'book', libraryId: 'books', id: 'book-1'});
  await value.load(lease);
  assert.equal(value.getSnapshot().sections[0].id, 'files');
  assert.equal(value.getSnapshot().sections[0].entries.length, 81, 'the detail lists every part; nothing pages');
  // Stopgap: the rebuilt server plays only a several-file book's first file, so a place past it is not resumed.
  await value.resume(lease);
  assert.equal(starts.length, 0);
  await value.startOver(lease);
  assert.deepEqual(starts[0].target, {itemId: 'book-1', startSeconds: 0});
  await value.next('files', lease);
  assert.equal(requests.filter(path => path.startsWith('/v1/items/')).length, 1);
  assert.equal(value.getSnapshot().entity.id, 'book-1');
  value.dispose();
});

test('queued section reads serialize and old opening cannot dispatch queued successors', async () => {
  const calls: string[] = [];
  let resolveNext!: () => void;
  const sections = ['chapters', 'files', 'books'].map(id => ({id, type: 'list', heading: {key: id}, entries: [], totalCount: 81, nextCursor: `next-${id}`}));
  let snapshot: any = {phase: 'ready', error: null, sections, appended: sections, pagination: {cursor: null}, projection: {entity, listening, sections}};
  class Service {
    subscribe = () => () => {};
    getSnapshot = () => snapshot;
    select = async () => {};
    next = async (sectionId: string) => {calls.push(sectionId); await new Promise<void>(resolve => {resolveNext = resolve;});};
    dispose = () => {};
  }
  const {NativeAudiobookController} = await screenModule(new URL('../src-next/screens/destinations/audiobooks/model.ts', import.meta.url), {
    react: {default: {}}, '../lifetime': {nativeDestinationLifetime: () => ({scope: {serverId: 'server', viewerId: 'viewer'}, current: () => true})},
    '../../../../../../packages/client-core/src/library-content': {LibraryContentService: Service},
    '../../../../../../packages/client-core/src/container-state': {getContainerState: async () => ({revision: 4, watched: false})},
  });
  const value = new NativeAudiobookController({context: {api: {}, playback: {}}, kind: 'book', libraryId: 'books', id: 'book-1'});
  const abort = new AbortController(), opening = {signal: abort.signal, isCurrent: () => !abort.signal.aborted};
  await value.load(opening);
  const first = value.next('chapters', opening), second = value.next('files', opening), third = value.next('books', opening);
  assert.deepEqual(calls, ['chapters']);
  abort.abort(); resolveNext();
  await Promise.all([first, second, third]);
  assert.deepEqual(calls, ['chapters']);
  value.dispose();
});

test('continuation failure keeps offered rows and explicit recovery retries the failed read', async () => {
  const section = {id: 'files', type: 'list', heading: {key: 'files'}, entries: chapters, totalCount: 41, nextCursor: 'next'};
  let snapshot: any = {phase: 'ready', error: null, sections: [section], appended: [section], pagination: {cursor: null}, projection: {entity, listening, sections: [section]}};
  let retryCalls = 0, nextCalls = 0;
  class Service {
    listener = () => {};
    subscribe = (fn: () => void) => {this.listener = fn; return () => {};};
    getSnapshot = () => snapshot;
    select = async () => {};
    next = async () => {nextCalls++; snapshot = {...snapshot, phase: 'error', error: {code: 'timeout', retryable: true}}; this.listener();};
    retry = async () => {retryCalls++; snapshot = {...snapshot, phase: 'ready', error: null}; this.listener();};
    dispose = () => {};
  }
  const {NativeAudiobookController} = await screenModule(new URL('../src-next/screens/destinations/audiobooks/model.ts', import.meta.url), {
    react: {default: {}}, '../lifetime': {nativeDestinationLifetime: () => ({scope: {serverId: 'server', viewerId: 'viewer'}, current: () => true})},
    '../../../../../../packages/client-core/src/library-content': {LibraryContentService: Service},
    '../../../../../../packages/client-core/src/container-state': {getContainerState: async () => ({revision: 4, watched: false})},
  });
  const value = new NativeAudiobookController({context: {api: {}, playback: {}}, kind: 'book', libraryId: 'books', id: 'book-1'});
  await value.load(lease); await value.next('files', lease);
  assert.equal(value.getSnapshot().phase, 'ready');
  assert.equal(value.getSnapshot().sections[0].entries.length, 2);
  assert.equal(value.getSnapshot().continuation.phase, 'error');
  await value.next('files', lease);
  assert.equal(nextCalls, 1);
  await value.recover(lease);
  assert.equal(retryCalls, 1);
  assert.equal(value.getSnapshot().continuation, undefined);
  value.dispose();
});


test('a chapter command cannot adopt a replacement with the same row id', async () => {
  const {value, starts} = await controller(); await value.load(lease);
  const old = chapters[0];
  await value.playChapter(lease, old);
  const replacement = {...old, playback: {itemId: 'replacement', startSeconds: 12}};
  FakeLibraryContentService.page = {phase: 'ready', error: null, sections: [{id: 'chapters', entries: [replacement]}],
    projection: {entity, listening, sections: [{id: 'chapters', entries: [replacement]}]}};
  await value.load(lease);
  await value.playChapter(lease, old);
  assert.equal(starts.length, 1);
  await value.playChapter(lease, replacement);
  assert.equal(starts.length, 2);
  assert.equal(starts[1].target.itemId, 'replacement');
  value.dispose();
});

test('a currentness-retired queued read does not strand a valid successor', async () => {
  const calls: string[] = [], resolvers: (() => void)[] = [];
  const sections = ['chapters', 'files', 'books'].map(id => ({id, type: 'list', heading: {key: id}, entries: [], totalCount: 81, nextCursor: `next-${id}`}));
  const snapshot = {phase: 'ready', error: null, sections, appended: sections, pagination: {cursor: null}, projection: {entity, listening, sections}};
  class Service {
    subscribe = () => () => {}; getSnapshot = () => snapshot; select = async () => {};
    next = async (id: string) => {calls.push(id); await new Promise<void>(resolve => resolvers.push(resolve));};
    dispose = () => {};
  }
  const {NativeAudiobookController} = await screenModule(new URL('../src-next/screens/destinations/audiobooks/model.ts', import.meta.url), {
    react: {default: {}}, '../lifetime': {nativeDestinationLifetime: () => ({scope: {serverId: 'server', viewerId: 'viewer'}, current: () => true})},
    '../../../../../../packages/client-core/src/library-content': {LibraryContentService: Service},
    '../../../../../../packages/client-core/src/container-state': {getContainerState: async () => ({revision: 4, watched: false})},
  });
  const value = new NativeAudiobookController({context: {api: {}, playback: {}}, kind: 'book', libraryId: 'books', id: 'book-1'});
  await value.load(lease);
  let current = true;
  const old = {signal: new AbortController().signal, isCurrent: () => current};
  const successor = {signal: new AbortController().signal, isCurrent: () => true};
  const first = value.next('chapters', old), skipped = value.next('files', old);
  current = false;
  const last = value.next('books', successor);
  resolvers[0](); await first;
  assert.deepEqual(calls, ['chapters', 'books']);
  resolvers[1](); await Promise.all([skipped, last]);
  value.dispose();
});

test('a scoped loading publication restores the cached book when its opening is retired before dispatch', async () => {
  const routeA = {kind: 'book', entry: entity}, routeB = {kind: 'book', entry: {...entity, id: 'book-2'}};
  let snapshot: any = {route: routeA, revision: 1}, renderer: any, successor: any, reentered = false;
  const callbacks = new Set<() => void>(), instances: any[] = [], requests: string[] = [];
  const lifetime = {scope: {serverId: 'server', viewerId: 'viewer'}, current: () => true};
  const {NativeAudiobookController} = await screenModule(new URL('../src-next/screens/destinations/audiobooks/model.ts', import.meta.url), {
    react: {default: {}}, '../lifetime': {nativeDestinationLifetime: () => lifetime},
    '../../../../../../packages/client-core/src/library-content': {LibraryContentService},
    '../../../../../../packages/client-core/src/container-state': {getContainerState: async () => ({revision: 4, watched: false})},
  });
  class ObservedController extends NativeAudiobookController {
    constructor(options: any) {
      super(options); instances.push({id: options.id, controller: this});
      this.subscribe(() => {
        if (options.id === 'book-1' && !reentered && this.getSnapshot().phase === 'loading') {
          reentered = true; snapshot = {route: routeB, revision: 2}; callbacks.forEach(fn => fn());
          successor = renderer.render(routeB, {});
        }
      });
    }
  }
  const react = {createElement: (type: any, props: any) => ({type, props})};
  const module = await screenModule(new URL('../src-next/screens/destinations/audiobooks/register.tsx', import.meta.url), {
    react: {default: react}, '../../../shell/route-registry': {nativeShellRoutes: {registerRoute() {}}},
    '../lifetime': {nativeDestinationLifetime: () => lifetime}, './model': {NativeAudiobookController: ObservedController},
    '../entry-actions': {createNativeEntryActions: () => ({menu() {}, dispose() {}})},
    './BookScreen': {BookScreen: 'book'}, './AuthorSeriesScreen': {AuthorSeriesScreen: 'author'},
  });
  const api = {request: async (path: string) => {
    const url = new URL(path, 'https://server.test');
    if (!url.pathname.startsWith('/v1/items/')) return rebuiltLibrary;
    const id = decodeURIComponent(url.pathname.split('/')[3]!); requests.push(id);
    return rebuiltBook(id, 0);
  }};
  const context = {api, authority: () => ({}), playback: {start: async () => {}},
    subscribeAuthority: (fn: () => void) => {callbacks.add(fn); return () => callbacks.delete(fn);},
    navigation: {getSnapshot: () => snapshot, isCurrent: () => true, subscribe: (fn: () => void) => {callbacks.add(fn); return () => callbacks.delete(fn);}}};
  renderer = module.createNativeBookRenderer(context);
  assert.equal(renderer.render(routeA, {}), null);
  assert.ok(successor?.props.controller);
  assert.equal(requests.filter(id => id === 'book-1').length, 0, 'retired A never dispatches');
  const cachedA = instances.find(value => value.id === 'book-1').controller;
  assert.equal(cachedA.getSnapshot().phase, 'idle');
  assert.equal(renderer.render(routeB, {}).props.controller, successor.props.controller);
  snapshot = {route: routeA, revision: 3}; callbacks.forEach(fn => fn());
  const revisited = renderer.render(routeA, {});
  assert.ok(revisited);
  assert.equal(instances.filter(value => value.id === 'book-1').length, 1, 'the original cached controller is reused');
  assert.equal(cachedA.getSnapshot().phase, 'loading', 'a live opening publishes shared loading');
  await new Promise(resolve => setImmediate(resolve));
  assert.equal(requests.filter(id => id === 'book-1').length, 1);
  assert.equal(cachedA.getSnapshot().phase, 'ready');
  renderer.dispose();
});

test('real author and series book continuations retain ordered eighty-one row membership', async () => {
  for (const kind of ['author', 'book_series'] as const) {
    const id = kind === 'author' ? 'author-1' : 'series-1';
    const rows = Array.from({length: 81}, (_, i) => volume(`row-${i}`));
    // Both pages read the rebuilt server: their own title, and the books a page at a time.
    const api = {request: async (path: string) => {
      const url = new URL(path, 'https://server.test'), cursor = url.searchParams.get('cursor');
      const start = cursor === 'page-3' ? 80 : cursor ? 40 : 0;
      if (url.pathname === '/v1/people/author-1') return {id, name: 'Original', images: {}};
      if (url.pathname === '/v1/items/series-1') return rebuiltSeries;
      if (url.pathname.startsWith('/v1/libraries/')) return rebuiltLibrary;
      return {items: rows.slice(start, start + 40), total: 81, ...(start === 80 ? {} : {nextCursor: start === 40 ? 'page-3' : 'page-2'})};
    }};
    const {NativeAudiobookController} = await screenModule(new URL('../src-next/screens/destinations/audiobooks/model.ts', import.meta.url), {
      react: {default: {}}, '../lifetime': {nativeDestinationLifetime: () => ({scope: {serverId: 'server', viewerId: 'viewer'}, current: () => true})},
      '../../../../../../packages/client-core/src/library-content': {LibraryContentService},
      '../../../../../../packages/client-core/src/container-state': {getContainerState: async () => ({revision: 4, watched: false})},
    });
    const value = new NativeAudiobookController({context: {api, playback: {}}, kind, libraryId: 'books', id});
    await value.load(lease);
    const first = value.getSnapshot().sections[0].entries[0];
    await value.next('books', lease); await value.next('books', lease);
    assert.deepEqual(value.getSnapshot().sections[0].entries.map((row: any) => row.id), rows.map(row => row.id));
    assert.equal(value.getSnapshot().sections[0].entries[0], first);
    assert.equal(value.getSnapshot().entity.id, id);
    value.dispose();
  }
});

test('real stale continuation retains rows and only explicit recovery refreshes page one', async () => {
  const requests: string[] = [];
  const api = {request: async (path: string) => {
    const url = new URL(path, 'https://server.test');
    if (url.pathname === '/v1/items/series-1') return rebuiltSeries;
    if (url.pathname.startsWith('/v1/libraries/')) return rebuiltLibrary;
    requests.push(path);
    if (url.searchParams.get('cursor')) throw Object.assign(new Error('expired'), {code: 'invalid_cursor'});
    return {items: [volume('book-a')], total: 41, nextCursor: 'expired'};
  }};
  const {NativeAudiobookController} = await screenModule(new URL('../src-next/screens/destinations/audiobooks/model.ts', import.meta.url), {
    react: {default: {}}, '../lifetime': {nativeDestinationLifetime: () => ({scope: {serverId: 'server', viewerId: 'viewer'}, current: () => true})},
    '../../../../../../packages/client-core/src/library-content': {LibraryContentService},
    '../../../../../../packages/client-core/src/container-state': {getContainerState: async () => ({revision: 4, watched: false})},
  });
  const value = new NativeAudiobookController({context: {api, playback: {}}, kind: 'book_series', libraryId: 'books', id: 'series-1'});
  await value.load(lease); await value.next('books', lease);
  assert.equal(requests.length, 2);
  assert.equal(value.getSnapshot().continuation.phase, 'refresh-required');
  assert.equal(value.getSnapshot().sections[0].entries[0].id, 'book-a');
  assert.equal(value.getSnapshot().entity.id, 'series-1');
  await value.next('books', lease); assert.equal(requests.length, 2);
  await value.recover(lease); assert.equal(requests.length, 3);
  assert.equal(new URL(requests[2], 'https://server.test').searchParams.has('cursor'), false);
  value.dispose();
});

test('a late real continuation from a departed opening cannot change cached rows or a successor', async () => {
  let resolveOld!: (v: any) => void, pending = true, calls = 0;
  const page = (cursor: boolean) => cursor ? {items: [volume('book-b')], total: 2} : {items: [volume('book-a')], total: 2, nextCursor: 'next'};
  const api = {request: async (path: string) => {
    const url = new URL(path, 'https://server.test');
    if (url.pathname === '/v1/items/series-1') return rebuiltSeries;
    if (url.pathname.startsWith('/v1/libraries/')) return rebuiltLibrary;
    const cursor = url.searchParams.has('cursor');
    if (!cursor) return page(false);
    calls++;
    if (pending) {pending = false; return new Promise(resolve => {resolveOld = resolve;});}
    return page(true);
  }};
  const {NativeAudiobookController} = await screenModule(new URL('../src-next/screens/destinations/audiobooks/model.ts', import.meta.url), {
    react: {default: {}}, '../lifetime': {nativeDestinationLifetime: () => ({scope: {serverId: 'server', viewerId: 'viewer'}, current: () => true})},
    '../../../../../../packages/client-core/src/library-content': {LibraryContentService},
    '../../../../../../packages/client-core/src/container-state': {getContainerState: async () => ({revision: 4, watched: false})},
  });
  const value = new NativeAudiobookController({context: {api, playback: {}}, kind: 'book_series', libraryId: 'books', id: 'series-1'});
  const abort = new AbortController(), old = {signal: abort.signal, isCurrent: () => !abort.signal.aborted};
  await value.load(old);
  const oldRequest = value.next('books', old);
  abort.abort(); await oldRequest;
  const successor = {signal: new AbortController().signal, isCurrent: () => true};
  await value.next('books', successor);
  resolveOld(page(true)); await Promise.resolve();
  assert.equal(calls, 2);
  assert.deepEqual(value.getSnapshot().sections[0].entries.map((v: any) => v.id), ['book-a', 'book-b']);
  value.dispose();
});

test('a true-returning predicate cannot dispatch after disposing the owner or aborting the signal', async () => {
  for (const retirement of ['dispose', 'abort']) {
    let reads = 0;
    const {value, starts, writes} = await controller({getContainerState: async () => {reads++; return {revision: 4, watched: false};}});
    await value.load(lease);
    const abort = new AbortController();
    const retiring = {signal: abort.signal, isCurrent: () => {if (retirement === 'dispose') value.dispose(); else abort.abort(); return true;}};
    const before = reads;
    await value.markFinished(retiring);
    await value.resume(retiring);
    assert.equal(reads, before);
    assert.equal(writes.length, 0);
    assert.equal(starts.length, 0);
    value.dispose();
  }
});

test('retired queued contexts are pruned before a valid successor is admitted', async () => {
  const calls: string[] = [], resolvers: (() => void)[] = [];
  const sections = ['chapters', 'files', 'books'].map(id => ({id, type: 'list', heading: {key: id}, entries: [], totalCount: 81, nextCursor: 'next'}));
  const snapshot = {phase: 'ready', error: null, sections, appended: sections, pagination: {cursor: null}, projection: {entity, listening, sections}};
  class Service {
    subscribe = () => () => {}; getSnapshot = () => snapshot; select = async () => {};
    next = async (id: string) => {calls.push(id); await new Promise<void>(resolve => resolvers.push(resolve));}; dispose = () => {};
  }
  const {NativeAudiobookController} = await screenModule(new URL('../src-next/screens/destinations/audiobooks/model.ts', import.meta.url), {
    react: {default: {}}, '../lifetime': {nativeDestinationLifetime: () => ({scope: {serverId: 'server', viewerId: 'viewer'}, current: () => true})},
    '../../../../../../packages/client-core/src/library-content': {LibraryContentService: Service},
    '../../../../../../packages/client-core/src/container-state': {getContainerState: async () => ({revision: 4, watched: false})},
  });
  const value = new NativeAudiobookController({context: {api: {}, playback: {}}, kind: 'book', libraryId: 'books', id: 'book-1'});
  await value.load(lease);
  let active = true;
  const old = {signal: new AbortController().signal, isCurrent: () => active};
  const flight = value.next('chapters', old); active = false;
  const retired: Promise<void>[] = [];
  for (let i = 0; i < 6; i++) {
    let current = true;
    const opening = {signal: new AbortController().signal, isCurrent: () => current};
    retired.push(value.next('files', opening)); current = false;
  }
  const successor = {signal: new AbortController().signal, isCurrent: () => true};
  const result = value.next('books', successor);
  resolvers[0](); await flight;
  assert.deepEqual(calls, ['chapters', 'books']);
  resolvers[1](); await Promise.all([...retired, result]);
  value.dispose();
});

test('renderer stops guarded navigation reads when a predicate disposes it but returns true', async () => {
  const route = {kind: 'book', entry: entity};
  const lifetime = {scope: {serverId: 'server', viewerId: 'viewer'}, current: () => true};
  const {NativeAudiobookController} = await screenModule(new URL('../src-next/screens/destinations/audiobooks/model.ts', import.meta.url), {
    react: {default: {}}, '../lifetime': {nativeDestinationLifetime: () => lifetime},
    '../../../../../../packages/client-core/src/library-content': {LibraryContentService: FakeLibraryContentService},
    '../../../../../../packages/client-core/src/container-state': {getContainerState: async () => ({revision: 4, watched: false})},
  });
  const react = {createElement: (type: any, props: any) => ({type, props})};
  const {createNativeBookRenderer} = await screenModule(new URL('../src-next/screens/destinations/audiobooks/register.tsx', import.meta.url), {
    react: {default: react}, '../../../shell/route-registry': {nativeShellRoutes: {registerRoute() {}}},
    '../lifetime': {nativeDestinationLifetime: () => lifetime}, './model': {NativeAudiobookController},
    '../entry-actions': {createNativeEntryActions: () => ({menu() {}, dispose() {}})},
    './BookScreen': {BookScreen: 'book'}, './AuthorSeriesScreen': {AuthorSeriesScreen: 'author'},
  });
  let disposed = false, renderer: any, starts = 0;
  const context = {api: {}, authority: () => ({}), playback: {start: async () => {starts++;}}, subscribeAuthority: () => () => {},
    navigation: {subscribe: () => () => {}, getSnapshot: () => {if (disposed) throw new Error('retired getter read'); return {route, revision: 1};},
      isCurrent: () => {disposed = true; renderer.dispose(); return true;}}};
  renderer = createNativeBookRenderer(context);
  assert.equal(renderer.render(route, {}), null);
  assert.equal(starts, 0);
  const liveContext = {...context, navigation: {...context.navigation, isCurrent: () => true, getSnapshot: () => ({route, revision: 1})}};
  const liveRenderer = createNativeBookRenderer(liveContext);
  assert.ok(liveRenderer.render(route, {}));
  liveRenderer.dispose();
});
