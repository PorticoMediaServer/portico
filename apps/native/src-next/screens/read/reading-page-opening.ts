import {browseCapabilitiesPath, parseBrowseCapabilities, type BrowseCapabilities} from '../../../../../packages/client-core/src/browse';
import {sameAuthority} from '../../shell/model';
import type {NativeReadNavigationContext, NativeReadOrigin} from './eligibility';
import {isReadingLibraryKind} from './platform';
import type {NativeDownloadRequest} from '../downloads/request';
import type {ContentEntry} from '../../../../../packages/client-core/src/library-content';
import type {NativeLibrarySnapshot} from '../library/model';

/** One committed Reading library page, not a press-time route or a new grant. */
export type NativeReadingPageOpening = Readonly<{
  context: NativeReadNavigationContext; libraryId: string; kind: string; origin: NativeReadOrigin;
  signal: AbortSignal; isCurrent(): boolean; dispose(): void;
  capabilities(): Promise<BrowseCapabilities>;
}>;
export type NativeReadingOfferLifetime = Readonly<{signal: AbortSignal; isCurrent(): boolean}>;
export type NativeReadingDownloadOffer = Readonly<{
  opening: NativeReadingPageOpening; item: Readonly<{id: string; title: string}>;
  lifetime: NativeReadingOfferLifetime;
}>;
const issued = new WeakSet<NativeReadingPageOpening>();
const offers = new WeakSet<NativeReadingDownloadOffer>();
const retired = () => Object.assign(new Error('native_reading_page_retired'), {name: 'AbortError', code: 'cancelled'});

/** Called in layout with the render's own route/revision. All asynchronous work
 * and offered actions remain bound to this original family, API and library kind. */
export function nativeReadingPageOpening(context: NativeReadNavigationContext,
  target: Readonly<{libraryId: string; kind: string}>, rendered: NativeReadOrigin): NativeReadingPageOpening | undefined {
  const api = context.api, selected = context.session(), authority = Object.freeze({...context.authority()});
  const {libraryId, kind} = target, route = rendered.route;
  if (!selected || !context.current() || !isReadingLibraryKind(kind) || route.kind !== 'shell'
    || route.destination.kind !== 'library' || route.destination.libraryId !== libraryId) return undefined;
  const viewer = Object.freeze({...selected.viewer}), family = selected.sessionFamilyId;
  if (viewer.serverId !== authority.serverId || viewer.accountId !== authority.accountId || viewer.profileId !== authority.profileId) return undefined;
  const origin = Object.freeze({route, revision: rendered.revision}), life = new AbortController();
  const current = () => {
    try {
      const live = context.session();
      if (life.signal.aborted || !context.current() || !live || live.sessionFamilyId !== family
        || !sameAuthority(authority, context.authority()) || live.viewer.authority !== viewer.authority || live.viewer.role !== viewer.role
        || live.viewer.serverId !== viewer.serverId || live.viewer.accountId !== viewer.accountId || live.viewer.profileId !== viewer.profileId
        || !context.libraries().some(value => value.id === libraryId && value.kind === kind)) return false;
      const navigation = context.navigation.getSnapshot();
      return !life.signal.aborted && navigation.route === origin.route && navigation.revision === origin.revision;
    } catch {return false;}
  };
  if (!current()) return undefined;
  const opening: NativeReadingPageOpening = Object.freeze({context, libraryId, kind, origin, signal: life.signal,
    isCurrent: current, dispose: () => life.abort(),
    async capabilities() {
      if (!current()) throw retired();
      const raw = await api.request<unknown>(browseCapabilitiesPath(libraryId, kind === 'comic' ? 'comics' : 'books'), 'GET', undefined, life.signal);
      if (!current()) throw retired();
      const data = parseBrowseCapabilities(raw);
      if (data.library.id !== libraryId || data.library.kind !== kind) throw new Error('native_reading_capability_scope');
      if (!current()) throw retired();
      return data;
    }});
  issued.add(opening);
  return opening;
}

/** A retained sparse row belongs to its original query as well as its
 * collection. invalidate() deliberately keeps old rows visible while loading;
 * residence alone must not admit an action under the new filter or sort. */
