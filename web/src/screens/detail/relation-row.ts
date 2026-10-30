import {useEffect, useState, useSyncExternalStore} from 'react';
import type {ContentEntry} from '@core/library-content.ts';
import type {RelationRowPager} from '@core/relation-rows.ts';
import {screenLifetime, type ScreenContext} from './context';

type Api = ReturnType<typeof screenLifetime>['api'];
type Pagination = Readonly<{cursor: string; loading: boolean; onMore: () => void}>;
const idle = () => () => undefined;

/**
 * A relation row read to its end (JUSTIN-ANSWERS #34): the first page as its page returned it,
 * then each later page as the viewer nears the row's end. The pager belongs to one first page
 * (`first`, a stable snapshot value) on one captured screen, and is disposed with it.
 */
export function useRelationRow(context: ScreenContext, first: object | undefined, entries: readonly ContentEntry[], create: (api: Api) => RelationRowPager | undefined): Readonly<{entries: readonly ContentEntry[]; pagination?: Pagination}> {
  const [made, setMade] = useState<Readonly<{first: object; pager?: RelationRowPager}>>();
  useEffect(() => {
    if (!first) return;
    const life = screenLifetime(context), pager = create(life.api);
    setMade({first, pager});
    return () => { pager?.dispose(); life.dispose(); };
    // The pager is the first page's: `create` only reads what `first` already names.
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [context, first]);
  const pager = made?.first === first ? made?.pager : undefined;
  const snapshot = useSyncExternalStore(pager?.subscribe ?? idle, () => pager?.getSnapshot(), () => pager?.getSnapshot());
  if (!snapshot) return {entries};
  return {entries: snapshot.entries, ...(snapshot.cursor ? {pagination: {cursor: snapshot.cursor, loading: snapshot.loading, onMore: () => { void pager?.more(); }}} : {})};
}
