import {createWindowedCollection} from '@core/collections/index.ts';

/** Cursor pages append only when approached. Rendering stays proportional to the viewport. */
export function appendedWindow<T>(keyOf: (item: T) => string) {
  let items: readonly T[] = [];
  const fetchPage = async (start: number, count: number) => ({items: items.slice(start, start + count), total: items.length});
  const collection = createWindowedCollection<T>({fetchPage, pageSize: 40, maxResidentPages: 5, keyOf});
  return {collection, set(next: readonly T[]) { items = next; collection.invalidate(fetchPage); }, dispose() { items = []; collection.dispose(); }};
}
