/**
 * Open screens that stay current, quietly. One hub per signed-in viewer on one server.
 *
 * The server pushes small hints on the device's one event feed: "this library's content moved",
 * "this profile's watched state moved", "the list of libraries moved". The hub turns those into
 * named dependencies with a local version each. A document on screen (Home, a library tab, a
 * title, a list, search results) declares the dependencies it was built from and how to read
 * itself again without disturbing what is shown. The hub then does the rest:
 *
 * - A document is read again only when one of its dependencies moved, never on a timer and never
 *   because the person came back to it. A document that is not on screen only remembers that it
 *   is out of date and catches up when it is shown again.
 * - Hints that arrive together are answered by one read. Progress from another device moves
 *   often, so a document that only depends on it for a resume bar is read at a slower pace.
 * - A read waits for a pause in the person's input, so a list is not rearranged under a press.
 * - A failed read keeps what is on screen and is tried again, a little later each time. Nothing
 *   is said until the server has been out of reach for a while; then one quiet state is raised,
 *   and it is taken down by the first read or event that succeeds.
 * - While the app is in the background nothing runs. On return, documents whose dependencies
 *   moved in the meantime (the feed replays what was missed) are read once.
 *
 * No React, no DOM, no timers of its own beyond the two injected: tested under Node.
 */
import type {EventsClient} from './playback-v1/events.ts';
import type {ServerEvent} from './playback-v1/types.ts';
import {parseFreshnessChange} from './freshness.ts';
import {FreshnessHistory} from './freshness-memory.ts';
import {Interest, type FreshnessInterest, type FreshnessInterestOptions} from './freshness-interest.ts';
import {FreshnessSharedReads} from './freshness-shared.ts';
export type {InterestOutcome, InterestReadContext, FreshnessInterest, FreshnessInterestOptions} from './freshness-interest.ts';
export {isSharedReadSuperseded} from './freshness-shared.ts';

/** A named thing a document was built from. Build them with `dependsOn`. */
export type FreshnessDependency = string;

const ALL = '*';
/** The dependencies the server reports. A library id narrows one to that library; none means every library. */
export const dependsOn = Object.freeze({
  /** The list of libraries this viewer may see (added, removed, renamed). */
  libraries: 'directory' as FreshnessDependency,
  /** Titles added, changed, removed, gone missing or restored. */
  content: (libraryId: string = ALL): FreshnessDependency => `content:${libraryId}`,
  /** The order of a library's lists (a title moved under another sort key). */
  order: (libraryId: string = ALL): FreshnessDependency => `order:${libraryId}`,
  /** This profile's watched ticks, watchlist, favourites, ratings, history and Continue Watching membership. */
  personal: (libraryId: string = ALL): FreshnessDependency => `personal:${libraryId}`,
  /** This profile's playback position (a resume bar moving on another device). */
  progress: (libraryId: string = ALL): FreshnessDependency => `progress:${libraryId}`,
  /**
   * This profile's state of one title (or anything under it), for a page about that title: it
   * moves when a hint names the title, or when a hint for its library names none.
   */
  personalTitle: (libraryId: string, titleId: string): readonly FreshnessDependency[] => [`personal:title:${titleId}`, `personal:any:${libraryId}`],
  /** The same for a playback position. */
  progressTitle: (libraryId: string, titleId: string): readonly FreshnessDependency[] => [`progress:title:${titleId}`, `progress:any:${libraryId}`],
  /** A settings document: the server's, or this profile's preferences. */
  settings: (document: 'server' | 'preferences'): FreshnessDependency => `settings:${document}`,
  /**
   * Something the server sends no hint for (the guide, recordings, downloads being prepared on
   * another device): the device's own clock stands in, and moves this when it is time to look again.
   * A stand-in only. Where the feed has a hint, depend on the hint.
   */
  clock: (name: string): FreshnessDependency => `clock:${name}`,
  /** Any other event type on the feed (`notification.changed`, `library.scan.updated`), optionally for one resource. */
  event: (type: string, resourceId?: string): FreshnessDependency => (resourceId ? `event:${type}:${resourceId}` : `event:${type}`),
  /** One of the lists the server sends a `list.changed` hint for (keys, members, sources...). */
  list: (name: ServerList): FreshnessDependency => `event:list.changed:${name}`,
});

