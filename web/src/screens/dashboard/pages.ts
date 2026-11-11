import type {DashboardBindings, DashboardSessions} from './bindings';

/** The drawing shows the session stack without an additional paging control.
 * Follow only the producer's opaque continuation; every page retains the
 * binding's current-authority, scope and observed-time checks. Now playing is
 * a moving read, separate from the aggregate ReportContext. */
export async function readNowPlaying(bindings: {read: Pick<DashboardBindings['read'], 'sessions'>}, signal: AbortSignal): Promise<DashboardSessions> {
  const items = new Map<string, DashboardSessions['items'][number]>(), seen = new Set<string>();
  let page: DashboardSessions, cursor: string | undefined;
  do {
    signal.throwIfAborted();
    if (cursor) {if (seen.has(cursor)) throw new Error('invalid_dashboard_session_cursor'); seen.add(cursor);}
    page = await bindings.read.sessions(cursor, signal);
    signal.throwIfAborted();
    for (const session of page.items) items.set(session.id, session);
    cursor = page.nextCursor;
  } while (cursor);
  return {...page, items: [...items.values()]};
}

/** Every account, for the person filters. */
export async function readDashboardAccounts(bindings: {read: Pick<DashboardBindings['read'], 'accounts'>}, signal: AbortSignal) {
  signal.throwIfAborted();
  const items = await bindings.read.accounts(signal);
  signal.throwIfAborted();
  return items;
}
