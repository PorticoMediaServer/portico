/** Binary access is supplied by the selected server/viewer scope, or a lawful
 * local/offline file. Engines receive no URL, cookie, token or HTTP client. */
export type ReaderRange = Readonly<{start: number; endExclusive: number}>;
export type ReaderIndexedSearchStatus = 'indexed' | 'pending' | 'unsupported' | 'evicted' | 'excluded';
export type ReaderIndexedSearchHit = Readonly<{sectionPath: string; sectionIndex: number; utf16Offset: number; excerpt: string; matchStart: number; matchLength: number}>;
export type ReaderIndexedSearchPage = Readonly<{
  sourceId: string; sourceRevision: string; indexRevision: number;
  coverage: Readonly<{status: ReaderIndexedSearchStatus; reason?: string}>;
  hits: readonly ReaderIndexedSearchHit[]; next?: string;
}>;
export type ReaderIndexedSearchOptions = Readonly<{signal: AbortSignal; limit: number; cursor?: string}>;
export interface ReaderSource {
  readonly id: string;
  readonly revision: string;
  readonly size: number;
  readonly resources?: ReaderResources;
  /** Captured manifest entries for this source, never caller-supplied URLs. */
  readonly contents?: readonly ReaderContentsEntry[];
  /** Captured manifest spine order, required only for optional indexed search. */
  readonly spine?: readonly string[];
  /** Optional persisted-index page for this exact source. The host binds the
   * callback to one retained session/asset revision and supplies no URL or
   * token to the engine. Converted/offline sources must not provide it. */
  indexedSearch?(query: string, options: ReaderIndexedSearchOptions): Promise<ReaderIndexedSearchPage>;
  /** Genuine interactive from-start stream; never a synthetic random reader. */
  readonly sequential?: ReaderSequentialSource;
  read?(range: ReaderRange, signal: AbortSignal): Promise<Uint8Array>;
}
export type ReaderSequentialSource = Readonly<{open(signal: AbortSignal): Promise<ReadableStream<Uint8Array>>}>;
export type ReaderResource = Readonly<{path: string; mime: string; size: number}>;
export interface ReaderResources {
  readonly entries: readonly ReaderResource[];
  read(path: string, signal: AbortSignal): Promise<Uint8Array>;
}
export type ReaderComicFormat = 'cbz' | 'cbr';
export type ReaderFormat = 'epub' | 'pdf' | ReaderComicFormat;
export type ReaderCrease = Readonly<{left: number; right: number}>;
/** Window-local vertical hinge band reported by the platform, when there is one. */
/** `turnClearance`: the host's page arrows cover this many px from each side edge; text starts clear of them. */
export type ReaderViewport = Readonly<{width: number; height: number; crease?: ReaderCrease; turnClearance?: number}>;
export type ReaderRect = Readonly<{x: number; y: number; width: number; height: number}>;
type ObservedLocation = Readonly<{
  progression: number;
  /** These are omitted unless observed by the renderer; never estimated copy. */
  chapter?: string;
  pageLabel?: string;
  pagesLeftInSection?: number;
}>;
/** The visible part of one fixed page while a single page is zoomed past its fit,
 * as 0..1 page fractions. Live presentation only: absent at fit, on a spread, at
 * a fold crease and before a page is drawn, and never a persisted position. */
export type ReaderFixedViewport = Readonly<{
  page: number; pageWidth: number; pageHeight: number;
  x: number; y: number; width: number; height: number; scale: number;
}>;
export type ReaderLocation = ObservedLocation & (
  | Readonly<{format: 'epub'; cfi: string; section?: string}>
  | Readonly<{format: 'pdf' | ReaderComicFormat; page: number; viewport?: ReaderFixedViewport}>
);
export type ReaderTocItem = Readonly<{
  id: string; label: string; location: ReaderLocation; children?: readonly ReaderTocItem[];
}>;
export type ReaderContentsEntry = Readonly<{title: string; depth: number; section: string; fragment?: string}>;
/** A detail selection can cross a session reopen only for the same source revision. */
export type ReaderContentsTarget = Readonly<{sourceId: string; sourceRevision: string; index: number}>;
export type ReaderMetadata = Readonly<{
  title?: string; author?: string; language?: string;
  direction?: 'ltr' | 'rtl' | 'vertical'; pageCount?: number;
}>;
export type ReaderSelection = Readonly<{
  text: string; location: ReaderLocation; rects: readonly ReaderRect[];
  /** Real page-relative unit rectangles; viewport rectangles are never persisted as anchors. */
  pageRects?: readonly ReaderRect[];
}>;
export type ReaderSearchResult = Readonly<{
  id: string; excerpt: string; chapter?: string;
  /** A real engine location only when this hit's range was resolved and proved
   * against the current source. A display-only hit omits it; callers must not
   * navigate or persist a location they do not have. */
  location?: ReaderLocation;
  /** UTF-16 range of this actual hit within its literal excerpt; absent means unknown. */
  match?: Readonly<{start: number; length: number}>;
}>;
export type ReaderPageImage = Readonly<{mime: 'image/jpeg' | 'image/png' | 'image/webp'; bytes: Uint8Array; width: number; height: number}>;
export type ReaderSearchOptions = Readonly<{signal: AbortSignal; limit: number}>;
export type ReaderNavigation =
  | Readonly<{kind: 'next' | 'previous'}>
  | Readonly<{kind: 'location'; location: ReaderLocation}>
  | Readonly<{kind: 'progression'; value: number}>;
