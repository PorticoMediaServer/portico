import type {ContentEntryKind} from '@core/library-content.ts';
import type {BrowseSelectionContext, QuerySelectionTarget} from '@core/selection-model/index.ts';
/** A title Delete takes, with its library. */
export type QueryDeleteTarget = Readonly<{itemId: string; libraryId: string; kind: ContentEntryKind}>;
/** Only explicit chosen titles have a rebuilt Delete route. A whole view is never enumerated here. */
export const queryDeleteTarget = <C,>(_target: QuerySelectionTarget<C>): boolean => false;
export const resolveQueryDelete = async (_target: QuerySelectionTarget<BrowseSelectionContext>, _ports: unknown, _signal: AbortSignal): Promise<readonly QueryDeleteTarget[]> => {
  throw new Error('query-delete-unavailable');
};
