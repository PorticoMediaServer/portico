import type {queryDeleteTarget as CoreDeleteTarget, resolveQueryDelete as CoreResolveDelete} from '@core/query/catalogue-query-delete.ts';
/** Only explicit chosen titles have a rebuilt Delete route. A whole view is never enumerated here. */
export const queryDeleteTarget: typeof CoreDeleteTarget = () => false;
export const resolveQueryDelete: typeof CoreResolveDelete = async () => {throw new Error('query-delete-unavailable');};
