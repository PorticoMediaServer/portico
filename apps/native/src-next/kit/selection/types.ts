import type React from 'react';
import type {RepairFieldSpec} from '../../../../../packages/client-core/src/metadata-repair';
import type {BulkReceipt} from '../../../../../packages/client-core/src/metadata-bulk';
import type {DeletePreview, DeleteResult} from '../../../../../packages/client-core/src/administration';
import type {SurfaceProps} from '../overlays/types';
import type {IconName} from '../Icon';

export type SelectionSurfaceProps = Pick<SurfaceProps, 'open' | 'onOpenChange' | 'initialFocusRef' | 'returnFocusRef' | 'testID'>;
export type SelectionAction = 'play' | 'queue' | 'playlist' | 'myList' | 'watched' | 'unwatched' | 'collection' | 'favorite' | 'removeMyList' | 'download' | 'edit' | 'refresh' | 'delete' | 'playNext' | 'playLater' | 'more';
export type SelectionActionOffer = {id: SelectionAction; disabled?: boolean; reason?: string; /** Disabled whole-query explanation only; grants no executor. */ wholeQueryUnavailable?: true; /** Actual caller-owned operation detail; never an invented estimate. */ description?: string};
export type SelectionBarProps = {
  /** Caller-owned authority and surface identity, forwarded to each native control. */
  testID?: string;
  count: number;
  subtitle?: string;
  /** Ready whole-view label, only when the host has a matching executor.
   * Explicit server refusals use the count-free unavailable offer below. */
  selectAllLabel?: string;
  /** Count-free unavailable whole-view control; carries no callback. */
  selectAllUnavailable?: Readonly<{label: string; reason: string}>;
  onSelectAll?: () => void;
  onCancel: () => void;
  onAction: (action: SelectionAction) => void;
  actions: readonly SelectionActionOffer[];
  menu: 'queue' | 'watched' | 'more' | null;
  onMenuChange: (menu: 'queue' | 'watched' | 'more' | null) => void;
  ownerTag?: React.ReactNode;
  topInset?: number;
  menuTitle?: string;
  /** Localized actual selected kind/count, e.g. 4 movies; generic titles when absent. */
  selectionLabel?: string;
  /** Episode selections put Watched in the primary slot and My List in More. */
  fourthSlot?: 'myList' | 'watched';
};
export type SelectionOutcome = {id: string; title: string; ok: boolean; reason?: string; artwork?: React.ReactNode};
export type PickerEntry = {id: string; title: string; size: number; canAdd: boolean; alreadyIn?: number; artwork?: React.ReactNode};
export type PickerFailure = Readonly<{id: string; title: string; reason: string}>;
/** Counters describe actual membership writes; processed does not imply inserted. */
export type PickerResult = Readonly<{
  destination: Readonly<{id: string; title: string; size?: number; artwork?: React.ReactNode}>;
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
export type PickerProps = SelectionSurfaceProps & {
  count: number;
  selectionLabel: string;
  entries: readonly PickerEntry[];
  focusedEntryId?: string;
  nameInput?: React.ReactNode;
  hasNext?: boolean;
  hasPrevious?: boolean;
  onNext?: () => void;
  onPrevious?: () => void;
  loading?: boolean;
  error?: string;
  query: string;
  onQueryChange: (query: string) => void;
  showFind?: boolean;
  creating: boolean;
  onCreatingChange: (creating: boolean) => void;
  name: string;
  onNameChange: (name: string) => void;
  onCreateAndAdd: (name: string) => void;
  onAdd: (id: string) => void;
  onRetry: (failedIds: readonly string[]) => void;
  /** Present only for a genuine uncertain original operation. */
  onRetryOperation?: () => void;
  /** Present only for a genuine read or confirmed command error on the retained picker. */
  onRetryError?: () => void;
  pending?: boolean;
  result?: PickerResult;
};
export type BulkListValue = Readonly<{value: string; count: number}>;
/** Structural projection of the actual bulk metadata preview, including bounded examples. */
export type BulkFieldValue = Readonly<{
  truncated: boolean;
  value?: string;
  values: readonly BulkListValue[];
  entries: readonly BulkListValue[];
} & ({state: 'shared' | 'mixed'; distinctCount: number; atLeast?: never}
  | {state: 'many'; distinctCount?: never; atLeast: number; value?: never})>;
/** A presentation of latest target outcomes, never a new operation receipt. */
export type BulkResultView = Pick<BulkReceipt, 'results' | 'updated' | 'failed'>;
export type BulkFieldChange = {value: string} | {add: readonly string[]; remove: readonly string[]};
export type BulkChanges = Readonly<Record<string, BulkFieldChange>>;
export type BulkEditorProps = SelectionSurfaceProps & {
  title: string;
  count: number;
  saveLabel: string;
  schema: readonly RepairFieldSpec[];
  values: Readonly<Record<string, BulkFieldValue>>;
  changes: BulkChanges;
  onChangesChange: (changes: BulkChanges) => void;
  lockEdited: boolean;
  onLockEditedChange: (lockEdited: boolean) => void;
  /** Caller validates/fences the real payload and submits only explicit changes. */
  onSave: () => void;
  pending?: boolean;
  error?: string;
  result?: BulkResultView;
  resultEntries: readonly {id: string; title: string; artwork?: React.ReactNode}[];
  onRetry: (failedIds: readonly string[]) => void;
  ownerHeader: React.ReactNode;
  artwork?: React.ReactNode;
  /** Actual complete registry (or exact excludedFields); only !bulk fields are described. */
  singleItemFields: readonly RepairFieldSpec[];
  /** Localized server-provided field labels/values; presentation never invents a schema. */
  fieldLabel: (field: RepairFieldSpec) => string;
  fieldValueLabel?: (field: RepairFieldSpec, value: string) => string;
  fieldOptions?: Readonly<Record<string, readonly {value: string; label: string}[]>>;
  fieldErrors?: Readonly<Record<string, string>>;
  summary: readonly {id: string; text: string}[];
};
export type MixedEditExplainerProps = SelectionSurfaceProps & {
  description: string;
  total: number;
  groups: readonly {kind: string; label: string; count: number; icon: IconName}[];
  onKeepOnly: (kind: string) => void;
};
export type DeleteMediaDialogProps = SelectionSurfaceProps & {
  ownerHeader: React.ReactNode;
  owner: boolean;
  selectionCount: number;
  preview: DeletePreview | null;
  loading?: boolean;
  error?: string;
  result?: DeleteResult;
  confirmation: string;
  onConfirmationChange: (value: string) => void;
  removeFiles: boolean;
  onRemoveFilesChange: (value: boolean) => void;
  onDelete: (preview: DeletePreview, confirmation: string, removeFiles: boolean) => void;
  pending?: boolean;
  renderArtwork?: (id: string) => React.ReactNode;
};
