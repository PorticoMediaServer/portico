import type {ReactNode} from 'react';
import type {BrowseFieldCapability, BrowseQuickFilter, BrowseSortCapability} from '@core/browse';
import type {AppliedFilterModel} from '@core/presentation/kit-model';

export type BrowsePlatform = 'desktop' | 'phone' | 'tv';
export type BrowseLabels = Readonly<{
  field: (field: BrowseFieldCapability) => string;
  sort: (sort: BrowseSortCapability) => string;
  quick: (quick: BrowseQuickFilter) => string;
}>;
export type AppliedChip = Readonly<{id: string; parts: AppliedFilterModel}>;
export type BrowseView = Readonly<{value: 'grid' | 'list'; onValueChange: (value: 'grid' | 'list') => void}>;
export type CardSizeChoice = Readonly<{value: string; label: ReactNode}>;
export type CardSizeControl = Readonly<{value: string; options: readonly CardSizeChoice[]; onValueChange: (value: string) => void}>;
export type SaveViewControl = Readonly<{canSave: boolean; onSave: () => void}>;
export type FacetValue = Readonly<{value: string; label: string; count?: number; countIsLowerBound?: boolean}>;
export type FacetSelection = 'include' | 'exclude' | 'clear';
export type FilterMatch = 'any' | 'all' | 'none';
export type FilterValueState = Readonly<{
  kind: 'values'; values: readonly FacetValue[]; included: readonly string[]; excluded: readonly string[];
  match: FilterMatch; query: string; moreLabel?: string; hint?: string; total?: number; status?: 'loading' | 'error' | 'ready'; error?: ReactNode;
}>;
export type FilterChoiceState = Readonly<{kind: 'choice' | 'range'; options: readonly Readonly<{value: string; label: string}>[]; value: string | undefined}>;
export type FilterToggleState = Readonly<{kind: 'toggle'; value: boolean | undefined}>;
export type FilterTextState = Readonly<{kind: 'text'; value: string; placeholder?: string}>;
export type FilterFieldState = (FilterValueState | FilterChoiceState | FilterToggleState | FilterTextState) & Readonly<{summary?: string}>;
export type FilterFieldChange = Readonly<{kind: 'facet'; value: string; selection: FacetSelection}> |
  Readonly<{kind: 'match'; value: FilterMatch}> | Readonly<{kind: 'choice'; value: string | undefined}> |
  Readonly<{kind: 'toggle'; value: boolean | undefined}> | Readonly<{kind: 'text'; value: string}> | Readonly<{kind: 'clear'}>;