/**
 * The lists the server hints about. The owner and administrators hear every one but
 * `accessTokens` and `togetherGroups`, which are a profile's own and heard by that profile.
 */
export type ServerList =
  | 'apiKeys' | 'accessTokens' | 'webhooks' | 'members' | 'invitations' | 'devices'
  | 'clientReports' | 'feedback' | 'downloads' | 'remoteSources' | 'liveSources' | 'tuners'
  | 'recordingGroups' | 'recordingStorage' | 'libraryChannels' | 'playbackProblems'
  /** The Watch Together rooms this profile is in (heard by the profile). */
  | 'togetherGroups'
  /** This profile's own recordings and rules, and its own downloads (heard by the profile). */
  | 'myRecordings' | 'myDownloads'
  /** The TV guide was brought in again, or a source turned on or off (heard by everyone). */
  | 'guide'
  /** Home's Trending and Popular rows may have moved (heard by everyone). */
  | 'recommendations';

/**
 * Everything a page about one title depends on: its library's content, and this profile's state of
 * that title. `titleIds` are the ids the page shows (the title, and the show of an episode when
 * known); a personal hint about another title of the library does not move it.
 */
export function titleWithPersonalState(libraryId: string, titleIds: readonly string[]): readonly FreshnessDependency[] {
  const out: FreshnessDependency[] = [dependsOn.content(libraryId)];
  for (const id of new Set(titleIds.filter(Boolean))) out.push(...dependsOn.personalTitle(libraryId, id), ...dependsOn.progressTitle(libraryId, id));
  if (out.length === 1) out.push(dependsOn.personal(libraryId), dependsOn.progress(libraryId));
  return [...new Set(out)];
}

/**
 * Whether a list's filters or sort read this profile's state (played, favourites, the watchlist,
 * the viewer's rating, last played): then any personal change can change which titles it holds or
 * their order, not only the marks on the titles it shows.
 */
export function readsPersonalState(fields: readonly (string | undefined)[]): boolean {
  return fields.some(f => !!f && /^(playState|played|watched|unwatched|inProgress|progress|favorite|watchlist|personalRating|userRating|lastPlayed|playCount|history)/.test(f));
}

/**
 * A list of titles (a library tab, a container's titles, a person's titles): it moves when its
 * library's titles or their order change, and for this profile's state only when a hint names one
 * of the titles it holds now (`titleIds`, read each time a hint arrives), or names none. A list
 * whose query reads personal state (`readsPersonal`) moves for every personal change, as before.
 */
export function listedTitlesWithPersonalState(libraryId: string | undefined, titleIds: Iterable<string>, readsPersonal: boolean): readonly FreshnessDependency[] {
  if (readsPersonal) return titlesWithPersonalState(libraryId);
  const library = libraryId ?? ALL;
  const out: FreshnessDependency[] = [dependsOn.content(libraryId), dependsOn.order(libraryId), `personal:any:${library}`, `progress:any:${library}`];
  for (const id of titleIds) if (id) out.push(`personal:title:${id}`, `progress:title:${id}`);
  return out;
}

/** The ids a personal hint may name for these entries: the entry's own, the title its page is about, and what it plays. */
export function titleIdsOf(entries: readonly Readonly<{id: string; navigation?: Readonly<{entityId?: string}>; playback?: Readonly<{itemId?: string}>}>[]): string[] {
  const out: string[] = [];
  for (const e of entries) {
    out.push(e.id);
    if (e.navigation?.entityId && e.navigation.entityId !== e.id) out.push(e.navigation.entityId);
    if (e.playback?.itemId && e.playback.itemId !== e.id) out.push(e.playback.itemId);
  }
  return out;
}

/** Everything a screen of titles with personal marks depends on, for one library or all of them. */
export function titlesWithPersonalState(libraryId?: string): readonly FreshnessDependency[] {
  return [dependsOn.content(libraryId), dependsOn.order(libraryId), dependsOn.personal(libraryId), dependsOn.progress(libraryId)];
}

