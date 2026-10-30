import {useCallback, useState} from 'react';
import type {DashboardBindings, DashboardReportQuery} from './bindings';
import {useDashboardRead} from './use-read';

const reportChanges = ['playback.history', 'stream.resync'];
const initialQuery: DashboardReportQuery = {period: '30d', window: 'live', peoplePeriod: '7d', topPeriod: '30d', topKind: 'movies', peopleLimit: 100};

/** All aggregate panels consume one checked producer response. The shell owns capture/refresh. */
export function useDashboardReport(bindings: DashboardBindings, enabled = true) {
  const [query, setQuery] = useState(initialQuery);
  const load = useCallback((signal: AbortSignal) => bindings.read.report(query, signal), [bindings, query]);
  const state = useDashboardRead(bindings, load, enabled, 0, reportChanges);
  const change = useCallback((patch: Partial<DashboardReportQuery>) => setQuery(previous => ({...previous, ...patch})), []);
  return {query, change, state};
}
export type DashboardReportController = ReturnType<typeof useDashboardReport>;
