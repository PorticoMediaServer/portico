import {DetailService} from '@core/detail.ts';
import {getContainerState, setContainerFlag, setContainerState, type ContainerKind} from '@core/container-state.ts';
import type {ContentEntry} from '@core/library-content.ts';
import type {HomeEntry} from '@core/home.ts';
import type {SearchEntry} from '@core/search.ts';
import {homeEntryCapabilities} from '@core/home-selection.ts';
import {presentError} from '@core/presentation/errors.ts';
import type {EntryActionId, EntryCapabilities} from '@core/presentation/entry-actions.ts';
import {refreshAvailableCatalogueMetadata} from '@core/query/catalogue-menu.ts';
import {detailMenuCapabilities, isAggregateKind, isLeafKind, limitMenuCapabilities, titleMenuPlatform, type TitleMenuPorts} from '@core/presentation/title-menu.ts';
import {entryStartDisplay} from '../platform/playback/subject';
import {screenLifetime, type ScreenContext} from '../screens/detail/context';
import type {EntryActionTarget} from './entry-actions';

/** What a screen tells the menu about the exact card it opened for. */
export type TitleMenuSubject = Readonly<{
  entry: ContentEntry | HomeEntry;
  title: string;
  /** The screen still draws this exact entry (its page, query revision, admission). A retired opening never draws or runs. */
  current: () => boolean;
  /** The screen's own fenced Play, or none where the entry cannot play. */
  onPlay?: () => void;
  /** The screen's own selection toggle, or none where the card cannot be selected. */
  select?: () => void;
}>;

/** The part of a Detail service the menu uses; tests supply their own. */
export type TitleMenuDetail = Pick<DetailService, 'select' | 'getSnapshot' | 'mutate' | 'retryMutation' | 'dispose'>;
export type TitleMenuHostOptions = Readonly<{detail?: (context: ScreenContext, life: ReturnType<typeof screenLifetime>) => TitleMenuDetail}>;

export type TitleMenuPhase = 'loading' | 'ready' | 'failed';
/** One menu, bound to the exact opening: the anchor, subject and the grants read for it. */
export type TitleMenuOpening = Readonly<{token: object; anchor: HTMLElement; subject: TitleMenuSubject; phase: TitleMenuPhase; caps: EntryCapabilities; detail?: TitleMenuDetail}>;
export type TitleRatingPhase = 'loading' | 'ready' | 'saving' | 'result' | 'uncertain' | 'error';
export type TitleRatingSession = Readonly<{token: object; subject: TitleMenuSubject; detail: TitleMenuDetail; phase: TitleRatingPhase; value?: number | null; error?: string}>;
export type TitleMenuSnapshot = Readonly<{menu?: TitleMenuOpening; rating?: TitleRatingSession}>;

const errorText = (error: unknown) => presentError(error, 'home', {operation: 'action'}).body;

/** The entry's capabilities from what is already known: the server's grants when it sent them, otherwise only Details. */
export function knownCapabilities(entry: ContentEntry | HomeEntry, ports: TitleMenuPorts): EntryCapabilities {
  const home = (entry as HomeEntry).homeActions;
  // Without grants the screen's own Play is the one thing it already knows, and Details is always there.
  return limitMenuCapabilities(home ? homeEntryCapabilities(entry as HomeEntry, titleMenuPlatform) : {kind: entry.kind, details: true, ...(ports.play ? {playable: true} : {})}, ports);
}

/**
 * The state behind the one title action menu for every screen that is not Home (FX-S3, FX-S4). A card's ⋯ or a hold
 * opens the same rows the model gives Home. An entry the server sent grants for builds its menu at once; any other
 * title reads its Detail when the menu opens (cancelled with the menu), with Play, Select and Details there meanwhile.
 * A failed read leaves just those. Every row runs an effect the screen already owns.
 *
 * The menu belongs to its exact opening: the same card, entry and screen state. When the screen moves on (a new query,
 * another page, a retired authority) the menu is gone and nothing it held runs.
 */
export class TitleMenuHost {
  readonly anchor: {current: HTMLElement | null} = {current: null};
  private state: TitleMenuSnapshot = {};
  private listeners = new Set<() => void>();
  private lifeRef?: Readonly<{context: ScreenContext; life: ReturnType<typeof screenLifetime>}>;
  constructor(private readonly context: ScreenContext, private readonly options: TitleMenuHostOptions = {}) {}

