import type {ReadingHostContentsTarget} from '../../../../packages/client-core/src/reading-host';
import type {ContentEntry} from '../../../../packages/client-core/src/library-content';
import type {HomeChannel, HomeSeeAll} from '../../../../packages/client-core/src/home';
import type {SavedRoute} from '../../../../packages/client-core/src/saved';
import type {ReadingItem} from '../../../../packages/client-core/src/reading';
import {sameAuthority, selectedTab, type ShellAuthority, type ShellDestination} from './model';

/** Descriptors are requests to the registered phase owner, never synthesized screen content. */
export type NativeContinueWatchingOffer = Readonly<{itemId: string; revision: number}>;
export type NativeShellRoute = Readonly<
  | {kind: 'shell'; destination: ShellDestination}
  | {kind: 'title'; entry: ContentEntry; continueWatching?: NativeContinueWatchingOffer}
  | {kind: 'music'; entry: ContentEntry}
  | {kind: 'book'; entry: ContentEntry}
  | {kind: 'audiobook'; entry: ContentEntry}
  | {kind: 'person'; personId: string}
  | {kind: 'read'; itemId: string; sourceId: string; downloadId?: string; /** Root54: the exact source revision a search hit named, proven again before the guest mounts. */ expectedSourceRevision?: string; contentsTarget?: ReadingHostContentsTarget}
  | {kind: 'read-book'; libraryId: string; item: ReadingItem}
  | {kind: 'read-series'; libraryId: string; seriesId: string}
  | {kind: 'read-periodical'; libraryId: string; groupId: string; publicationType: 'magazine' | 'newspaper'}
  /** Root54: the reader's own authored Search all books, carrying its literal
   * current words. This is an explicit indexed book-text mode, never inferred
   * from an ordinary Search query, and it is pushed above the reader that sent it. */
  | {kind: 'search-reading'; query: string}
  | {kind: 'settings-page'; scope: 'account'; page: 'reading'}
  | {kind: 'channel'; sourceId: string; channelId: string; guide?: import('../platform/playback/types').NativeChannelGuideOrigin;
      tune?: Readonly<{fence: import('../../../../packages/player-core/src').ChannelPlaybackFence; sourceGeneration: string}>}
  | {kind: 'home-see-all'; destination: HomeSeeAll}
  | {kind: 'home-customize'}
  | {kind: 'profiles'; profileId?: string}
  | {kind: 'servers'; initialPanel?: 'consent-kind'}
  | {kind: 'notifications'}
  | {kind: 'licenses'}
  | {kind: 'together'}
  | {kind: 'account'}
  | {kind: 'downloads'}
  | {kind: 'edit-pins'}
  | {kind: 'search-genre'; value: string; label: string}
  | {kind: 'search-genres'}
  | {kind: 'saved-view'; view: SavedRoute}
  | {kind: 'saved-destination'; destination: Readonly<{kind: 'playlist' | 'collection'; id: string}>}
  | {kind: 'settings-equalizer'}
  | {kind: 'recordings'}
  | {kind: 'server-feedback'}
  | {kind: 'server-dashboard'}
  | {kind: 'server-settings'; channelId?: string; page?: 'general' | 'history' | 'libraries' | 'remote-sources' | 'metadata' | 'segments' | 'live' | 'people' | 'signin' | 'remote' | 'integrations' | 'streaming' | 'storage'}
  | {kind: 'server-segments'}
  | {kind: 'collection'; libraryId: string; collectionId: string; title: string}
  | {kind: 'library-edit-pins' | 'library-settings'; libraryId: string}
>;

export const titleRoute = (entry: ContentEntry, continueWatching?: NativeContinueWatchingOffer): NativeShellRoute =>
  Object.freeze({kind: 'title', entry, ...(continueWatching ? {continueWatching} : {})});
/** The Home entry's own offer and personal revision, captured with the route; never a new read. */
type ContinueSource = ContentEntry & {homeActions?: {removeFromContinue?: boolean; state?: {personalRevision?: number}}};
const continueOffer = (entry: ContentEntry): NativeContinueWatchingOffer | undefined => {
  const actions = (entry as ContinueSource).homeActions, revision = actions?.state?.personalRevision;
  return actions?.removeFromContinue === true && typeof revision === 'number' && Number.isSafeInteger(revision) && revision >= 0
    ? Object.freeze({itemId: entry.id, revision}) : undefined;
};
/** Albums and artists open on their own drawn page on every platform; every other
 * entry keeps the existing title route exactly. */