export type FreshnessTimers = Readonly<{
  setTimer(fn: () => void, ms: number): unknown;
  clearTimer(timer: unknown): void;
  now(): number;
  random(): number;
}>;

export type FreshnessHubOptions = Partial<FreshnessTimers> & Readonly<{
  /** How long hints are gathered before one read answers them. */
  gatherMs?: number;
  /** The same, for a document only its progress dependencies moved for. */
  progressGatherMs?: number;
  /** The least time between two reads of one document that only progress asked for. */
  progressEveryMs?: number;
  /** The least time between two reads of one open document that content asked for. A library
   * being scanned moves several times a second; a screen showing it reads again at this pace,
   * not on every hint. A change after a quiet spell is still read after `gatherMs`. */
  contentEveryMs?: number;
  /** How long input must have paused before a read may change what is on screen. */
  settleMs?: number;
  /** The longest a read waits for that pause. */
  settleLimitMs?: number;
  /** Waits between attempts after a failed read (the last repeats). */
  backoffMs?: readonly number[];
  /** How long the server must have been out of reach before anything is said. */
  sustainedMs?: number;
}>;

export type Reachability = 'reachable' | 'unreachable';

/** What a document's quiet read is given. */
export type RevalidateContext = Readonly<{
  signal: AbortSignal;
  /** @internal This owner already holds the feed's read admission. */
  admission?: object;
  /** Resolves when the person's input has paused. Await it between receiving an answer and showing it. */
  settle(): Promise<void>;
  /** Only progress moved: a read may keep to the parts that show a position. */
  progressOnly: boolean;
}>;

export type FreshDocumentOptions = Readonly<{
  /** What the document was built from. A function is asked again before every check, for documents whose libraries are known only once loaded. */
  dependencies: readonly FreshnessDependency[] | (() => readonly FreshnessDependency[]);
  /** Read again and show the answer in place. Rejects when the read failed; what is shown must be left as it was. */
  revalidate(context: RevalidateContext): Promise<void>;
  /** Whether there is something on screen to keep. A document with nothing yet is left to its own first load, unless `recover` is given. */
  hasContent?(): boolean;
  /** A first load that failed with nothing to show: try it again. Asked for with `invalidate()`, repeated on the same waits as a failed quiet read. */
  recover?(context: RevalidateContext): Promise<void>;
}>;

export type FreshDocumentState = Readonly<{
  /** A dependency moved and the read that answers it has not finished. */
  stale: boolean;
  /** A read is in flight. */
  revalidating: boolean;
  /** Reads in a row that failed. */
  failures: number;
}>;

export interface FreshDocument {
  /** The document is on screen (true) or covered, backgrounded or closed (false). Reads only run while open. */
  setOpen(open: boolean): void;
  /** What is shown was read just now by the screen itself: forget earlier hints. Call when that read starts. */
  markCurrent(): void;
  /** Treat the document as out of date (an action here changed it, or its screen knows better). */
  invalidate(): void;
  /** Content has just arrived on screen: run a read that was owed while there was nothing to keep. */
  check(): void;
  getState(): FreshDocumentState;
  subscribe(listener: () => void): () => void;
  dispose(): void;
}

type Urgency = 0 | 1 | 2; // current, progress moved, content moved

/** The server answered, and asking again will not change the answer (not allowed, gone, refused): not a failure to try again. */
export function finalAnswer(error: unknown): boolean {
  const e = error as {status?: unknown; retryable?: unknown; code?: unknown} | null;
  if (e?.retryable === true) return false;
  if (e?.retryable === false) return true;
  const status = typeof e?.status === 'number' ? e.status : 0;
  // The server answered and the answer will not change by asking again: the screen's own state says so.
  return status >= 400 && status < 500 && status !== 408 && status !== 425 && status !== 429;
}

function serverWait(error: unknown): number {
  const e = error as {retryAfterMs?: unknown; retryAfterSeconds?: unknown} | null;
  const ms = typeof e?.retryAfterMs === 'number' ? e.retryAfterMs : typeof e?.retryAfterSeconds === 'number' ? e.retryAfterSeconds * 1000 : 0;
  return Number.isFinite(ms) && ms > 0 ? Math.min(ms, 10 * 60 * 1000) : 0;
}