  subscribe = (listener: () => void) => { this.listeners.add(listener); return () => { this.listeners.delete(listener); }; };
  getSnapshot = (): TitleMenuSnapshot => this.state;
  private publish(next: TitleMenuSnapshot) { this.state = next; for (const listener of [...this.listeners]) listener(); }

  /** Made on first use and again after a retirement, so a remounted host is never left with a dead lifetime. */
  private life() {
    if (!this.lifeRef || this.lifeRef.life.signal().aborted) this.lifeRef = {context: this.context, life: screenLifetime(this.context)};
    return this.lifeRef.life;
  }
  private detailFor(): TitleMenuDetail {
    const life = this.life();
    return this.options.detail ? this.options.detail(this.context, life)
      : new DetailService({queries: this.context.queries, api: life.api, scope: this.context.scope, requestId: async () => crypto.randomUUID()});
  }
  private ports(): Omit<TitleMenuPorts, 'play'> {
    const viewer = this.context.identity?.viewer, effects = this.context.effects;
    const localOwner = viewer?.role === 'owner' && viewer.authority === 'local';
    return {playlist: !!effects.addToPlaylist, collection: !!effects.addToCollection, edit: !!effects.editMetadata && localOwner, delete: !!effects.deleteTitle, refresh: localOwner, rate: true};
  }
  private reportError = (error: unknown) => { if (this.context.isCurrent()) this.context.effects.reportError(error); };
  private alive(opening: TitleMenuOpening) {
    return this.state.menu?.token === opening.token && this.life().current() && this.context.isCurrent() && opening.subject.current();
  }

  /** What is on screen now: an opening whose screen state has moved on is already gone. */
  live(): TitleMenuSnapshot {
    const {menu, rating} = this.state;
    return {...(menu && this.alive(menu) ? {menu} : {}), ...(rating && this.ratingAlive(rating) ? {rating} : {})};
  }
  /** Retire whatever the screen no longer serves. */
  sweep() {
    const {menu, rating} = this.state;
    if (menu && !this.alive(menu)) this.close(menu);
    if (rating && !this.ratingAlive(rating)) this.closeRating();
  }
  private ratingAlive(rating: TitleRatingSession) {
    return this.state.rating?.token === rating.token && this.life().current() && this.context.isCurrent() && rating.subject.current();
  }

  /** The stable opener screens hand to their cards. */
  readonly show = (anchor: HTMLElement, subject: TitleMenuSubject) => { void this.open(anchor, subject); };

  /** Resolves once the opening has settled (its Detail read answered); the menu is already drawn. */
  open(anchor: HTMLElement, subject: TitleMenuSubject): Promise<void> {
    // A retained opener cannot draw a menu for a page its screen no longer serves.
    if (!subject.current() || !this.life().current() || !this.context.isCurrent()) return Promise.resolve();
    this.state.menu?.detail?.dispose();
    const entry = subject.entry, token = {}, carried: TitleMenuPorts = {...this.ports(), play: !!subject.onPlay};
    this.anchor.current = anchor;
    const reads = !(entry as HomeEntry).homeActions && isLeafKind(entry.kind) && !!entry.libraryId;
    const detail = reads ? this.detailFor() : undefined;
    const opening: TitleMenuOpening = {token, anchor, subject, phase: reads ? 'loading' : 'ready', caps: knownCapabilities(entry, carried), ...(detail ? {detail} : {})};
    this.publish({...this.state, menu: opening});
    if (!detail || !entry.libraryId) return Promise.resolve();
    const libraryId = entry.libraryId;
    const settle = (next: Pick<TitleMenuOpening, 'phase'> & Partial<Pick<TitleMenuOpening, 'caps'>>) => {
      if (!this.alive(opening)) return;
      this.publish({...this.state, menu: {...opening, ...next}});
    };
    return detail.select({libraryId, itemId: entry.id}).then(() => {
      const state = detail.getSnapshot(), data = state.data;
      if (state.phase !== 'ready' || !data || data.item.id !== entry.id || data.item.kind !== entry.kind || data.scope.libraryId !== libraryId) { settle({phase: 'failed'}); return; }
      settle({phase: 'ready', caps: limitMenuCapabilities(detailMenuCapabilities({kind: data.item.kind, available: data.item.available, actions: data.actions, personal: data.personal}), carried)});
    }).catch(() => { settle({phase: 'failed'}); });
  }

  /** The menu was dismissed. A later opening is never closed by an earlier one's dismissal. */
  close(opening: TitleMenuOpening) {
    if (this.state.menu?.token !== opening.token) return;
    this.publish({...this.state, menu: undefined});
    opening.detail?.dispose();
  }

