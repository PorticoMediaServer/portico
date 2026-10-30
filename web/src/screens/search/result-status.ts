import type {SearchGroupResult} from '@core/search.ts';

export function searchResultCount(groups: readonly SearchGroupResult[]): number | undefined {
  if (!groups.length || groups.some(group => group.status !== 'success' || !group.totalCountExact)) return undefined;
  return groups.reduce((total, group) => total + group.totalCount, 0);
}

/** Retired queries cannot publish a count, even if their timer callback is already queued. */
export function scheduleSearchResultStatus(message: string, publish: (message: string) => void,
  schedule: (callback: () => void, ms: number) => unknown = setTimeout,
  cancel: (timer: unknown) => void = timer => clearTimeout(timer as ReturnType<typeof setTimeout>)): () => void {
  let current = true;
  const timer = schedule(() => {if (current) publish(message);}, 300);
  return () => {current = false;cancel(timer);};
}