class Document implements FreshDocument {
  private urgency: Urgency = 0;
  /** What the read in flight is answering, so a read that is stopped is still owed. */
  private answering: Urgency = 0;
  private open = false;
  private running?: AbortController;
  private timer: unknown;
  private timerAt = 0;
  private failures = 0;
  private lastProgressRead = -Infinity;
  private lastContentRead = -Infinity;
  private disposed = false;
  private listeners = new Set<() => void>();
  private state: FreshDocumentState = Object.freeze({stale: false, revalidating: false, failures: 0});
  /** Reports this document's reads to the hub's reachability under its own name. */
  readonly source: object = {};

  private readonly hub: FreshnessHub;
  private readonly options: FreshDocumentOptions;
  constructor(hub: FreshnessHub, options: FreshDocumentOptions) { this.hub = hub; this.options = options; }

  dependencies(): readonly FreshnessDependency[] {
    const d = this.options.dependencies;
    try { return typeof d === 'function' ? d() : d; } catch { return []; }
  }

  getState = (): FreshDocumentState => this.state;
  subscribe = (listener: () => void): (() => void) => { this.listeners.add(listener); return () => { this.listeners.delete(listener); }; };
  private publish(): void {
    const next = {stale: this.urgency > 0, revalidating: !!this.running, failures: this.failures};
    if (next.stale === this.state.stale && next.revalidating === this.state.revalidating && next.failures === this.state.failures) return;
    this.state = Object.freeze(next);
    for (const listener of [...this.listeners]) listener();
  }

  /** A dependency moved. `progress` says only a playback position did. */
  moved(progress: boolean): void {
    if (this.disposed) return;
    const urgency: Urgency = progress ? 1 : 2;
    if (urgency > this.urgency) this.urgency = urgency;
    this.publish();
    this.plan();
  }

  setOpen(open: boolean): void {
    if (this.disposed || this.open === open) return;
    this.open = open;
    if (open) this.plan(); else this.pause();
  }

  markCurrent(): void {
    if (this.disposed) return;
    this.urgency = 0; this.answering = 0; this.failures = 0;
    this.cancelTimer();
    this.running?.abort(); this.running = undefined;
    this.hub.report(this.source, true);
    this.publish();
  }

  invalidate(): void { this.moved(false); }

  check(): void { this.plan(); }

  /** The hub went to the background or the screen was covered: stop, and keep remembering that a read is owed. */
  pause(): void {
    this.cancelTimer();
    if (!this.running) return;
    this.running.abort(); this.running = undefined;
    if (this.answering > this.urgency) this.urgency = this.answering;
    this.answering = 0;
    this.publish();
  }

  dispose(): void {
    if (this.disposed) return;
    this.pause();
    this.disposed = true;
    this.hub.report(this.source, true);
    this.hub.forget(this);
    this.listeners.clear();
  }

  private cancelTimer(): void {
    if (this.timer !== undefined) { this.hub.timers.clearTimer(this.timer); this.timer = undefined; }
  }

  private plan(delay?: number): void {
    if (this.disposed || !this.urgency || !this.open || !this.hub.isActive() || this.running) return;
    // Nothing shown and no way to fill it from here: the screen's own first load does that, then asks to be checked.
    if (!this.hasContent() && !this.options.recover) return;
    const o = this.hub.tuning;
    let wait = delay ?? (this.urgency === 1 ? o.progressGatherMs : o.gatherMs);
    if (delay === undefined && this.urgency === 1) wait = Math.max(wait, this.lastProgressRead + o.progressEveryMs - this.hub.timers.now());
    if (delay === undefined && this.urgency === 2) wait = Math.max(wait, this.lastContentRead + o.contentEveryMs - this.hub.timers.now());
    // Clients told the same thing at the same moment do not all ask at once.
    wait = Math.round(wait * (1 + this.hub.timers.random() * 0.25));
    const at = this.hub.timers.now() + wait;
    if (this.timer !== undefined) {
      // An earlier plan stands, and a wait after a failure is never cut short by a new hint.
      if (this.timerAt <= at || this.failures) return;
      this.cancelTimer();
    }
    this.timerAt = at;
    this.timer = this.hub.timers.setTimer(() => { this.timer = undefined; void this.run(); }, Math.max(0, wait));
  }

