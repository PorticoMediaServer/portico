import {createWindowedCollection} from '@core/collections/index.ts';
import type {DetailCredit, DetailProjection} from '@core/detail.ts';
import type {DetailBindings} from './bindings';

/** Cursor addresses are small; private credit rows live only in five resident pages. */
export function titleCreditsWindow(binding: DetailBindings, data: DetailProjection, group: 'cast' | 'crew') {
  const pageSize = 40, cursors = new Map<number, string | undefined>([[0, undefined]]);
  let addressablePage = 0;
  return createWindowedCollection<DetailCredit>({
    pageSize, maxConcurrent: 1, maxResidentPages: 5, keyOf: credit => credit.id,
    async fetchPage(start, _count, signal) {
      binding.current(data); signal.throwIfAborted();
      const page = Math.floor(start / pageSize);
      if (!cursors.has(page)) throw new DOMException('Credit cursor unavailable', 'AbortError');
      const value = await binding.readCredits(data, group, cursors.get(page), signal);
      binding.current(data); signal.throwIfAborted();
      if (value.credits.length > pageSize) throw new Error('Credit page exceeded its requested bound');
      if (value.nextCursor) {
        if ([...cursors].some(([address, cursor]) => address !== page + 1 && cursor === value.nextCursor)) throw new Error('Repeated credit cursor');
        cursors.set(page + 1, value.nextCursor);
        addressablePage = Math.max(addressablePage, page + 1);
      }
      const total = value.nextCursor ? Math.min(value.total, (addressablePage + 1) * pageSize) : value.total;
      return {items: value.credits, total, totalIsLowerBound: total < value.total};
    },
  });
}