  /** An accepted action claims the opening once; only a personal change keeps the Detail it read. */
  private take(opening: TitleMenuOpening, act: () => void | Promise<unknown>, keepRead = false) {
    if (!this.alive(opening)) return;
    this.publish({...this.state, menu: undefined});
    if (!keepRead) opening.detail?.dispose();
    try { void Promise.resolve(act()).catch(this.reportError); } catch (error) { this.reportError(error); }
  }

  /** Select is the surface's own toggle. */
  select(opening: TitleMenuOpening) { this.take(opening, () => opening.subject.select?.()); }

  /** Run a row. Resolves once the effect it started has finished (for tests); the menu is already closed. */
  choose(opening: TitleMenuOpening, id: EntryActionId): Promise<void> {
    const effects = this.context.effects, entry = opening.subject.entry, element = opening.anchor, subject = opening.subject;
    const target: EntryActionTarget | undefined = isLeafKind(entry.kind) && entry.libraryId ? {libraryId: entry.libraryId, itemId: entry.id, kind: entry.kind, title: subject.title} : undefined;
    let done: Promise<unknown> = Promise.resolve();
    const act = (work: () => void | Promise<unknown>, keepRead = false) => {
      if (!this.alive(opening)) return;
      let pending: void | Promise<unknown> = undefined;
      this.take(opening, () => { pending = work(); return pending; }, keepRead);
      done = Promise.resolve(pending).catch(() => {});
    };
    switch (id) {
      case 'play': case 'resume': act(() => subject.onPlay?.()); break;
      case 'details': act(() => { effects.openEntry(entry as SearchEntry); }); break;
      case 'watchlist': case 'favorite': case 'watched': act(() => this.personal(opening, id), true); break;
      case 'rate': act(() => this.rateOpen(subject)); break;
      case 'addToPlaylist': act(() => target && effects.addToPlaylist?.(target, element)); break;
      case 'addToCollection': act(() => target && effects.addToCollection?.(target, element)); break;
      case 'delete': act(() => target && effects.deleteTitle?.(target, element)); break;
      case 'editMetadata': act(() => target ? effects.editMetadata?.(target, element)
        : entry.libraryId && isAggregateKind(entry.kind) ? effects.editMetadata?.({entityKind: entry.kind as 'show' | 'season' | 'album' | 'artist' | 'book', entityId: entry.id, libraryId: entry.libraryId, title: subject.title}, element) : undefined); break;
      case 'refreshMetadata': act(() => {
        const kind = entry.kind, id = entry.id, life = this.life();
        return refreshAvailableCatalogueMetadata(life.api, kind, id, life.signal());
      }); break;
      default: break;
    }
    return Promise.resolve(done).then(() => {});
  }

  private async personal(opening: TitleMenuOpening, id: 'watchlist' | 'favorite' | 'watched') {
    const life = this.life(), entry = opening.subject.entry, signal = life.signal();
    if (isAggregateKind(entry.kind)) {
      const kind = entry.kind as ContainerKind;
      const state = await getContainerState(life.api, kind, entry.id, signal);
      if (id === 'watched') await setContainerState(life.api, kind, entry.id, {watched: !state.watched}, signal);
      else await setContainerFlag(life.api, kind, entry.id, id === 'watchlist' ? 'watchlisted' : 'favorite', id === 'watchlist' ? !state.watchlisted : !state.favorite, signal);
      return;
    }
    if (!entry.libraryId) throw new Error('personal-action-unavailable');
    const detail = opening.detail ?? this.detailFor();
    try {
      if (detail.getSnapshot().phase !== 'ready') await detail.select({libraryId: entry.libraryId, itemId: entry.id});
      const data = detail.getSnapshot().data;
      if (!data || data.item.id !== entry.id) throw new Error('personal-action-unavailable');
      await detail.mutate({action: id, value: id === 'watchlist' ? !data.personal.watchlisted : id === 'favorite' ? !data.personal.favorite : !data.personal.watched});
      const result = detail.getSnapshot();
      if (result.pending.length || result.mutationError) throw Object.assign(new Error(result.mutationError?.code ?? 'personal-result-unconfirmed'), {code: result.mutationError?.code ?? 'personal-result-unconfirmed', retryable: true});
    } finally { detail.dispose(); }
  }

