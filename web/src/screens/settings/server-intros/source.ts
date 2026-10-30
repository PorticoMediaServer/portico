import {defaultI18n} from '@i18n';
import {ApiError} from '@core/index.ts';
import {
  approveSegments, editSegmentMarker, readSegmentStatus, readSegmentTitle, readSegmentTitles, redetectSegments, segmentKinds, segmentRequestId,
  type SegmentApi, type SegmentKind, type SegmentReviewEntry, type SegmentReviewMarker, type SegmentStatus, type SegmentTitlePage, type SegmentTitleReview, type SegmentTitleSummary,
} from '@core/segment-review.ts';

/** The two bounded reviews and the owner's marker writes. Injected only so the
 * state machine can be exercised without inventing a wire response. */
export type SegmentIntrosReads = Readonly<{
  status: (signal: AbortSignal) => Promise<SegmentStatus>;
  titles: (libraryId: string, cursor: string, signal: AbortSignal) => Promise<SegmentTitlePage>;
  title: (id: string, signal: AbortSignal) => Promise<SegmentTitleReview>;
  approveSeason: (id: string, season: number | undefined, kinds: readonly SegmentKind[]) => Promise<number>;
  redetect: (id: string) => Promise<number>;
  approveMarker: (entry: SegmentReviewEntry, marker: SegmentReviewMarker) => Promise<unknown>;
  dismissMarker: (entry: SegmentReviewEntry, marker: SegmentReviewMarker) => Promise<unknown>;
  createMarker: (entry: SegmentReviewEntry, kind: SegmentKind, startUS: string, endUS: string, requestId: string) => Promise<unknown>;
  editMarker: (entry: SegmentReviewEntry, marker: SegmentReviewMarker, kind: SegmentKind, startUS: string, endUS: string) => Promise<unknown>;
}>;

const definiteMarkerRefusal = new Set(['invalid_request', 'unauthorized', 'not_permitted', 'not_found']);
/** Strict positive microsecond field. */
const markerMicros = (value: string): number | undefined => {
  if (!/^(0|[1-9][0-9]{0,18})$/.test(value)) return undefined;
  const micros = Number(value);
  return Number.isSafeInteger(micros) ? micros : undefined;
};

export function segmentIntrosReads(api: SegmentApi): SegmentIntrosReads {
  return {
    status: signal => readSegmentStatus(api, signal),
    titles: (libraryId, cursor, signal) => readSegmentTitles(api, libraryId, cursor, signal),
    title: (id, signal) => readSegmentTitle(api, id, signal),
    approveSeason: (id, season, kinds) => approveSegments(api, id, kinds, season),
    redetect: id => redetectSegments(api, id),
    approveMarker: (entry, marker) => editSegmentMarker(api, entry, {action: 'approve', markerId: marker.id}),
    dismissMarker: (entry, marker) => editSegmentMarker(api, entry, {action: 'dismiss', markerId: marker.id}),
    createMarker: (entry, kind, startUS, endUS, requestId) => editSegmentMarker(api, entry, {action: 'create', kind, startUS, endUS}, requestId),
    editMarker: (entry, marker, kind, startUS, endUS) => editSegmentMarker(api, entry, {action: 'edit', markerId: marker.id, kind, startUS, endUS}),
  };
}

export type SegmentIntrosPhase = 'idle' | 'loading' | 'ready' | 'error';

export type SegmentIntrosSnapshot = Readonly<{
  phase: SegmentIntrosPhase;
  status?: SegmentStatus;
  error?: unknown;
  libraryId?: string;
  titles: readonly SegmentTitleSummary[];
  titlesCursor: string;
  titlesLoading: boolean;
  titlesError?: unknown;
  titleId?: string;
  title?: SegmentTitleReview;
  season?: number;
  titleLoading: boolean;
  titleError?: unknown;
  busy?: 'approve' | 'redetect' | 'marker';
  note?: string;
  actionError?: unknown;
}>;

type Options = Readonly<{
  api: SegmentApi;
  isCurrent: () => boolean;
  reportError?: (error: unknown) => void;
  reads?: SegmentIntrosReads;
  /** The drawn progress line follows the server; a page that is not current stops it. */
  pollMs?: number;
}>;

const empty: SegmentIntrosSnapshot = Object.freeze({phase: 'idle', titles: [], titlesCursor: '', titlesLoading: false, titleLoading: false});
const approvedKinds: readonly SegmentKind[] = ['intro', 'recap', 'credits', 'outro'];

