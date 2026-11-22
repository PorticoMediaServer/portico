import {useEffect, useState} from 'react';
import {captureReportContext} from '../../../../../../packages/client-core/src/report-context';
import type {NativeShellRuntime} from '../../../shell/runtime';
import type {NativeScreenLifetime} from '../../../screens/settings-live/lifetime';
import type {NativeSettingsLivePorts} from '../../../screens/settings-live/ports';
import {createTVServerReader} from '../../../screens/settings-live/server/tv-server-reader';
import {readTVServerPage, type TVReadOnlyPage, type TVPageSnapshot} from '../../../screens/settings-live/server/tv-server-page-data';

/** A TV server Settings page shown read-only: display facts only, through the GET-only Settings reader, a page at a time. */
export function useTVServerReadOnly({page, ports, runtime, lifetime}: {
  page: TVReadOnlyPage; ports: NativeSettingsLivePorts; runtime: NativeShellRuntime; lifetime: NativeScreenLifetime;
}) {
  const t = runtime.text.t;
  const [snapshot, setSnapshot] = useState<TVPageSnapshot>(), [error, setError] = useState<unknown>();
  const [revision, setRevision] = useState(0), [cursors, setCursors] = useState<readonly string[]>(['']);
  const cursor = cursors.at(-1) ?? '';
  // Each mounted page owns a fixed report timestamp.
  const [report] = useState(() => captureReportContext(Intl.DateTimeFormat().resolvedOptions().timeZone || 'UTC'));
  useEffect(() => {
    const abort = new AbortController(), api = createTVServerReader(ports.context, lifetime);
    const current = () => !abort.signal.aborted && lifetime.current();
    setSnapshot(undefined);
    setError(undefined);
    const load = async () => {
      const locale = runtime.text.locale ?? 'en-US';
      const value = await readTVServerPage(page, api, current, report, cursor, lifetime.authority.serverId, t, locale, abort.signal);
      if (value.nextCursor && (value.nextCursor === cursor || cursors.includes(value.nextCursor))) throw new Error('invalid_play_history_continuation');
      return value;
    };
    void load().then(value => {if (current()) setSnapshot(value);}, failure => {if (current()) setError(failure);});
    return () => abort.abort();
  }, [page, ports, lifetime, runtime.text, t, revision, cursor, cursors, report]);
  const empty = t(page === 'history' ? 'next.settingsSpec.server.history.groups.0.rows.0.control.empty'
    : page === 'remote-sources' ? 'next.remoteSources.empty' : page === 'live' ? 'web.live.empty.noSourcesTitle' : 'error.unavailable');
  const nextCursor = snapshot?.nextCursor;
  return {
    snapshot, error, empty,
    retry: () => setRevision(value => value + 1),
    previous: cursors.length > 1 ? () => setCursors(value => value.slice(0, -1)) : undefined,
    next: nextCursor ? () => setCursors(value => [...value, nextCursor]) : undefined,
  };
}
