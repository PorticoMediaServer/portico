import {LibraryContentService, readListeningSimilar, type ContentEntry, type ContentProjection} from '@core/library-content.ts';
import {getContainerState, setContainerFlag, setContainerState} from '@core/container-state.ts';
import {browseCapabilitiesPath, parseBrowseCapabilities, validateBrowseQuery, type BrowseCapabilities} from '@core/browse.ts';
import {RelationRowPager, type RelationPage} from '@core/relation-rows.ts';
import {screenLifetime, type ScreenContext} from '../context';

export type AudiobookTarget = Readonly<{libraryId: string; view: 'book' | 'author' | 'book_series'; entityId: string}>;
export function createAudiobookBindings(context: ScreenContext, input: AudiobookTarget) {
  const target = Object.freeze({...input});
  const life = screenLifetime(context);
  const service = new LibraryContentService({api: life.api, scope: context.scope, pageSize: 40});
  const current = (page: ContentProjection) => {
    life.assert();
    const snapshot = service.getSnapshot();
    if (snapshot.phase !== 'ready' || snapshot.projection !== page || page.scope.libraryId !== target.libraryId
      || page.scope.libraryKind !== 'audiobook' || page.scope.view !== target.view || page.scope.entityId !== target.entityId
      || page.entity?.id !== target.entityId || page.entity.kind !== target.view) throw new DOMException('Audiobook page changed', 'AbortError');
  };
  const entries = () => service.getSnapshot().appended.flatMap(section => section.entries);
  const viewer = context.identity?.viewer;
  const similarScope = viewer && (viewer.authority === 'local' || viewer.authority === 'hosted')
    ? Object.freeze({serverId: context.scope.serverId, authority: viewer.authority, accountId: viewer.accountId, profileId: viewer.profileId}) : undefined;
  /** Keep the source cursor even when every entry in this bounded page is filtered out.
   * The visible shelf, rather than an eager scan, asks for each following source page. */
  const readMoreBooks = async (page: ContentProjection, cursor?: string, signal?: AbortSignal): Promise<RelationPage> => {
    current(page);
    if (target.view !== 'book' || !similarScope) return Object.freeze({entries: Object.freeze([])});
    const value = await readListeningSimilar(life.api, similarScope, target.entityId,
      {limit: cursor ? 24 : 12, cursor, signal: life.signal(signal)});
    current(page);
    if (value.itemId !== target.entityId || value.libraryId !== target.libraryId || value.bySound
      || value.revision.catalog !== page.revision.catalog || value.revision.viewer !== page.revision.viewer)
      throw Object.assign(new Error('Book page changed'), {code: 'stale_continuation'});
    return Object.freeze({entries: Object.freeze(value.entries.filter(entry => entry.kind === 'book' && entry.libraryId === target.libraryId && entry.available !== false && !entry.missing)),
      ...(value.nextCursor ? {nextCursor: value.nextCursor} : {})});
  };
  // The exact page whose checked filters may act, and the one opener both links use.
  let filterFact: Readonly<{page: ContentProjection; capabilities: BrowseCapabilities}> | undefined;
  const openFilter = async (page: ContentProjection, field: 'narrator' | 'series', name: string, member: boolean) => {
    current(page);
    if (target.view !== 'book' || !filterFact || filterFact.page !== page || !member || typeof name !== 'string' || !name.trim()) throw new DOMException('Book filter unavailable', 'AbortError');
    const open = context.effects.openBrowse;
    if (!open) throw new DOMException('Browse unavailable', 'AbortError');
    // validateBrowseQuery does not check kind applicability: the requested field itself must be
    // book-applicable and offer equals, never merely present because the other link was.
    const descriptor = filterFact.capabilities.fields.find(value => value.id === field);
    if (!descriptor || !descriptor.applicableKinds.includes('book') || !descriptor.operators.includes('equals')) throw new DOMException('Book filter unavailable', 'AbortError');
    const query = Object.freeze({field, operator: 'equals' as const, value: name});
    validateBrowseQuery(filterFact.capabilities, query);
    open({libraryId: target.libraryId, pivot: 'books', query});
    current(page);
  };
  const play = async (page: ContentProjection, entry: ContentEntry, startSeconds = entry.playback?.startSeconds ?? 0) => {
    current(page);
    if (!entries().includes(entry) || !entry.playback || entry.available === false || entry.missing) throw new DOMException('Audiobook target unavailable', 'AbortError');
    await context.effects.play({libraryId: target.libraryId, itemId: entry.playback.itemId, startSeconds}, life.signal(),
      () => {try {current(page);return entries().includes(entry);} catch {return false;}});
    current(page);
  };
  return {
    context, service, current,
    async load() {await service.select(target); const page = service.getSnapshot().projection; if (page) current(page);},
    async finished(page: ContentProjection) {current(page);if (target.view !== 'book') throw new DOMException('Not a book', 'AbortError');const state = await getContainerState(life.api, 'book', target.entityId, life.signal());current(page);return state;},
    async markFinished(page: ContentProjection) {
      current(page);
      if (target.view !== 'book') throw new DOMException('Not a book', 'AbortError');
      const state = await getContainerState(life.api, 'book', target.entityId, life.signal());current(page);
      const result = await setContainerState(life.api, 'book', target.entityId, {expectedRevision: state.revision, watched: !state.watched}, life.signal());
      current(page);return result;
    },
    play,
    /** The server's own related books, filtered without losing its source continuation. */
    moreBooks(page: ContentProjection, signal?: AbortSignal) {return readMoreBooks(page, undefined, signal);},
    moreBooksPager(page: ContentProjection, first: RelationPage) {
      current(page);
      return new RelationRowPager(first, (cursor, signal) => readMoreBooks(page, cursor, signal));
    },
    /** Reads once and retains this exact page's own checked Books capabilities: which real
     * filters it offers for book kinds, so only those become destinations. */
    async filterSupport(page: ContentProjection) {
      const none = Object.freeze({narrator: false, series: false});
      current(page);
      if (target.view !== 'book' || !context.effects.openBrowse) return none;
      const capabilities = parseBrowseCapabilities(await life.api.request<unknown>(browseCapabilitiesPath(target.libraryId, 'books')));
      current(page);
      if (capabilities.library.id !== target.libraryId || capabilities.library.kind !== page.scope.libraryKind) return none;
      const pivot = capabilities.resolvedPivot;
      if (!pivot || pivot.id !== 'books' || !pivot.browsable) return none;
      const offers = (id: string) => {
        const field = capabilities.fields.find(value => value.id === id);
        return !!field && field.applicableKinds.includes('book') && field.operators.includes('equals');
      };
      const support = Object.freeze({narrator: offers('narrator'), series: offers('series')});
      if (support.narrator || support.series) filterFact = Object.freeze({page, capabilities});
      return support;
    },
    /** Opens this library's Books filtered by the exact narrator the page drew. */
    async openNarrator(page: ContentProjection, name: string) {
      await openFilter(page, 'narrator', name, page.entity?.narrators?.includes(name) === true);
    },
    /** Opens this library's Books filtered by the exact drawn series row, and only when this book's
     * own published series name is character-for-character that same drawn title. */
    async openSeries(page: ContentProjection, entry: ContentEntry, name: string) {
      current(page);
      // Exactly the context rows Entry renders: only the appended context section.
      const rows = service.getSnapshot().appended.filter(section => section.id === 'context').flatMap(section => section.entries);
      if (entry.kind !== 'book_series' || entry.libraryId !== target.libraryId || entry.title !== name || !rows.includes(entry)
        || page.entity?.series?.some(value => value.name === name) !== true) throw new DOMException('Book filter unavailable', 'AbortError');
      await openFilter(page, 'series', name, true);
    },
    /** The book's own Watchlist flag, read from the container state the server already keeps. */
    async watchlisted(page: ContentProjection) {
      current(page);
      if (target.view !== 'book') throw new DOMException('Not a book', 'AbortError');
      const state = await getContainerState(life.api, 'book', target.entityId, life.signal());
      current(page);
      return state.watchlisted;
    },
    /** One plain flag write; the server treats it as last-write-wins, so no revision is claimed. */
    async setWatchlisted(page: ContentProjection, value: boolean) {
      current(page);
      if (target.view !== 'book' || typeof value !== 'boolean') throw new DOMException('Not a book', 'AbortError');
      const state = await setContainerFlag(life.api, 'book', target.entityId, 'watchlisted', value, life.signal());
      current(page);
      return state.watchlisted;
    },
    async primary(page: ContentProjection, restart = false) {
      current(page);
      const rows = entries();
      if (target.view === 'book') {
        const journey = page.listening;
        if (!journey?.actions.includes('play') || journey.resumeUnavailable && !restart) throw new DOMException('Audiobook unavailable', 'AbortError');
        const resume = !restart ? journey.resume : undefined;
        if (resume) {
          // The server's resume may refer to a part outside this bounded window.
          await context.effects.play({libraryId: target.libraryId, itemId: resume.itemId, startSeconds: 0, storedResume: true}, life.signal(),
            () => {try {current(page);return true;} catch {return false;}});
          return;
        }
        const first = rows.find(entry => entry.kind === 'audiobook_file' && entry.playback) ?? rows.find(entry => entry.kind === 'chapter' && entry.playback);
        if (!first) throw new DOMException('Audiobook unavailable', 'AbortError');
        return play(page, first, 0);
      }
      const book = rows.find(entry => entry.kind === 'book' && entry.bookProgress && entry.available !== false && !entry.missing);
      if (!book?.bookProgress) throw new DOMException('Audiobook resume unavailable', 'AbortError');
      await context.effects.play({libraryId: target.libraryId, itemId: book.bookProgress.itemId, startSeconds: 0, storedResume: true}, life.signal(),
        () => {try {current(page);return entries().includes(book);} catch {return false;}});
    },
    open(page: ContentProjection, entry: ContentEntry) {current(page);if (!entries().includes(entry)) throw new DOMException('Audiobook entry changed', 'AbortError');context.effects.openEntry(entry);},
    dispose() {life.dispose();service.dispose();},
  };
}
export type AudiobookBindings = ReturnType<typeof createAudiobookBindings>;