/** One owner-scoped review: the server's status, a bounded library page, one
 * title's seasons and markers, and the marker writes fenced by the entry the
 * review carried. Every read and write is generation-checked, so a retired or
 * replaced scope cannot publish an older answer. */
export class SegmentIntrosSource {
  private options: Options;
  private reads: SegmentIntrosReads;
  private state: SegmentIntrosSnapshot = empty;
  private listeners = new Set<() => void>();
  private statusGeneration = 0;
  private libraryGeneration = 0;
  private titleGeneration = 0;
  private poll?: ReturnType<typeof setTimeout>;
  private disposed = false;
  constructor(options: Options) {
    this.options = options;
    this.reads = options.reads ?? segmentIntrosReads(options.api);
  }
  getSnapshot = (): SegmentIntrosSnapshot => this.state;
  subscribe = (listener: () => void): (() => void) => {
    if (this.disposed) return () => {};
    this.listeners.add(listener);
    return () => {this.listeners.delete(listener);};
  };
  private current(): boolean {
    if (!this.disposed && !this.options.isCurrent()) this.dispose();
    return !this.disposed;
  }
  private publish(patch: Partial<SegmentIntrosSnapshot>): void {
    if (!this.current()) return;
    this.state = Object.freeze({...this.state, ...patch});
    for (const listener of [...this.listeners]) listener();
  }
  private report(error: unknown): void {
    if (this.current()) this.options.reportError?.(error);
  }
  private stopPolling(): void {
    if (this.poll !== undefined) clearTimeout(this.poll);
    this.poll = undefined;
  }
  private schedule(): void {
    this.stopPolling();
    if (this.disposed || !this.current()) return;
    const every = this.options.pollMs ?? 15000;
    if (!(every > 0)) return;
    this.poll = setTimeout(() => {this.poll = undefined; void this.loadStatus(false);}, every);
  }
  /** Shows the mode and how far the work has got. `first` owns the loading state. */
  load = async (): Promise<void> => {
    if (!this.current()) return;
    this.publish({phase: this.state.status ? 'ready' : 'loading', error: undefined});
    await this.loadStatus(true);
  };
  private async loadStatus(first: boolean): Promise<void> {
    if (!this.current()) return;
    const generation = ++this.statusGeneration;
    try {
      const status = await this.reads.status(new AbortController().signal);
      if (!this.current() || generation !== this.statusGeneration) return;
      this.publish({phase: 'ready', status, error: undefined});
    } catch (error) {
      if (!this.current() || generation !== this.statusGeneration) return;
      if (first || !this.state.status) this.publish({phase: 'error', error});
      else this.report(error);
    } finally {
      if (this.current() && generation === this.statusGeneration) this.schedule();
    }
  }
  /** One library's first bounded page. A newer open or a retired page wins. */
  openLibrary = async (libraryId: string): Promise<void> => {
    if (!this.current() || !libraryId) return;
    const generation = ++this.libraryGeneration;
    ++this.titleGeneration;
    this.publish({libraryId, titles: [], titlesCursor: '', titlesLoading: true, titlesError: undefined,
      titleId: undefined, title: undefined, season: undefined, titleLoading: false, titleError: undefined, busy: undefined, note: undefined, actionError: undefined});
    try {
      const page = await this.reads.titles(libraryId, '', new AbortController().signal);
      if (!this.current() || generation !== this.libraryGeneration) return;
      this.publish({titles: Object.freeze([...page.titles]), titlesCursor: page.nextCursor, titlesLoading: false});
    } catch (error) {
      if (!this.current() || generation !== this.libraryGeneration) return;
      this.publish({titlesLoading: false, titlesError: error});
      this.report(error);
    }
  };
  /** The next bounded page of the selected library. */
  moreTitles = async (): Promise<void> => {
    const {libraryId, titlesCursor, titlesLoading} = this.state;
    if (!this.current() || !libraryId || !titlesCursor || titlesLoading) return;
    const generation = this.libraryGeneration;
    this.publish({titlesLoading: true, titlesError: undefined});
    try {
      const page = await this.reads.titles(libraryId, titlesCursor, new AbortController().signal);
      if (!this.current() || generation !== this.libraryGeneration || this.state.libraryId !== libraryId) return;
      this.publish({titles: Object.freeze([...this.state.titles, ...page.titles]), titlesCursor: page.nextCursor, titlesLoading: false});
    } catch (error) {
      if (!this.current() || generation !== this.libraryGeneration) return;
      this.publish({titlesLoading: false, titlesError: error});
      this.report(error);
    }
  };
  /** One title's real review, with its first season selected when it has seasons. */
  openTitle = async (id: string): Promise<void> => {
    if (!this.current() || !id) return;
    const generation = ++this.titleGeneration;
    this.publish({titleId: id, title: undefined, season: undefined, titleLoading: true, titleError: undefined, busy: undefined, note: undefined, actionError: undefined});
    try {
      const title = await this.reads.title(id, new AbortController().signal);
      if (!this.current() || generation !== this.titleGeneration || this.state.titleId !== id) return;
      const season = title.kind === 'show' ? title.seasons[0]?.number : undefined;
      this.publish({title, season, titleLoading: false});
    } catch (error) {
      if (!this.current() || generation !== this.titleGeneration || this.state.titleId !== id) return;
      this.publish({titleLoading: false, titleError: error});
      this.report(error);
    }
  };
  back = (): void => {
    if (!this.current()) return;
    ++this.titleGeneration;
    this.publish({titleId: undefined, title: undefined, season: undefined, titleLoading: false, titleError: undefined, busy: undefined, note: undefined, actionError: undefined});
  };
  selectSeason = (season: number): void => {
    if (!this.current() || !this.state.title || this.state.title.kind !== 'show') return;
    if (!this.state.title.seasons.some(value => value.number === season)) return;
    this.publish({season, note: undefined, actionError: undefined});
  };
  private async reloadTitle(generation: number): Promise<void> {
    const id = this.state.titleId;
    if (!this.current() || !id || generation !== this.titleGeneration) return;
    try {
      const title = await this.reads.title(id, new AbortController().signal);
      if (!this.current() || generation !== this.titleGeneration || this.state.titleId !== id) return;
      const season = this.state.season !== undefined && title.seasons.some(value => value.number === this.state.season) ? this.state.season : title.kind === 'show' ? title.seasons[0]?.number : undefined;
      this.publish({title, season, titleLoading: false, titleError: undefined});
    } catch (error) {
      if (!this.current() || generation !== this.titleGeneration) return;
      this.publish({titleLoading: false, titleError: error});
      this.report(error);
    }
  }
  /** Approve every waiting marker in the selected season (or the film). */
  approve = async (): Promise<void> => {
    const {title, season} = this.state;
    if (!this.current() || !title || this.state.busy) return;
    const generation = this.titleGeneration;
    this.publish({busy: 'approve', note: undefined, actionError: undefined});
    try {
      const changed = await this.reads.approveSeason(title.id, title.kind === 'show' ? season : undefined, approvedKinds);
      if (!this.current() || generation !== this.titleGeneration) return;
      this.publish({note: defaultI18n.t('web.segments.approvedCount', {count: changed})});
      await this.reloadTitle(generation);
      void this.loadStatus(false);
    } catch (error) {
      if (!this.current() || generation !== this.titleGeneration) return;
      this.publish({actionError: error});
      this.report(error);
    } finally {
      if (this.current() && generation === this.titleGeneration) this.publish({busy: undefined});
    }
  };
  /** Measure this title's files again; approvals and edits are kept by the server. */
  detectAgain = async (expectedTitle: SegmentTitleReview): Promise<void> => {
    const {title} = this.state;
    if (!this.current() || !title || title !== expectedTitle || this.state.busy) return;
    const generation = this.titleGeneration;
    this.publish({busy: 'redetect', note: undefined, actionError: undefined});
    try {
      const queued = await this.reads.redetect(title.id);
      if (!this.current() || generation !== this.titleGeneration) return;
      this.publish({note: defaultI18n.t('web.segments.detectAgainQueued', {count: queued})});
      await this.reloadTitle(generation);
      void this.loadStatus(false);
    } catch (error) {
      if (!this.current() || generation !== this.titleGeneration) return;
      this.publish({actionError: error});
      this.report(error);
    } finally {
      if (this.current() && generation === this.titleGeneration) this.publish({busy: undefined});
    }
  };
  approveMarker = async (entry: SegmentReviewEntry, marker: SegmentReviewMarker): Promise<void> => {
    if (!this.current() || this.state.busy) return;
    const generation = this.titleGeneration;
    this.publish({busy: 'marker', note: undefined, actionError: undefined});
    try {
      await this.reads.approveMarker(entry, marker);
      if (!this.current() || generation !== this.titleGeneration) return;
      await this.reloadTitle(generation);
    } catch (error) {
      if (!this.current() || generation !== this.titleGeneration) return;
      this.publish({actionError: error});
      this.report(error);
    } finally {
      if (this.current() && generation === this.titleGeneration) this.publish({busy: undefined});
    }
  };
  private createPending?: Readonly<{entry: SegmentReviewEntry; kind: SegmentKind; startUS: string; endUS: string; requestId: string}>;
  createMarker = async (entry: SegmentReviewEntry, kind: SegmentKind, startUS: string, endUS: string): Promise<boolean> => {
    if (!this.current() || this.state.busy) return false;
    const snapshot = this.state, title = snapshot.title;
    if (!title || title.id !== snapshot.titleId) return false;
    // The exact captured row must still be this open title's row.
    if (!title.seasons.some(season => season.entries.some(value => value === entry))) return false;
    if (!segmentKinds.includes(kind)) return false;
    const start = markerMicros(startUS), end = markerMicros(endUS), duration = markerMicros(entry.durationUS);
    if (start === undefined || end === undefined || duration === undefined || start < 0 || start >= end || end > duration) return false;
    const generation = this.titleGeneration;
    const previous = this.createPending;
    const sameDraft = !!previous && previous.entry.itemId === entry.itemId && previous.entry.sourceId === entry.sourceId && previous.entry.sourceRevision === entry.sourceRevision
      && previous.entry.mappingRevision === entry.mappingRevision && previous.entry.markerRevision === entry.markerRevision
      && previous.kind === kind && previous.startUS === startUS && previous.endUS === endUS;
    // An unknown result must be resolved with the original request; a different
    // draft cannot start a second create over it.
    if (previous && !sameDraft) return false;
    const pending = sameDraft && previous ? previous : {entry, kind, startUS, endUS, requestId: segmentRequestId()};
    this.createPending = pending;
    this.publish({busy: 'marker', note: undefined, actionError: undefined});
    try {
      await this.reads.createMarker(pending.entry, kind, startUS, endUS, pending.requestId);
      if (!this.current() || generation !== this.titleGeneration) return false;
      if (this.createPending === pending) this.createPending = undefined;
      await this.reloadTitle(generation);
      return this.current() && generation === this.titleGeneration;
    } catch (error) {
      if (!this.current() || generation !== this.titleGeneration) return false;
      if (error instanceof ApiError && definiteMarkerRefusal.has(error.code) && this.createPending === pending) this.createPending = undefined;
      this.publish({actionError: error});
      this.report(error);
      return false;
    } finally {
      if (this.current() && generation === this.titleGeneration) this.publish({busy: undefined});
    }
  };
  dismissMarker = async (entry: SegmentReviewEntry, marker: SegmentReviewMarker): Promise<void> => {
    if (!this.current() || this.state.busy) return;
    const snapshot = this.state, title = snapshot.title;
    if (!title || title.id !== snapshot.titleId) return;
    // The exact rendered row and marker must still belong to the open title; a
    // replacement object with the same item id is refused, never adopted.
    if (!title.seasons.some(season => season.entries.some(value => value === entry))) return;
    if (!entry.markers.some(value => value === marker)) return;
    const generation = this.titleGeneration;
    this.publish({busy: 'marker', note: undefined, actionError: undefined});
    try {
      await this.reads.dismissMarker(entry, marker);
      if (!this.current() || generation !== this.titleGeneration) return;
      await this.reloadTitle(generation);
    } catch (error) {
      if (!this.current() || generation !== this.titleGeneration) return;
      this.publish({actionError: error});
      this.report(error);
    } finally {
      if (this.current() && generation === this.titleGeneration) this.publish({busy: undefined});
    }
  };
  saveMarker = async (entry: SegmentReviewEntry, marker: SegmentReviewMarker, kind: SegmentKind, startUS: string, endUS: string): Promise<void> => {
    if (!this.current() || this.state.busy) return;
    const generation = this.titleGeneration;
    this.publish({busy: 'marker', note: undefined, actionError: undefined});
    try {
      await this.reads.editMarker(entry, marker, kind, startUS, endUS);
      if (!this.current() || generation !== this.titleGeneration) return;
      await this.reloadTitle(generation);
    } catch (error) {
      if (!this.current() || generation !== this.titleGeneration) return;
      this.publish({actionError: error});
      this.report(error);
    } finally {
      if (this.current() && generation === this.titleGeneration) this.publish({busy: undefined});
    }
  };
  dispose = (): void => {
    if (this.disposed) return;
    this.disposed = true;
    ++this.statusGeneration; ++this.libraryGeneration; ++this.titleGeneration;
    this.stopPolling();
    this.listeners.clear();
  };
}