export function nativeReadingRowLifetime(opening: NativeReadingPageOpening, entry: ContentEntry,
  drawn: NativeLibrarySnapshot, read: () => NativeLibrarySnapshot, lease: NativeReadingOfferLifetime): NativeReadingOfferLifetime {
  const collection = drawn.collection, signal = lease.signal, leasedCurrent = lease.isCurrent;
  const current = () => {
    try {
      if (!collection || drawn.phase !== 'ready' || signal.aborted || !opening.isCurrent() || !leasedCurrent()) return false;
      if (drawn.library.id !== opening.libraryId || drawn.library.kind !== opening.kind
        || (entry.libraryId !== undefined && entry.libraryId !== opening.libraryId) || (entry.kind !== 'ebook' && entry.kind !== 'comic')) return false;
      const live = read();
      return live.phase === 'ready' && live.library.id === drawn.library.id && live.library.kind === drawn.library.kind
        && live.tab === drawn.tab && live.collection === collection && live.queryRevision === drawn.queryRevision
        && live.predicates === drawn.predicates && live.sort === drawn.sort && collection.residentItems().some(value => value === entry)
        && !signal.aborted && leasedCurrent() && opening.isCurrent();
    } catch {return false;}
  };
  return Object.freeze({signal, isCurrent: current});
}

/** The original offered object remains private in resident(). Display fields are
 * copied once; a menu or version sheet cannot borrow a successor's row or route. */
export function nativeReadingDownloadOffer(opening: NativeReadingPageOpening,
  item: Readonly<{id: string; title: string}>, resident: () => boolean,
  offeredLife: NativeReadingOfferLifetime = opening): NativeReadingDownloadOffer | undefined {
  if (!issued.has(opening)) return undefined;
  const signal = offeredLife.signal, offeredCurrent = offeredLife.isCurrent;
  const current = () => {
    try {return opening.isCurrent() && !signal.aborted && offeredCurrent() === true && resident() === true
      && !signal.aborted && opening.isCurrent();} catch {return false;}
  };
  if (!current()) return undefined;
  const target = Object.freeze({id: item.id, title: item.title});
  if (!target.id || !target.title || !current()) return undefined;
  const offer = Object.freeze({opening, item: target, lifetime: Object.freeze({signal, isCurrent: current})});
  offers.add(offer); return offer;
}

/** One page's Download menu. Claim before reading the offered row or calling
 * availability/currentness code; a retired callback can only touch this store.
 * The request keeps its row lifetime after the menu has been consumed. */
export class NativeReadingDownloads {
  private value?: NativeReadingDownloadOffer;
  private claimed = false;
  private disposed = false;
  private listeners = new Set<() => void>();
  private readonly opening: NativeReadingPageOpening | undefined;
  private readonly available: () => boolean;
  private readonly request: (value: NativeDownloadRequest) => void;
  constructor(opening: NativeReadingPageOpening | undefined, available: () => boolean, request: (value: NativeDownloadRequest) => void) {
    this.opening = opening; this.available = available; this.request = request;
  }
  getSnapshot = () => this.value;
  subscribe = (listener: () => void) => {this.listeners.add(listener); return () => {this.listeners.delete(listener);};};
  private current(offer: NativeReadingDownloadOffer): boolean {
    if (this.disposed || !offers.has(offer) || offer.opening !== this.opening) return false;
    const current = offer.lifetime.isCurrent();
    return current && !this.disposed;
  }
  private publish(): void {for (const listener of [...this.listeners]) listener();}
  offer = (capture: () => NativeReadingDownloadOffer | undefined): void => {
    if (this.disposed || this.claimed) return;
    this.claimed = true;
    try {
      const offer = capture();
      if (!offer || !this.current(offer) || !this.available() || !this.current(offer)) return;
      this.value = offer; this.publish();
    } finally {this.claimed = false;}
  };
  dismiss = (offer: NativeReadingDownloadOffer | undefined): void => {
    if (!offer || this.disposed || this.value !== offer) return;
    this.value = undefined; this.publish();
  };
  select = (offer: NativeReadingDownloadOffer | undefined): void => {
    if (this.disposed || this.claimed) return;
    this.claimed = true;
    try {
      if (!offer || this.value !== offer || !this.current(offer) || !this.available() || !this.current(offer) || this.value !== offer) return;
      this.value = undefined; this.publish();
      if (!this.current(offer)) return;
      this.request({title: offer.item.title, itemId: offer.item.id, reading: true, lifetime: offer.lifetime});
    } finally {this.claimed = false;}
  };
  dispose = (): void => {this.disposed = true; this.value = undefined; this.listeners.clear();};
}