export const nativeEntryRouteSupported = (entry: ContentEntry): boolean =>
  ['movie', 'show', 'season', 'episode', 'extra', 'album', 'artist', 'book', 'author', 'book_series', 'playlist'].includes(entry.kind)
  || entry.kind === 'collection' && !!entry.libraryId
  || entry.kind === 'category' && !!entry.libraryId && entry.navigation?.view === 'releases' && entry.navigation.browse?.pivot === 'albums';
export const nativeEntryRoute = (entry: ContentEntry): NativeShellRoute => {
  if (entry.kind === 'collection' && entry.libraryId) return Object.freeze({kind: 'collection', libraryId: entry.libraryId, collectionId: entry.id, title: entry.title});
  if (entry.kind === 'playlist') return Object.freeze({kind: 'saved-destination', destination: {kind: 'playlist', id: entry.id}} as const);
  const browse = entry.navigation?.browse;
  if (entry.kind === 'category' && entry.libraryId && entry.navigation?.view === 'releases' && browse?.pivot === 'albums') {
    return Object.freeze({kind: 'shell', destination: {kind: 'library', libraryId: entry.libraryId, tab: 'releases',
      definition: Object.freeze({libraryId: entry.libraryId, pivot: browse.pivot, query: browse.query ?? null, sort: browse.sort, presentation: 'grid'})}} as const);
  }
  return entry.kind === 'album' || entry.kind === 'artist' ? Object.freeze({kind: 'music', entry})
    : entry.kind === 'book' ? Object.freeze({kind: 'book', entry})
      : entry.kind === 'author' || entry.kind === 'book_series' ? Object.freeze({kind: 'audiobook', entry}) : titleRoute(entry, continueOffer(entry));
};
export const channelRoute = (item: HomeChannel): NativeShellRoute => Object.freeze({kind: 'channel', sourceId: item.source.id, channelId: item.channel.id});

export type NativeRouteSnapshot = Readonly<{authority: ShellAuthority; route: NativeShellRoute; destination: ShellDestination; revision: number}>;
/** One actual opening in this authority's history, including root/current. The
 * occurrence is presentation identity, never a route admission or command. */
export type NativeRoutePresentation = Readonly<{occurrence: string; route: NativeShellRoute; destination: ShellDestination}>;
/** Resource lifetime for one already-admitted occurrence. It is not command
 * authority: commands still capture their own original snapshot/revision. */
export type NativePresentationBinding = Readonly<{
  original: NativeRoutePresentation;
  retainedCurrent: () => boolean;
  activeCurrent: () => boolean;
  subscribe: (listener: () => void) => () => void;
  dispose: () => void;
}>;
export type NativeDestinationIntent = 'enter' | 'restore' | 'top';
export type NativeDestinationActivation = Readonly<{visible: boolean; intent: NativeDestinationIntent; revision: number}>;
export type NativeDestinationActivationPort = Readonly<{subscribe: (listener: () => void) => () => void; getSnapshot: () => NativeDestinationActivation}>;
export type NativeRouteAdmission = (route: NativeShellRoute, origin: ShellDestination) => boolean;
/** One tab's own stack, root first. Only touch navigation keeps more than one. */
export type NativeTabStack = Readonly<{tab: string; presentations: readonly NativeRoutePresentation[]}>;
export type NativeTabStacks = Readonly<{active: string; stacks: readonly NativeTabStack[]}>;
export type NativeShellNavigationOptions = Readonly<{
  /** motion.html 2.11: each tab keeps its own stack (touch). Off, history is one
   * linear list, which television's rail ladder relies on. */
  tabStacks?: boolean;
}>;
/** Settings and every page opened from it are a modal over the tab; they never park with it. */
export const isModalRoot = (route: NativeShellRoute): boolean => route.kind === 'shell' && route.destination.kind === 'settings';
const sameJson = (a: unknown, b: unknown) => JSON.stringify(a) === JSON.stringify(b);
const sameDestination = (a: ShellDestination, b: ShellDestination): boolean => a.kind === b.kind &&
  (a.kind !== 'library' || b.kind === 'library' && a.libraryId === b.libraryId);
