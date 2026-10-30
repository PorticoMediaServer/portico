import type {ReactNode, RefObject} from 'react';
import type {RepairFieldSpec} from '@core/metadata-repair';
import type {BulkReceipt} from '@core/metadata-bulk';
import type {DeletePreview, DeleteResult} from '@core/administration';
import type {SelectionKind} from '@core/selection-model';
import type {OverlayChange, OverlaySurfaceProps} from '../overlays';

export type SelectionMediaKind = SelectionKind;
export type SelectionItemDisplay = Readonly<{id: string; title: string; artwork?: ReactNode; subtitle?: string}>;
export type SelectionCount = Readonly<{count: number; kind?: SelectionMediaKind}>;
export type SelectionTypeCount = Readonly<{kind: SelectionMediaKind; count: number}>;
export type SelectionSurface = Readonly<{open: boolean; onOpenChange: OverlayChange; returnFocusRef?: RefObject<HTMLElement | null>}>;

export type SelectionActions = Readonly<{
  onPlay?: () => void;
  playDisabledReason?: string;
  queueDisabledReason?: string;
  playlistDisabledReason?: string;
  editDisabledReason?: string;
  onQueue?: (placement: 'next' | 'end') => void;
  onPlaylist?: (anchor: HTMLElement) => void;
  onMyList?: () => void;
  onWatchedChange?: (watched: boolean) => void;
  onEdit?: (anchor: HTMLElement) => void;
  editMode?: 'editable' | 'mixed';
  onCollection?: (anchor: HTMLElement) => void;
  onFavorite?: () => void;
  onRemoveFromMyList?: () => void;
  onRefresh?: () => void;
  onDelete?: () => void;
  deleteDisabledReason?: string;
  /** No query-download producer: retain a truthful unavailable control and reason. */
  downloadDisabledReason?: string;
  /** Explanatory dialog is reachable; this description does not disable its trigger. */
  deleteExplanation?: string;
  ownerMark?: ReactNode;
}>;
export type SelectionBarProps = Readonly<{
  selection: SelectionCount;
  actions: SelectionActions;
  context?: string;
  typeCount?: number;
  /** An explicit unavailable view carries a reason, never a guessed total or executor. */
  selectAll?: {kind: 'view'; total: number} | {kind: 'row'} | {kind: 'clear'} | {kind: 'unavailable'; reason: string};
  onSelectAll?: () => void;
  onExit: () => void;
  allInView?: {visible: number; scope?: 'library' | 'view'};
  sticky?: boolean;
  className?: string;
}>;

/** Aggregates are supplied by the caller's real schema/preview, never read or guessed by the kit. */
export type BulkFieldValues = Readonly<{
  truncated: boolean;
  value?: string;
  values: readonly Readonly<{value: string; count: number; titles?: readonly string[]}>[];
  entries: readonly Readonly<{value: string; count: number}>[];
} & ({state: 'shared' | 'mixed'; distinctCount: number; atLeast?: never}
  | {state: 'many'; distinctCount?: never; atLeast: number; value?: never})>;
/** A presentation of latest target outcomes, never a new operation receipt. */
export type BulkResultView = Pick<BulkReceipt, 'results' | 'updated' | 'failed'>;
export type BulkListDraft = Readonly<{add: readonly string[]; remove: readonly string[]}>;
export type BulkEditorDraft = Readonly<{
  fields: Readonly<Record<string, string>>;
  lists: Readonly<Record<string, BulkListDraft>>;
  lockEdited: boolean;
}>;
export type BulkEditorProps = SelectionSurface & Readonly<{
  selection: SelectionCount & {kind: SelectionMediaKind};
  schema: readonly RepairFieldSpec[];
  /** Actual complete registry (or exact excludedFields); only !bulk fields are described. */
  singleItemFields: readonly RepairFieldSpec[];
  values: Readonly<Record<string, BulkFieldValues>>;
  draft: BulkEditorDraft;
  onDraftChange: (draft: BulkEditorDraft) => void;
  /** Optional caller-owned controls attached to an actual offered field. */
  fieldAdditions?: Readonly<Record<string, ReactNode>>;
  onSave: (draft: BulkEditorDraft) => void;
  onRetry: (failedTargetIds: readonly string[], draft: BulkEditorDraft) => void;
  /** Include a title/art projection for every returned result id; never display internal ids. */
  targets: readonly SelectionItemDisplay[];
  resultDescriptions?: Readonly<Record<string, string>>;
  result?: BulkResultView;
  busy?: boolean;
  error?: string;
  ownerTag: ReactNode;
  /** Exact caller-projected summary/description when available, including per-track wording. */
  description?: string;
}>;

export type PickerEntry = Readonly<{id: string; name: string; size: number; canAdd: boolean; alreadyIn?: number; artwork?: ReactNode}>;
export type PickerFailure = Readonly<{id: string; title: string; reason: string}>;
/** Counters describe actual membership writes; processed does not imply inserted. */
export type PickerResult = Readonly<{
  destination: Readonly<{id: string; name: string; size?: number; artwork?: ReactNode}>;
  inserted?: number;
  processed?: number;
  failed?: number;
  unknown?: number;
  pending?: boolean;
  total?: number;
  alreadyIn?: number;
  failures: readonly PickerFailure[];
  complete: boolean;
  jobFacts?: readonly Readonly<{id: string; text: string}>[];
}>;
export type SelectionPickerProps = SelectionSurface & Pick<OverlaySurfaceProps, 'initialFocusRef'> & Readonly<{
  selection: SelectionCount;
  anchorRef: RefObject<HTMLElement | null>;
  entries: readonly PickerEntry[];
  query?: string;
  onQueryChange?: (query: string) => void;
  hasNext?: boolean;
  hasPrevious?: boolean;
  onNext?: () => void;
  onPrevious?: () => void;
  loading?: boolean;
  mode: 'choose' | 'new';
  onModeChange: (mode: 'choose' | 'new') => void;
  name: string;
  onNameChange: (name: string) => void;
  onAdd: (destinationId: string) => void;
  onCreateAndAdd: (name: string) => void;
  onRetry?: (missingIds: readonly string[]) => void;
  /** Present only for a genuine uncertain original operation, not a failed-target retry. */
  onRetryOperation?: () => void;
  /** Present only for a genuine read or confirmed command error on the retained picker. */
  onRetryError?: () => void;
  result?: PickerResult;
  busy?: boolean;
  error?: string;
}>;

export type DeleteMediaDialogProps = SelectionSurface & Readonly<{
  owner: boolean;
  selectionCount: number;
  preview: DeletePreview | null;
  confirmation: string;
  onConfirmationChange: (value: string) => void;
  removeFiles: boolean;
  onRemoveFilesChange: (remove: boolean) => void;
  onDelete: (input: {preview: DeletePreview; confirmation: string; deleteFiles: boolean}) => void;
  busy?: boolean;
  error?: string;
  result?: DeleteResult;
  ownerTag: ReactNode;
  artwork?: Readonly<Record<string, ReactNode>>;
  targetFacts?: Readonly<Record<string, string>>;
}>;
