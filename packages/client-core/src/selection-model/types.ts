import type {JobSelector} from '../bulk-jobs.ts';
import type {ContentEntryKind} from '../library-content.ts';

export type SelectionKind = ContentEntryKind | 'channel' | 'person';
/** Displayed aggregate identity, never a card index or its first playable child. */
export type CatalogueSelectionIdentity = Readonly<{libraryId: string; kind: ContentEntryKind; id: string}>;
/** Source-local channel ids can collide across sources. Neither part is a library id. */
export type ChannelSelectionIdentity = Readonly<{kind: 'channel'; sourceId: string; channelId: string}>;
export type PersonSelectionIdentity = Readonly<{kind: 'person'; id: string}>;
export type SelectionIdentity = CatalogueSelectionIdentity | ChannelSelectionIdentity | PersonSelectionIdentity;
export type SelectionScope = Readonly<{
  serverId: string;
  authority: 'local' | 'hosted';
  accountId: string;
  profileId: string;
  /** Include route, pivot, filters and ordering in the caller's view identity. */
  viewId: string;
  /** The server's viewer/restriction fence; never an authentication credential. */
  privacyFence: string;
}>;
export type SelectionRevision = Readonly<{catalogRevision: string; viewerRevision: string}>;
export type SelectionContext = Readonly<{scope: SelectionScope; revision: SelectionRevision}>;
/** Browse reports one opaque page revision, not a viewer fence or revision pair.
 * The caller retires this context on principal/route authority replacement.
 * viewId captures the applied library, pivot, full query and ordering. */
export type BrowseSelectionScope = Omit<SelectionScope, 'privacyFence'>;
export type BrowseSelectionContext = Readonly<{scope: BrowseSelectionScope; revision: Readonly<{pageRevision: string}>}>;

/** All-of facts from actual capabilities, not inferred permission grants. */
export type SelectionCapabilities = Readonly<{
  play: boolean;
  queue: boolean;
  playlist: boolean;
  myList: boolean;
  watched: boolean;
  collection: boolean;
  favorite: boolean;
  download: boolean;
  edit: boolean;
  refresh: boolean;
  delete: boolean;
}>;
export type SelectionItem = SelectionIdentity & Readonly<{capabilities: SelectionCapabilities}>;
export type SelectionTypeCount = Readonly<{kind: SelectionKind; count: number}>;
export type SelectionLibrary =
  | Readonly<{kind: 'none'}>
  | Readonly<{kind: 'single'; libraryId: string}>
  | Readonly<{kind: 'multiple'}>
  | Readonly<{kind: 'unknown'}>;
/** Server-selected producer exclusions; these never alter per-title grants or counts. */
export type SelectionUnavailableActions = Readonly<{
  play?: 'unsupported-sort';
  queue?: 'unsupported-sort';
  playlist?: 'unsupported-sort';
  delete?: 'unsupported-sort';
  metadataPreview?: 'preview-limit';
}>;
export type SelectionSummary = Readonly<{
  count: number;
  types: readonly SelectionTypeCount[];
  library: SelectionLibrary;
  every: SelectionCapabilities;
  unavailableActions?: SelectionUnavailableActions;
}>;

export type SelectionCapabilityCounts = Readonly<Record<keyof SelectionCapabilities, number>>;
/** Exact aggregate facts for one kind, over the full authorised query at this revision. */
export type SelectionQueryType = SelectionTypeCount & Readonly<{
  /** Only catalogue kinds have an existing whole-query selector contract. */
  kind: ContentEntryKind;
  capabilityCounts: SelectionCapabilityCounts;
  libraries: readonly Readonly<{libraryId: string; count: number}>[];
}>;

