import type {HomeApi, HomeChannel, HomeCollection, HomeDocument, HomeRow} from '../home.ts';
import type {HomeService} from '../home-service.ts';
import type {EventsClient} from '../playback-v1/events.ts';
import type {QueueHeader} from '../playback-v1/types.ts';
import type {ContentEntry} from '../library-content.ts';
import type {EntryActionGroup, EntryActionId} from '../presentation/entry-actions.ts';
import type {SelectionAction, SelectionActionRules, SelectionActionReason, SelectionContext, SelectionEnvironment, SelectionIdentity, SelectionItem, SelectionSnapshot} from '../selection-model/index.ts';
import type {Job} from '../bulk-jobs.ts';
import type {BulkEdit, BulkReceipt} from '../metadata-bulk.ts';
import type {DeletePreview, DeleteResult} from '../administration.ts';
import type {BulkMetadataPreview} from './metadata-preview.ts';
import type {HomeDestinationReceipt} from './destination-receipt.ts';

export type HomeSelectionPlaybackItem = Readonly<{
  identity: SelectionIdentity;
} & ({
  entry: ContentEntry;
  /** The actual server-selected child/resume target can differ from identity. */
  target: NonNullable<ContentEntry['playback']>;
  startIntent: 'resume' | 'start-over' | 'explicit';
} | {collection: HomeCollection})>;
export type HomeSelectionPlaybackRequest = Readonly<{
  context: SelectionContext;
  items: readonly HomeSelectionPlaybackItem[];
  signal: AbortSignal;
}>;
/** All commands must bind the real queue/player runtime; a resolved void is not a receipt. */
export interface HomeSelectionPlaybackPort {
  play(request: HomeSelectionPlaybackRequest): Promise<Readonly<{queue: QueueHeader}>>;
  enqueue(request: HomeSelectionPlaybackRequest & Readonly<{placement: 'next' | 'end'}>): Promise<Readonly<{queue: QueueHeader}>>;
  playChannel(channel: HomeChannel, context: SelectionContext, signal: AbortSignal): Promise<void>;
}
export type HomeSelectionDestination = Readonly<{kind: 'playlist' | 'collection'; id: string}>;
export interface HomeSelectionRoutes {
  openEntry(entry: ContentEntry): void | Promise<void>;
  openCollection(collection: HomeCollection): void | Promise<void>;
  openDestination(destination: HomeSelectionDestination): void | Promise<void>;
}
export type HomeSelectionOptions = Readonly<{
  api: HomeApi;
  events: Pick<EventsClient, 'on'> & Partial<Pick<EventsClient, 'onResync' | 'resyncReads'>>;
  home: HomeService;
  context: SelectionContext;
  environment: SelectionEnvironment;
  /** Current shell/source authority, including route, fence and document revision. */
  isCurrent(context: SelectionContext): boolean;
  requestId(): Promise<string>;
  playback: HomeSelectionPlaybackPort;
  routes: HomeSelectionRoutes;
  reportError(error: unknown): void;
  /** A genuine native download sheet/runtime, absent on other platforms. */
  download?: (entries: readonly ContentEntry[], context: SelectionContext, signal: AbortSignal) => Promise<void>;
}>;
export type HomeSelectionMember = Readonly<{item: SelectionItem; entry?: ContentEntry; collection?: HomeCollection; channel?: HomeChannel; playbackTarget?: ContentEntry}>;
export type HomeSelectionMenu = Readonly<{
  windowId: string;
  key: string;
  member: HomeSelectionMember;
  groups: readonly EntryActionGroup[];
}>;
export type HomeSelectionError = Readonly<{code: string; retryable: boolean}>;
export type HomeSelectionProgress = Readonly<{phase: 'preparing' | 'working'; done: number; total: number; totalKnown: boolean}>;
export type HomeSelectionItemResult = Readonly<{id: string; ok: boolean; code?: string}>;
export type HomeSelectionActionState = Readonly<{
  phase: 'idle' | 'working' | 'complete' | 'partial' | 'error' | 'uncertain' | 'cancelled';
  action: SelectionAction | EntryActionId | null;
  progress: HomeSelectionProgress | null;
  results: readonly HomeSelectionItemResult[];
  error: HomeSelectionError | null;
  /** Actual durable jobs, retained when observation is cancelled. */
  jobIds: readonly string[];
}>;
export type HomeSelectionPickerRow = Readonly<{
  id: string;
  name: string;
  count: number;
  revision: number;
  canAdd: boolean;
  /** A fact proved by this controller's receipt, never inferred from a page. */
  alreadyIn?: number;
  artwork: readonly ContentEntry[];
}>;
export type HomeSelectionPicker = Readonly<{
  kind: 'playlist' | 'collection';
  /** All genuine jobs, including confirmed earlier rounds of a partial retry. */
  jobs: readonly Job[];
  /** Server DISTINCT summary; job.done never stands in for newly added. */
  receipt: HomeDestinationReceipt | null;
  query: string;
  phase: 'loading' | 'ready' | 'adding' | 'added' | 'partial' | 'error' | 'uncertain';
  rows: readonly HomeSelectionPickerRow[];
  next: boolean;
  previous: boolean;
  destination: (HomeSelectionDestination & Pick<HomeSelectionPickerRow, 'name' | 'artwork' | 'count' | 'revision' | 'canAdd'>) | null;
  error: HomeSelectionError | null;
  results: readonly HomeSelectionItemResult[];
}>;
export type HomeSelectionEditor = Readonly<{
  phase: 'loading' | 'ready' | 'saving' | 'result' | 'conflict' | 'error' | 'uncertain';
  preview: BulkMetadataPreview | null;
  draft: Pick<BulkEdit, 'fields' | 'lists' | 'genres' | 'lockEdited'>;
  receipts: readonly BulkReceipt[];
  error: HomeSelectionError | null;
}>;
export type HomeSelectionDelete = Readonly<{
  phase: 'loading' | 'ready' | 'deleting' | 'result' | 'blocked' | 'limit' | 'error' | 'uncertain';
  preview: DeletePreview | null;
  confirmation: string;
  removeFiles: boolean;
  result: DeleteResult | null;
  error: HomeSelectionError | null;
}>;
export type HomeSelectionSnapshot = Readonly<{
  context: SelectionContext;
  selection: SelectionSnapshot;
  displayMembers: readonly HomeSelectionMember[];
  actions: SelectionActionRules;
  menu: HomeSelectionMenu | null;
  picker: HomeSelectionPicker | null;
  editor: HomeSelectionEditor | null;
  deletion: HomeSelectionDelete | null;
  rating: Readonly<{
    phase: 'loading' | 'ready' | 'saving' | 'result' | 'error' | 'uncertain';
    /** Undefined only while the genuine personal state is being loaded. */
    currentValue: number | null | undefined;
    value: number | null | undefined;
    error: HomeSelectionError | null;
  }> | null;
  mixedEdit: boolean;
  editExplanation: SelectionActionReason | null;
  action: HomeSelectionActionState;
}>;
export type HomeSelectionWindowInput = Readonly<{
  id: string;
  row: HomeRow;
  sectionOrder: number;
  /** Exact bounded subset of this row; omitted means its returned page. */
  entries?: readonly ContentEntry[];
  channels?: readonly HomeChannel[];
  start?: number;
}>;
export type HomeSelectionDocument = HomeDocument;
