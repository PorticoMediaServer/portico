import type {ReactNode} from 'react';
import type {BrowseFieldCapability, BrowseQuickFilter, BrowseSortCapability, BrowseSortSelection} from '../../../../../packages/client-core/src/browse';
import type {AppliedFilterModel, BrowseToolbarModel, FactPart} from '../../../../../packages/client-core/src/presentation/kit-model';

export type BrowsePlatform = 'desktop' | 'phone' | 'tv';
export type BrowseLabels = Readonly<{
  field: (field: BrowseFieldCapability) => string;
  sort: (sort: BrowseSortCapability) => string;
  quick: (quick: BrowseQuickFilter) => string;
}>;
export type LibraryChoice = Readonly<{id: string; name: string; count?: ReactNode; tv?: boolean}>;
export type LibraryHeaderProps = Readonly<{
  name: string; counts?: readonly FactPart[]; switcher: ReactNode;
  menu?: ReactNode; ambient?: ReactNode; children?: ReactNode; testID?: string;
}>;
export type AppliedFilterChip = Readonly<{id: string; parts: AppliedFilterModel}>;
export type BrowseToolbarProps = Readonly<{
  model: BrowseToolbarModel; platform?: BrowsePlatform; sticky?: boolean;
  resultCount: ReactNode;
  onQuickChange: (id: string, checked: boolean) => void; onFilters: () => void;
  /** True while the caller's Filters surface is open, so the chip reads as open. */
  filtersOpen?: boolean;
  onSortChange: (sort: BrowseSortSelection) => void; onReshuffle?: () => void;
  labels: BrowseLabels; applied?: readonly AppliedFilterChip[];
  onRemoveFilter?: (id: string) => void; onClearFilters?: () => void;
  view?: Readonly<{value: 'grid' | 'list'; onValueChange: (value: 'grid' | 'list') => void}>;
  cardSize?: Readonly<{value: string; options: readonly Readonly<{value: string; label: string}>[]; onValueChange: (value: string) => void}>;
  save?: Readonly<{canSave: boolean; onSave: () => void}>;
  onSelect?: () => void;
  onJump?: () => void; selection?: ReactNode; children?: ReactNode; testID?: string;
}>;

export type FacetValue = Readonly<{value: string; label: string; count?: number; countIsLowerBound?: boolean; disabled?: boolean}>;
export type MatchModeValue = 'any' | 'all' | 'none';
export type FilterValueSelection = Readonly<{kind: 'values'; included: readonly string[]; excluded: readonly string[]; mode: MatchModeValue}>;
export type FilterFieldValue = FilterValueSelection
  | Readonly<{kind: 'choice'; value: string | null}>
  | Readonly<{kind: 'toggle'; value: boolean | null}>
  | Readonly<{kind: 'range'; value: string | null}>
  | Readonly<{kind: 'text'; value: string}>;
type FieldPresentation = Readonly<{
  summary?: ReactNode; active?: boolean; help?: ReactNode;
  status?: 'ready' | 'loading' | 'error'; statusLabel?: ReactNode;
  /** A bounded server page's total, never inferred from the loaded values. */
  total?: number; search?: string; searchLabel?: string; showAllLabel?: ReactNode; preferredFocusValue?: string;
}>;
export type FilterFieldState = FieldPresentation & (
  | (FilterValueSelection & Readonly<{values: readonly FacetValue[]; allowAll: boolean; cycleExclusion?: boolean; showMatchMode?: boolean}>)
  | Readonly<{kind: 'choice'; value: string | null; options: readonly Readonly<{value: string; label: string; disabled?: boolean}>[]}>
  | Readonly<{kind: 'toggle'; value: boolean | null}>
  | Readonly<{kind: 'range'; value: string | null; presets: readonly Readonly<{value: string; label: string}>[]}>
  | Readonly<{kind: 'text'; value: string; inputLabel?: string}>
);
export type FilterBuilderProps = Readonly<{
  model: Pick<BrowseToolbarModel, 'groups'>; labels: BrowseLabels; platform?: BrowsePlatform;
  /** Actual caller region/authority namespace; field and value identities stay semantic. */
  focusPrefix?: string;
  openFields: readonly string[]; onFieldOpenChange: (id: string, open: boolean) => void;
  fields: Readonly<Record<string, FilterFieldState | undefined>>;
  onFieldChange: (id: string, value: FilterFieldValue) => void;
  onSearchChange: (id: string, query: string) => void;
  onShowAll?: (id: string) => void; onRetry?: (id: string) => void;
  /** Owner supplies the exact rare-field grouping and summary from its capability model. */
  more?: Readonly<Partial<Record<'title' | 'activity' | 'file', Readonly<{fields: readonly string[]; summary: ReactNode; open: boolean; onOpenChange: (open: boolean) => void}>>>>;
  children?: ReactNode; testID?: string;
}>;