export type WholeSelectionSelector = Exclude<JobSelector, {items: unknown}>;
/** An aggregate describing the entire authorised result, never just a loaded page. */
export type SelectionQuery = Readonly<{
  id: string;
  selector: WholeSelectionSelector;
  total: number;
  /** Exactly the revision the existing selector contract expects. */
  catalogRevision: string;
  summary: Omit<SelectionSummary, 'count' | 'types'> & Readonly<{types: readonly SelectionQueryType[]}>;
}>;
export type SelectionWindow = Readonly<{
  id: string;
  sectionId: string;
  sectionOrder: number;
  /** Absolute offset in this section, used only to prove a range has no holes. */
  start: number;
  /** Only windows explicitly bound to this descriptor belong to its whole selection. */
  queryId?: string;
  items: readonly SelectionItem[];
}>;
export type SelectedItem = Readonly<{key: string; item: SelectionItem}>;
export type ExplicitSelectionTarget<C = SelectionContext> = Readonly<{
  mode: 'explicit';
  context: C;
  /** In pick order for queue actions. Aggregate identities remain aggregates. */
  items: readonly SelectedItem[];
  /** The same picks in displayed section/position order, for Play. */
  displayItems: readonly SelectedItem[];
}>;
export type QuerySelectionTarget<C = SelectionContext> = Readonly<{
  mode: 'query';
  context: C;
  queryId: string;
  selector: WholeSelectionSelector;
  /** Selected count, after semantic narrowing and exclusions. */
  total: number;
  sourceTotal: number;
  /** These are model semantics, NOT fields of JobSelector. Never drop them when dispatching. */
  refinement: Readonly<{
    kind: SelectionKind | null;
    excludedItems: readonly SelectedItem[];
  }>;
  summary: SelectionSummary;
  expected: Readonly<{catalogRevision: string}>;
}>;
export type SelectionTarget<C = SelectionContext> = ExplicitSelectionTarget<C> | QuerySelectionTarget<C>;
export type SelectionSnapshot<C = SelectionContext> = Readonly<{
  version: number;
  context: C;
  active: boolean;
  summary: SelectionSummary;
  target: SelectionTarget<C> | null;
  /** Explicit identities only: this never enumerates a query. */
  selectedKeys: readonly string[];
  lastSectionId: string | null;
  registeredWindowCount: number;
  registeredItemCount: number;
}>;

/** Model-issued tickets are checked by object identity as well as generation. */
export type SelectionReadTicket<C = SelectionContext> = Readonly<{context: C}>;
export type SelectionOperation<C = SelectionContext> = Readonly<{target: SelectionTarget<C>}>;
export type SelectionFailure =
  | 'disposed' | 'stale-context' | 'unregistered' | 'no-anchor'
  | 'different-section' | 'range-not-loaded' | 'not-query-member'
  | 'exclusion-limit' | 'unknown-query';
export type SelectionChange = Readonly<{ok: true}> | Readonly<{ok: false; reason: SelectionFailure}>;
export type SelectionRegistration =
  | Readonly<{ok: true; unregister: () => void}>
  | Readonly<{ok: false; reason: 'disposed' | 'stale-context'}>;
export type SelectionRange =
  | Readonly<{ok: true; keys: readonly string[]}>
  | Readonly<{ok: false; reason: SelectionFailure}>;
export type SelectionLimits = Readonly<{
  maxWindows: number;
  maxWindowItems: number;
  maxRegisteredItems: number;
  maxQueries: number;
  maxQueryExclusions: number;
}>;

export type SelectionPlatform = 'desktop-web' | 'mobile-web' | 'phone-app' | 'tv';
export type SelectionEnvironment = Readonly<{
  platform: SelectionPlatform;
  isOwner: boolean;
  /** Supply only an existing, verified metadata authority. */
  canEditMetadata: boolean;
}>;
export type SelectionAction =
  | 'play' | 'play-next' | 'play-later' | 'playlist' | 'my-list'
  | 'mark-watched' | 'mark-unwatched' | 'collection' | 'favorite'
  | 'remove-my-list' | 'edit' | 'download' | 'refresh' | 'delete';
export type SelectionActionReason =
  | 'not-permitted' | 'mixed-types' | 'no-bulk-fields'
  | 'one-library-required' | 'delete-limit' | 'unsupported-sort' | 'preview-limit';
export type SelectionActionRule =
  | Readonly<{state: 'hidden'}>
  | Readonly<{state: 'ready'}>
  | Readonly<{state: 'disabled' | 'explain'; reason: SelectionActionReason}>;
export type SelectionActionRules = Readonly<Record<SelectionAction, SelectionActionRule>>;