export type ReaderFit = 'page' | 'width' | 'height' | 'spread';
export type ReaderZoom = Readonly<{kind: 'scale'; value: number}> | Readonly<{kind: 'fit'; value: ReaderFit}>;
export type ReaderZoomState = Readonly<{scale: number; fit?: ReaderFit}>;
export type ReaderErrorCode = 'aborted' | 'closed' | 'invalid_source' | 'invalid_book'
  | 'invalid_location' | 'invalid_preferences' | 'limit_exceeded' | 'protected'
  | 'unsupported_format' | 'unsupported_operation' | 'source_unavailable' | 'render_failed';

/** No persistence API is exposed: Aa and fixed-page changes are session-only.
 * Preferences is the canonical Reading facade value, supplied by the host. */
export interface ReaderView<Preferences extends object> {
  navigate(target: ReaderNavigation, signal: AbortSignal): Promise<void>;
  applyPreferences(preferences: Readonly<Preferences>, signal: AbortSignal): Promise<void>;
  resize(viewport: ReaderViewport, signal: AbortSignal): Promise<void>;
  getLocation(): ReaderLocation | null;
  /** Transient presentation of a real search result; no bookmark/annotation persistence. */
  showSearchResult?(location: ReaderLocation | null, signal: AbortSignal): Promise<void>;
  setZoom?(zoom: ReaderZoom, signal: AbortSignal): Promise<void>;
  getZoom?(): ReaderZoomState;
  canTurnAtFit?(): boolean;
  pan?(offset: Readonly<{x: number; y: number}>, signal: AbortSignal): Promise<void>;
  dispose(): void | Promise<void>;
}
/** Original host motion policy only; renderer always ORs real system reduction. */
export type ReaderMotionPolicy = Readonly<{reduced(): boolean; subscribe(changed: () => void): () => void}>;
export type ReaderMount<Preferences extends object> = Readonly<{
  container: HTMLElement; preferences: Readonly<Preferences>; viewport: ReaderViewport;
  /** Resolved catalogue identity, including authoritative omitted fields. */
  metadata?: ReaderMetadata;
  motion?: ReaderMotionPolicy;
  signal: AbortSignal; onLocation(location: ReaderLocation): void;
  onSelection(selection: ReaderSelection | null): void;
  onSurfaceInput?(input: ReaderSurfaceInput): void;
}>;
export interface ReaderDocument<Preferences extends object> {
  readonly id: string;
  readonly format: ReaderFormat;
  readonly revision: string;
  readonly metadata: ReaderMetadata;
  readonly toc: readonly ReaderTocItem[];
  /** Optional expensive outline resolution. The first page mounts before this
   * runs; results remain owned by the same abortable document/session. */
  loadToc?(signal: AbortSignal): Promise<readonly ReaderTocItem[]>;
  /** Resolve a captured manifest entry through this already-open engine. */
  resolveContents?(index: number, signal: AbortSignal): Promise<ReaderLocation>;
  mount(options: ReaderMount<Preferences>): Promise<ReaderView<Preferences>>;
  /** Literal in-publication text search. A single word matches a whole Unicode
   * word/token; multiword input is a contiguous literal phrase with the same
   * outer boundaries. No query language or new toggle is part of this port. */
  search(query: string, options: ReaderSearchOptions): AsyncIterable<ReaderSearchResult>;
  /** Real fixed-page preview; width 32..2048, bytes at most 16MiB. */
  page?(index: number, options: Readonly<{width: number; signal: AbortSignal}>): Promise<ReaderPageImage>;
  dispose(): void | Promise<void>;
}
export interface ReaderAdapter<Preferences extends object> {
  readonly format: ReaderFormat;
  open(options: Readonly<{source: ReaderSource; signal: AbortSignal}>): Promise<ReaderDocument<Preferences>>;
}
export type ReaderSurfaceInput =
  | Readonly<{kind: 'tap'; fraction: number}>
  | Readonly<{kind: 'swipe'; direction: 'left' | 'right'}>
  | Readonly<{kind: 'activity'}>
  /** A renderer already committed an original gesture turn. Never navigate again. */
  | Readonly<{kind: 'turned'}>
  | Readonly<{kind: 'key'; key: string; shift: boolean; alt: boolean; ctrl: boolean; meta: boolean; editable: boolean; preventDefault(): void}>;
export type ReaderSnapshot<Preferences extends object> = Readonly<{
  status: 'closed' | 'opening' | 'ready' | 'error';
  zoom: ReaderZoomState | null; canTurnAtFit: boolean;
  format?: ReaderFormat; sourceId?: string; sourceRevision?: string; metadata?: ReaderMetadata;
  toc: readonly ReaderTocItem[]; location: ReaderLocation | null;
  selection: ReaderSelection | null; preferences: Readonly<Preferences>;
  overriddenKeys: readonly (keyof Preferences)[]; error?: ReaderErrorCode;
}>;