  private hasContent(): boolean {
    try { return this.options.hasContent ? this.options.hasContent() : true; } catch { return false; }
  }

  private async run(): Promise<void> {
    if (this.disposed || !this.urgency || !this.open || !this.hub.isActive() || this.running) return;
    const filled = this.hasContent();
    if (!filled && !this.options.recover) return;
    const controller = new AbortController();
    this.running = controller;
    const answered = this.answering = this.urgency;
    this.urgency = 0;
    this.publish();
    let failed: unknown, ok = false;
    try {
      const context: RevalidateContext = {signal: controller.signal, admission: this.hub.readAdmission(), settle: () => this.hub.settled(controller.signal), progressOnly: answered === 1};
      if (filled) await context.settle();
      if (!controller.signal.aborted) {
        await this.hub.read(controller.signal, () => filled ? this.options.revalidate(context) : this.options.recover!(context));
        ok = true;
      }
    } catch (error) {
      failed = error;
    }
    // Stopped part-way (covered, backgrounded, or the screen read it itself): whoever stopped it settled what is owed.
    if (this.disposed || this.running !== controller) return;
    this.running = undefined; this.answering = 0;
    if (controller.signal.aborted) { if (answered > this.urgency) this.urgency = answered; this.publish(); return; }
    if (ok && !filled && !this.hasContent()) {
      // The screen's first load is still on its way: what moved is still owed, and is read once there is something to keep (`check`).
      if (answered > this.urgency) this.urgency = answered;
      this.publish();
      return;
    }
    if (ok || finalAnswer(failed)) {
      // A refusal the server will repeat (gone, not allowed) is an answer too: the screen's own state says so.
      this.failures = 0;
      if (answered === 1) this.lastProgressRead = this.hub.timers.now();
      else if (filled) this.lastContentRead = this.hub.timers.now();
      this.hub.report(this.source, true);
      this.publish();
      this.plan();
      return;
    }
    this.failures++;
    if (answered > this.urgency) this.urgency = answered;
    this.hub.report(this.source, false);
    this.publish();
    const steps = this.hub.tuning.backoffMs;
    this.plan(Math.max(steps[Math.min(this.failures - 1, steps.length - 1)] ?? 30_000, serverWait(failed)));
  }
}

type Tuning = Required<Pick<FreshnessHubOptions, 'gatherMs' | 'progressGatherMs' | 'progressEveryMs' | 'contentEveryMs' | 'settleMs' | 'settleLimitMs' | 'backoffMs' | 'sustainedMs'>>;

export class FreshnessHub {
  readonly timers: FreshnessTimers;
  readonly tuning: Tuning;
  private history = new FreshnessHistory();
  private versionFloor = 0;
  private highestVersion = 0;
  /** @internal Fixed guide/preferences cohorts for the opt-in interest scheduler. */
  readonly sharedReads = new FreshnessSharedReads(() => this.timers.now(), () => this.tuning.gatherMs);
  private interests = new Set<Interest>();
  private documents = new Set<Document>();
  private watchers = new Set<{dependencies: ReadonlySet<FreshnessDependency>; listener: () => void}>();
  private events: EventsClient | null = null;
  private detach: (() => void) | undefined;
  private active = true;
  private lastInput = -Infinity;
  private settleWaiters = new Set<() => void>();
  private settleTimer: unknown;
  private failing = new Map<object, number>();
  private reachability: Reachability = 'reachable';
  private reachTimer: unknown;
  private reachListeners = new Set<() => void>();
  private disposed = false;
  private readonly feedSource: object = {};

