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

/** Every option is an actual account ID. The drawn picker has no extra
 * continuation option; enumerating it does not establish an analytics total. */
export async function readDashboardAccounts(bindings: {read: Pick<DashboardBindings['read'], 'people'>}, signal: AbortSignal) {
  const items = new Map<string, Awaited<ReturnType<DashboardBindings['read']['people']>>['items'][number]>(), seen = new Set<string>();
  let cursor: string | undefined;
  do {
    signal.throwIfAborted();
    if (cursor) {if (seen.has(cursor)) throw new Error('invalid_dashboard_people_cursor'); seen.add(cursor);}
    const page = await bindings.read.people({period: 'all'}, cursor, signal);
    signal.throwIfAborted();
    for (const person of page.items) items.set(person.accountId, person);
    cursor = page.nextCursor;
  } while (cursor);
  return [...items.values()];
}