const noPresentations: readonly NativeRoutePresentation[] = Object.freeze([]);
let navigationSequence = 0;
const navigationIdentity = () => {
  if (navigationSequence >= Number.MAX_SAFE_INTEGER) throw new Error('Navigation identity exhausted');
  return `navigation-${++navigationSequence}`;
};

/** Read real navigation intent without taking ownership of screen focus or scroll memory. */
export function nativeDestinationActivation(navigation: NativeShellNavigation, destination: ShellDestination): NativeDestinationActivationPort {
  const target = Object.freeze({...destination});
  let previous: NativeRouteSnapshot | undefined, previousCurrent: boolean | undefined, value: NativeDestinationActivation;
  return Object.freeze({subscribe: navigation.subscribe, getSnapshot: (): NativeDestinationActivation => {
    const state = navigation.getSnapshot(), current = navigation.isCurrent();
    if (state !== previous || current !== previousCurrent) {
      previous = state; previousCurrent = current;
      value = Object.freeze({visible: current && state.route.kind === 'shell' && sameDestination(state.route.destination, target),
        intent: navigation.getIntent(), revision: state.revision});
    }
    return value;
  }});
}

/** Highlight the title's actual authorized library while retaining its mounted origin for Back. */
export function nativeRailDestination(snapshot: NativeRouteSnapshot, libraries: readonly Readonly<{id: string}>[]): ShellDestination {
  const route = snapshot.route;
  const libraryId = route.kind === 'title' || route.kind === 'music' || route.kind === 'book' || route.kind === 'audiobook' ? route.entry.libraryId
    : route.kind === 'read-book' || route.kind === 'read-series' || route.kind === 'read-periodical' ? route.libraryId : undefined;
  return libraryId && libraries.some(library => library.id === libraryId) ? {kind: 'library', libraryId} : snapshot.destination;
}