  constructor(options: FreshnessHubOptions = {}) {
    this.timers = Object.freeze({
      setTimer: options.setTimer ?? ((fn, ms) => setTimeout(fn, ms)),
      clearTimer: options.clearTimer ?? (timer => clearTimeout(timer as ReturnType<typeof setTimeout>)),
      now: options.now ?? (() => Date.now()),
      random: options.random ?? Math.random,
    });
    this.tuning = Object.freeze({
      gatherMs: options.gatherMs ?? 400,
      progressGatherMs: options.progressGatherMs ?? 3000,
      progressEveryMs: options.progressEveryMs ?? 15_000,
      contentEveryMs: options.contentEveryMs ?? 5_000,
      settleMs: options.settleMs ?? 350,
      settleLimitMs: options.settleLimitMs ?? 8000,
      backoffMs: options.backoffMs ?? [2000, 5000, 10_000, 20_000, 30_000, 60_000],
      sustainedMs: options.sustainedMs ?? 15_000,
    });
  }

  // ── The feed ──────────────────────────────────────────────────────

  /**
   * Listen on the device's one feed. Call again with the feed that replaces it, or with null
   * when there is none: a different feed cannot say what the old one would have, so every
   * document is then out of date.
   */
  attach(events: EventsClient | null): void {
    if (this.disposed || events === this.events) return;
    const had = this.events;
    this.detach?.(); this.detach = undefined;
    this.events = events;
    if (!events) { this.report(this.feedSource, true); if (had) this.resync(); return; }
    const offAny = events.onAny(event => this.accept(event));
    const offResync = events.onResync(() => this.resync());
    let everConnected = events.getStatus().connected;
    const status = () => {
      const s = events.getStatus();
      if (s.mode === 'stopped') { this.report(this.feedSource, true); return; }
      if (s.connected) { everConnected = true; this.report(this.feedSource, true); }
      // A feed that has not connected yet has not failed: only a connection that was lost, or attempts that keep failing, count.
      else if (everConnected || s.failures > 0) this.report(this.feedSource, false);
    };
    const offStatus = events.subscribeStatus(status);
    status();
    this.detach = () => { offAny(); offResync(); offStatus(); };
    if (had) this.resync();
  }

  /** One event from the feed. Public so a platform that receives events elsewhere can hand them over, and for tests. */
  accept(event: ServerEvent): void {
    if (this.disposed) return;
    const moved: FreshnessDependency[] = [];
    const change = parseFreshnessChange(event);
    const differs = (name: string, value: string | undefined) => {
      // A counter the hint does not carry is unknown, so it is taken to have moved.
      if (value === undefined) return true;
      if (this.history.get(`c:${name}`) === value) return false;
      this.remember(`c:${name}`, value);
      return true;
    };
    if (change) {
      const id = change.resourceId;
      if (change.type === 'libraries.changed') {
        if (differs('directory', change.counters.directory ?? change.revision)) moved.push(dependsOn.libraries);
      } else if (change.type === 'library.changed') {
        const content = differs(`content:${id}`, change.counters.content);
        const order = differs(`order:${id}`, change.counters.contentOrder);
        if (content) moved.push(dependsOn.content(id), dependsOn.content());
        if (order) moved.push(dependsOn.order(id), dependsOn.order());
      } else if (change.type === 'personal.changed' || change.type === 'progress.changed') {
        // A position hint names the watched-state counter too, unchanged: seeing it for the first time is not a change to it.
        const known = this.history.has(`c:personal:${id}`);
        const personal = change.counters.personal === undefined ? change.type === 'personal.changed' : differs(`personal:${id}`, change.counters.personal) && (known || change.type === 'personal.changed');
        const progress = change.counters.observation === undefined ? change.type === 'progress.changed' : differs(`progress:${id}`, change.counters.observation);
        if (personal) moved.push(dependsOn.personal(id), dependsOn.personal());
        if (progress) moved.push(dependsOn.progress(id), dependsOn.progress());
        // A hint that names its title moves only the pages about that title (or a title above it);
        // one that names none moves every title page of the library.
        if (personal) moved.push(...(change.titles ? change.titles.map(t => `personal:title:${t}`) : [`personal:any:${id}`, `personal:any:${ALL}`]));
        if (progress) moved.push(...(change.titles ? change.titles.map(t => `progress:title:${t}`) : [`progress:any:${id}`, `progress:any:${ALL}`]));
      } else if (change.type === 'settings.changed') {
        // Each preference scope/device address has its own revision counter.
        // Unknown addresses still invalidate; they cannot safely be deduped.
        const address = change.preferenceAddress;
        const key = id === 'preferences' && address ? `settings:${id}:${JSON.stringify([address.scope, address.deviceClass ?? '', address.deviceId ?? ''])}` : `settings:${id}`;
        if (differs(key, id === 'preferences' && !address ? undefined : change.revision)) moved.push(`settings:${id}`);
      }
    } else if (!event.type.startsWith('stream.')) {
      moved.push(dependsOn.event(event.type));
      if (event.resource?.id) moved.push(dependsOn.event(event.type, event.resource.id));
    }
    if (moved.length) this.bump(moved);
  }