  /** Play for a card that has no play of its own (a detail page's row): the entry's own playback, an album's or an
   * artist's through the screen's effects, or nothing where this screen has no way to play it. */
  play = (entry: ContentEntry, isCurrent: () => boolean): (() => void) | undefined => {
    const effects = this.context.effects, playback = entry.playback;
    if (entry.libraryId && playback && entry.available === true) {
      const libraryId = entry.libraryId;
      return () => { void effects.play({libraryId, itemId: playback.itemId, startSeconds: playback.startSeconds ?? 0, display: entryStartDisplay(entry)}, this.life().signal(), isCurrent).catch(this.reportError); };
    }
    if (entry.kind === 'album' && effects.playAlbum) { const album = effects.playAlbum; return () => { void album(entry.id, false, this.life().signal(), isCurrent).catch(this.reportError); }; }
    if (entry.kind === 'artist' && effects.playArtist) { const artist = effects.playArtist; return () => { void artist(entry.id, this.life().signal(), isCurrent).catch(this.reportError); }; }
    return undefined;
  };

  // The rating dialog: it reads the title's Detail, saves through it, and is retired with its screen.
  private rateOpen(subject: TitleMenuSubject) {
    const entry = subject.entry;
    if (!entry.libraryId) return;
    this.state.rating?.detail.dispose();
    const first: TitleRatingSession = {token: {}, subject, detail: this.detailFor(), phase: 'loading'};
    this.publish({...this.state, rating: first});
    return this.ratingRead(first);
  }
  private ratingPatch(token: object, next: Partial<TitleRatingSession>) {
    const current = this.state.rating;
    if (current?.token !== token) return;
    this.publish({...this.state, rating: {...current, ...next}});
  }
  private ratingRead(session: TitleRatingSession) {
    const {token, subject, detail} = session, entry = subject.entry;
    return detail.select({libraryId: entry.libraryId!, itemId: entry.id}).then(() => {
      const live = this.state.rating;
      if (live?.token !== token || !this.ratingAlive(live)) return;
      const state = detail.getSnapshot(), data = state.data;
      if (state.phase !== 'ready' || !data || data.item.id !== entry.id || data.item.kind !== entry.kind || !data.actions.some(action => action.id === 'rating' && action.enabled)) { this.ratingPatch(token, {phase: 'error', error: errorText(state.error)}); return; }
      this.ratingPatch(token, {phase: 'ready', value: data.personal.rating, error: undefined});
    }).catch(error => { if (this.state.rating?.token === token) this.ratingPatch(token, {phase: 'error', error: errorText(error)}); });
  }
  private ratingOutcome(session: TitleRatingSession) {
    const snapshot = session.detail.getSnapshot();
    this.ratingPatch(session.token, snapshot.pending.length || snapshot.mutationError ? {phase: 'uncertain', error: errorText(snapshot.mutationError)} : {phase: 'result', value: snapshot.data?.personal.rating, error: undefined});
  }
  setRatingValue(session: TitleRatingSession, value: number | null) {
    if (session.phase !== 'ready' || !this.ratingAlive(session)) return;
    this.ratingPatch(session.token, {value});
  }
  async saveRating(session: TitleRatingSession) {
    if (session.phase !== 'ready' || session.value === undefined || !this.ratingAlive(session)) return;
    this.ratingPatch(session.token, {phase: 'saving', error: undefined});
    try { await session.detail.mutate({action: 'rating', value: session.value}); this.ratingOutcome(session); }
    catch (error) { this.ratingPatch(session.token, {phase: 'uncertain', error: errorText(error)}); }
  }
  async retryRating(session: TitleRatingSession) {
    if (!this.ratingAlive(session)) return;
    if (session.phase === 'error') { this.ratingPatch(session.token, {phase: 'loading', error: undefined}); await this.ratingRead(session); return; }
    this.ratingPatch(session.token, {phase: 'saving', error: undefined});
    try { await session.detail.retryMutation(); this.ratingOutcome(session); }
    catch (error) { this.ratingPatch(session.token, {phase: 'uncertain', error: errorText(error)}); }
  }
  closeRating() {
    const old = this.state.rating; if (!old) return;
    this.publish({...this.state, rating: undefined}); old.detail.dispose();
  }

  /** The screen is going: retire every opening and abort what they hold. A later use starts a fresh lifetime. */
  dispose() {
    const {menu, rating} = this.state;
    this.state = {};
    menu?.detail?.dispose(); rating?.detail.dispose();
    this.lifeRef?.life.dispose(); this.lifeRef = undefined;
    for (const listener of [...this.listeners]) listener();
  }
}