/** One authority lifetime and one history. Async callers must retain the authority they read. */
export class NativeShellNavigation {
  private snapshot: NativeRouteSnapshot;
  private history: NativeRoutePresentation[] = [];
  private retained: readonly NativeShellRoute[] = Object.freeze([]);
  private presentations: readonly NativeRoutePresentation[];
  private readonly identity = navigationIdentity();
  private openingSequence = 0;
  private occurrence = this.nextOccurrence();
  private nativePopClaim?: object;
  private listeners = new Set<() => void>();
  private alive = true;
  private intent: NativeDestinationIntent = 'enter';
  readonly currentAuthority: () => ShellAuthority;
  private readonly admission?: NativeRouteAdmission;
  private readonly tabbed: boolean;
  /** The tab whose stack is `history` + current; every other visited tab is parked whole. */
  private tab = 'home';
  private parked = new Map<string, readonly NativeRoutePresentation[]>();
  private tabOrder: string[] = ['home'];
  private tabSnapshot: NativeTabStacks;
  constructor(authority: ShellAuthority, currentAuthority: () => ShellAuthority, admission?: NativeRouteAdmission, options?: NativeShellNavigationOptions) {
    this.currentAuthority = currentAuthority;
    this.admission = admission;
    this.tabbed = options?.tabStacks === true;
    const destination: ShellDestination = {kind: 'home'};
    const route: NativeShellRoute = {kind: 'shell', destination};
    this.snapshot = Object.freeze({authority: Object.freeze({...authority}), route, destination, revision: 0});
    this.presentations = Object.freeze([Object.freeze({occurrence: this.occurrence, route, destination})]);
    this.tabSnapshot = Object.freeze({active: this.tab, stacks: Object.freeze([Object.freeze({tab: this.tab, presentations: this.presentations})])});
  }
  getSnapshot = (): NativeRouteSnapshot => this.snapshot;
  /** Exact route descriptors still in this authority's Back stack. */
  retainedRoutes = (): readonly NativeShellRoute[] => this.isCurrent() ? this.retained : [];
  /** Stable readonly projection of real admitted history: parked tab stacks
   * first, then the current tab's, so `.at(-1)` is always the page on screen.
   * No extra root, by-item dedupe or renderer is manufactured here. */
  presentationStack = (): readonly NativeRoutePresentation[] => {
    try {return this.isCurrent() ? this.presentations : noPresentations;} catch {return noPresentations;}
  };
  /** Bind only an exact owner-issued record that is still admitted. A later
   * occurrence, equal descriptor or restored authority cannot revive the lease.
   * The supplied authority subscription is the original bootstrap's live port. */
  bindPresentation = (original: NativeRoutePresentation, authority: Readonly<{
    current: () => boolean; subscribe: (listener: () => void) => () => void;
  }>): NativePresentationBinding | undefined => {
    const expectedAuthority=this.snapshot.authority;
    const owns=()=>this.isCurrent()&&sameAuthority(expectedAuthority,this.snapshot.authority)&&authority.current();
    try {if(!this.presentations.includes(original)||!owns()||!this.allows(original.route,original.destination)
      ||!owns()||!this.presentations.includes(original))return undefined;}catch{return undefined;}
    let retired=false,checking=false,previousActive=this.presentations.at(-1)?.occurrence===original.occurrence;
    const registration={},listeners=new Set<()=>void>();let ownedRegistration:object|undefined=registration;
    let offNavigation=()=>{},offAuthority=()=>{};
    const registered=()=>!retired&&ownedRegistration===registration;
    const emit=()=>{for(const listener of [...listeners]){try{listener();}catch{/* A sink never grants resource ownership. */}}};
    const retire=()=>{
      if(!registered())return;
      retired=true;ownedRegistration=undefined;
      offNavigation();offAuthority();
      const sinks=[...listeners];listeners.clear();
      for(const listener of sinks){try{listener();}catch{/* Finish once-only original-lease retirement. */}}
    };
    const retainedCurrent=()=>{
      if(!registered()||checking)return false;
      checking=true;
      try {
        const retained=()=>registered()&&this.presentations.some(value=>value.occurrence===original.occurrence&&value.route===original.route);
        // Each external predicate may reenter, dispose this binding or replace
        // authority/history. Recheck the original registration after each one.
        if(!this.isCurrent()||!registered()||!sameAuthority(expectedAuthority,this.snapshot.authority)
          ||!authority.current()||!registered()||!retained()
          ||!this.allows(original.route,original.destination)||!registered()
          ||!this.isCurrent()||!registered()||!authority.current()||!registered()||!retained()){
          retire();return false;
        }
        return registered();
      }catch{retire();return false;}
      finally{checking=false;}
    };
    const activeCurrent=()=>retainedCurrent()&&registered()&&this.presentations.at(-1)?.occurrence===original.occurrence
      &&this.presentations.at(-1)?.route===original.route;
    const notify=()=>{
      const active=activeCurrent();if(!registered())return;
      if(active!==previousActive){previousActive=active;emit();}
    };
    offNavigation=this.subscribe(notify);offAuthority=authority.subscribe(notify);
    if(!retainedCurrent()){offNavigation();offAuthority();return undefined;}
    return Object.freeze({original,retainedCurrent,activeCurrent,
      subscribe:(listener:()=>void)=>{if(!registered())return()=>{};listeners.add(listener);return()=>{listeners.delete(listener);};},
      dispose:retire,
    });
  };