  /** The feed could not say everything that happened: every document is out of date. */
  resync(): void {
    if (this.disposed) return;
    this.history.clear();
    this.invalidateHistory();
  }

  private remember(key: string, value: string | number): void {
    if (this.history.set(key, value)) this.invalidateHistory();
  }

  private invalidateHistory(): void {
    // Forgotten versions must never look equal to any pre-eviction witness.
    this.versionFloor = ++this.highestVersion;
    this.sharedReads.move();
    for (const document of [...this.documents]) document.moved(false);
    for (const interest of [...this.interests]) interest.moved();
    for (const watcher of [...this.watchers]) watcher.listener();
  }

  private bump(moved: readonly FreshnessDependency[]): void {
    const set = new Set(moved);
    for (const dependency of set) {
      const next = this.version(dependency) + 1;
      this.highestVersion = Math.max(this.highestVersion, next);
      this.remember(`v:${dependency}`, next);
    }
    this.sharedReads.move([...set]);
    for (const document of [...this.documents]) {
      let any = false, progressOnly = true;
      for (const dependency of document.dependencies()) {
        if (!set.has(dependency)) continue;
        any = true;
        if (!dependency.startsWith('progress:')) { progressOnly = false; break; }
      }
      if (any) document.moved(progressOnly);
    }
    for (const interest of [...this.interests]) if (interest.dependencies().some(dependency => set.has(dependency))) interest.moved();
    for (const watcher of [...this.watchers]) for (const dependency of set) if (watcher.dependencies.has(dependency)) { watcher.listener(); break; }
  }

  /**
   * This device has reason to think a dependency moved without the feed saying so (a `clock`
   * coming round). Documents built from it are read again on the usual terms: only while open and
   * in front, gathered, after a pause in input, quietly retried.
   */
  note(dependency: FreshnessDependency): void {
    if (!this.disposed) this.bump([dependency]);
  }

  /** Monotonic dependency witness; eviction/resync may advance its conservative floor. */
  version(dependency: FreshnessDependency): number {
    const stored = this.history.get(`v:${dependency}`);
    return Math.max(typeof stored === 'number' ? stored : 0, this.versionFloor);
  }

  /** Hear when any of these dependencies moves (for a store that is not a document: a scan tracker, a badge). */
  watch(dependencies: readonly FreshnessDependency[], listener: () => void): () => void {
    const watcher = {dependencies: new Set(dependencies), listener};
    this.watchers.add(watcher);
    return () => { this.watchers.delete(watcher); };
  }

  // ── Documents ─────────────────────────────────────────────────────

  document(options: FreshDocumentOptions): FreshDocument {
    const document = new Document(this, options);
    if (!this.disposed) this.documents.add(document);
    return document;
  }

  forget(document: FreshDocument): void { this.documents.delete(document as Document); }

  /** One bounded obligation; initially open, with no read owed until invalidated. */
  interest(options: FreshnessInterestOptions): FreshnessInterest {
    const interest = new Interest(this, options);
    if (this.disposed) interest.dispose(); else this.interests.add(interest);
    return interest;
  }
  /** @internal */
  forgetInterest(interest: Interest): void { this.interests.delete(interest); }

  // ── Foreground and input ──────────────────────────────────────────

  /** @internal Admission only: existing owners keep debt, waits and read outcomes. */
  readAdmission(): object | undefined {return this.events?.resyncReads;}
  read<T>(signal: AbortSignal, read: () => Promise<T>): Promise<T> {
    return this.events ? this.events.resyncReads.run(signal, read) : read();
  }