  /** Every visited tab's stack in first-visit order (one linear stack when untabbed). */
  tabStacks = (): NativeTabStacks => this.isCurrent() ? this.tabSnapshot : Object.freeze({active: this.tab, stacks: Object.freeze([])});
  getIntent = (): NativeDestinationIntent => this.intent;
  isCurrent = (): boolean => this.current(this.snapshot.authority);
  allows = (route: NativeShellRoute, origin: ShellDestination = route.kind === 'shell' ? route.destination : this.snapshot.destination): boolean =>
    this.isCurrent() && (this.admission?.(route, origin) ?? true);
  subscribe = (listener: () => void): (() => void) => {this.listeners.add(listener); return () => {this.listeners.delete(listener);};};
  private current(authority: ShellAuthority): boolean {
    return this.alive && sameAuthority(authority, this.snapshot.authority) && sameAuthority(authority, this.currentAuthority());
  }
  open(route: NativeShellRoute, expectedAuthority: ShellAuthority): boolean {
    const expected = this.snapshot, previous = this.presentations.at(-1);
    const owns = () => this.alive && this.snapshot === expected && this.presentations.at(-1) === previous;
    if (!previous || !owns() || !this.current(expectedAuthority) || !owns() || !this.allows(route)
      || !owns() || !this.current(expectedAuthority) || !owns()) return false;
    const tab = this.tabbed && route.kind === 'shell' ? selectedTab(route.destination) : undefined;
    if (tab && tab !== this.tab) {this.switchTab(tab, route, false); return true;}
    if (tab && route.kind === 'shell') {
      // Same tab: a page already lower in this stack is returned to, never stacked twice;
      // switching library at the tab's root replaces it in place (2.11: no slide).
      const lower = this.history.findIndex(entry => entry.route.kind === 'shell' && sameJson(entry.route.destination, route.destination));
      if (lower >= 0) {
        const entry = this.history[lower]!;
        this.history = this.history.slice(0, lower);
        this.publish(entry.route, 'restore', entry.destination, entry.occurrence);
        return true;
      }
      // (Libraries then a library is a push, 2.9; going up from a first-screen library is not.)
      if (!this.history.length && previous.route.kind === 'shell' && previous.route.destination.kind === 'library'
        && (route.destination.kind === 'library' || route.destination.kind === 'libraries')) {
        this.publish(route, 'enter', undefined, this.nextOccurrence());
        return true;
      }
    }
    const occurrence = this.nextOccurrence();
    this.history.push(previous);
    this.publish(route, 'enter', undefined, occurrence);
    return true;
  }
  back(expectedAuthority: ShellAuthority): boolean {
    if (!sameAuthority(expectedAuthority, this.snapshot.authority)) return false;
    // At another tab's first screen, Back returns to Home as it was left (Android convention).
    if (this.tabbed && !this.history.length) {
      if (this.tab === 'home' || !this.current(expectedAuthority)) return false;
      const home: NativeShellRoute = Object.freeze({kind: 'shell', destination: Object.freeze({kind: 'home'})});
      if (!this.allows(home)) return false;
      this.switchTab('home', home, true); return true;
    }
    return this.commitNativePop(this.snapshot, this.occurrence);
  }
  /** A tab bar or rail selection. Another tab comes back exactly as it was left
   * (`anyRoot`, or the same first screen); the same tab returns to its first screen. */
  selectTab(destination: ShellDestination, expectedAuthority: ShellAuthority, anyRoot = false): boolean {
    const route: NativeShellRoute = Object.freeze({kind: 'shell', destination});
    const tab = this.tabbed ? selectedTab(destination) : undefined;
    if (!tab) return this.open(route, expectedAuthority);
    if (!this.alive || !this.current(expectedAuthority) || !this.allows(route)) return false;
    if (tab !== this.tab) {
      const parked = this.parked.get(tab)?.[0];
      this.switchTab(tab, route, !!parked && (anyRoot || sameJson(parked.destination, destination)));
      return true;
    }
    const root = this.history[0] ?? this.presentations.at(-1)!;
    if (anyRoot || sameJson(root.destination, destination)) return this.popToRoot(expectedAuthority) || true;
    this.history = [];
    this.publish(route, 'enter', undefined, this.nextOccurrence());
    return true;
  }
  /** Re-selecting the tab you're on: back to its first screen. False when already there. */
  popToRoot(expectedAuthority: ShellAuthority): boolean {
    if (!this.alive || !this.current(expectedAuthority) || !this.history.length) return false;
    const root = this.history[0]!;
    if (!this.allows(root.route, root.destination)) return false;
    this.history = [];
    this.publish(root.route, 'restore', root.destination, root.occurrence);
    return true;
  }
  private switchTab(tab: string, route: NativeShellRoute, restore: boolean): void {
    const leaving = [...this.history, this.presentations.at(-1)!];
    const modal = leaving.findIndex(entry => isModalRoot(entry.route));
    this.parked.set(this.tab, Object.freeze(modal > 0 ? leaving.slice(0, modal) : leaving));
    const target = this.parked.get(tab);
    this.parked.delete(tab);
    this.tab = tab;
    if (!this.tabOrder.includes(tab)) this.tabOrder.push(tab);
    if (restore && target?.length) {
      const top = target.at(-1)!;
      this.history = target.slice(0, -1);
      this.publish(top.route, 'restore', top.destination, top.occurrence);
    } else {
      this.history = [];
      this.publish(route, 'enter', undefined, this.nextOccurrence());
    }
  }
  /** The native presenter captures both values when Back/gesture begins.
   * Cancellation calls nothing. Rejecting this compare-pop never uses latest. */
  commitNativePop = (expected: NativeRouteSnapshot, expectedOccurrence: string): boolean => {
    if (this.nativePopClaim || this.snapshot !== expected || this.occurrence !== expectedOccurrence) return false;
    const revision = expected.revision, entry = this.history.at(-1), claim = {};
    if (!entry) return false;
    this.nativePopClaim = claim;
    let committed = false;
    const owns = () => this.nativePopClaim === claim && this.alive && this.snapshot === expected
      && this.snapshot.revision === revision && this.occurrence === expectedOccurrence && this.history.at(-1) === entry;
    try {
      if (!owns() || !this.current(expected.authority) || !owns()) return false;
      if (this.admission && this.admission(entry.route, entry.destination) !== true) return false;
      // Admission/current-authority callbacks can reenter, navigate or retire.
      // Only this original snapshot/history pair may be popped afterwards.
      if (!owns() || !this.current(expected.authority) || !owns()) return false;
      this.history.pop();
      committed = true;
      this.publish(entry.route, 'restore', entry.destination, entry.occurrence);
      return true;
    } catch {return committed;}
    finally {if (this.nativePopClaim === claim) this.nativePopClaim = undefined;}
  }
  /** A zap replaces only the exact channel opening that advertised it. */
  replaceChannel(expected: NativeRouteSnapshot, route: Extract<NativeShellRoute, {kind: 'channel'}>, expectedAuthority: ShellAuthority): boolean {
    if (!this.current(expectedAuthority) || this.snapshot !== expected || expected.route.kind !== 'channel' || !this.allows(route)
      || !this.current(expectedAuthority) || !this.alive || this.snapshot !== expected) return false;
    this.publish(route, 'enter'); return true;
  }
  top(destination: ShellDestination, expectedAuthority: ShellAuthority): boolean {
    const expected = this.snapshot;
    if (!this.current(expectedAuthority) || this.snapshot !== expected || expected.route.kind !== 'shell' || !sameDestination(destination, expected.route.destination) || !this.allows(expected.route)
      || !this.current(expectedAuthority) || !this.alive || this.snapshot !== expected) return false;
    this.publish(expected.route, 'top'); return true;
  }
  private nextOccurrence(): string {
    if (this.openingSequence >= Number.MAX_SAFE_INTEGER) throw new Error('Navigation occurrence exhausted');
    return `${this.identity}:${++this.openingSequence}`;
  }
  private publish(route: NativeShellRoute, intent: NativeDestinationIntent, restoredDestination?: ShellDestination, occurrence = this.occurrence): void {
    this.intent = intent;
    this.occurrence = occurrence;
    const parked = this.tabOrder.flatMap(tab => tab === this.tab ? [] : this.parked.get(tab) ?? []);
    this.retained = Object.freeze([...parked, ...this.history].map(entry => entry.route));
    this.snapshot = Object.freeze({...this.snapshot, route, destination: restoredDestination ?? (route.kind === 'shell' ? route.destination : this.snapshot.destination), revision: this.snapshot.revision + 1});
    const top = Object.freeze({occurrence, route, destination: this.snapshot.destination}), stack = Object.freeze([...this.history, top]);
    this.presentations = Object.freeze([...parked, ...stack]);
    this.tabSnapshot = Object.freeze({active: this.tab, stacks: Object.freeze(this.tabOrder.flatMap(tab => {
      const presentations = tab === this.tab ? stack : this.parked.get(tab);
      return presentations ? [Object.freeze({tab, presentations})] : [];
    }))});
    for (const listener of [...this.listeners]) listener();
  }
  dispose(): void {
    if (!this.alive) return;
    this.alive = false; this.history = []; this.parked.clear(); this.retained = Object.freeze([]); this.presentations = noPresentations;
    for (const listener of [...this.listeners]) listener();
    this.listeners.clear();
  }
}