  isActive(): boolean { return this.active && !this.disposed; }

  /** The app is in front (true) or in the background (false). Nothing is read in the background. */
  setActive(active: boolean): void {
    if (this.disposed || this.active === active) return;
    this.active = active;
    if (active) {
      for (const [source] of this.failing) this.failing.set(source, this.timers.now());
      this.judge();
      for (const document of [...this.documents]) document.check();
    } else {
      for (const document of [...this.documents]) document.pause();
      this.clearReachTimer();
      this.setReachability('reachable');
      for (const waiter of [...this.settleWaiters]) waiter();
    }
    for (const interest of [...this.interests]) interest.check();
  }

  /** The person pressed, clicked, scrolled or moved focus just now. */
  touch(): void { this.lastInput = this.timers.now(); }

  /** Resolves once input has paused for `settleMs`, or after `settleLimitMs` whatever happens. */
  settled(signal?: AbortSignal): Promise<void> {
    const {settleMs, settleLimitMs} = this.tuning;
    const started = this.timers.now();
    if (signal?.aborted || this.disposed || started - this.lastInput >= settleMs) return Promise.resolve();
    return new Promise(resolve => {
      let timer: unknown;
      const done = () => { this.settleWaiters.delete(done); if (timer !== undefined) this.timers.clearTimer(timer); signal?.removeEventListener('abort', done); resolve(); };
      const look = () => {
        timer = undefined;
        const now = this.timers.now(), quiet = now - this.lastInput;
        if (quiet >= settleMs || now - started >= settleLimitMs) { done(); return; }
        timer = this.timers.setTimer(look, Math.max(1, Math.min(settleMs - quiet, settleLimitMs - (now - started))));
      };
      this.settleWaiters.add(done);
      signal?.addEventListener('abort', done, {once: true});
      look();
    });
  }

  // ── Whether the server can be reached ─────────────────────────────

  /**
   * A source of evidence (the feed, the route, one document's reads) succeeded or failed just now.
   * Any success means the server answers: it clears the state, and every other source that is
   * still failing starts its wait again from this moment.
   */
  report(source: object, ok: boolean): void {
    if (this.disposed) return;
    if (ok) {
      if (!this.failing.size) return;
      this.failing.delete(source);
      const now = this.timers.now();
      for (const other of [...this.failing.keys()]) this.failing.set(other, now);
    } else if (!this.failing.has(source)) this.failing.set(source, this.timers.now());
    else return;
    this.judge();
  }

  getReachability = (): Reachability => this.reachability;
  subscribeReachability = (listener: () => void): (() => void) => { this.reachListeners.add(listener); return () => { this.reachListeners.delete(listener); }; };

  private clearReachTimer(): void { if (this.reachTimer !== undefined) { this.timers.clearTimer(this.reachTimer); this.reachTimer = undefined; } }
  private setReachability(next: Reachability): void {
    if (this.reachability === next) return;
    this.reachability = next;
    for (const listener of [...this.reachListeners]) listener();
  }
  private judge(): void {
    this.clearReachTimer();
    if (!this.active || !this.failing.size) { this.setReachability('reachable'); return; }
    let earliest = Infinity;
    for (const since of this.failing.values()) earliest = Math.min(earliest, since);
    const remaining = earliest + this.tuning.sustainedMs - this.timers.now();
    if (remaining <= 0) { this.setReachability('unreachable'); return; }
    this.reachTimer = this.timers.setTimer(() => { this.reachTimer = undefined; this.judge(); }, remaining);
  }

  dispose(): void {
    if (this.disposed) return;
    this.detach?.(); this.detach = undefined; this.events = null;
    for (const document of [...this.documents]) document.dispose();
    for (const interest of [...this.interests]) interest.dispose();
    this.sharedReads.dispose(); this.history.clear();
    this.disposed = true;
    this.clearReachTimer();
    this.watchers.clear(); this.reachListeners.clear();
    for (const waiter of [...this.settleWaiters]) waiter();
  }
}
