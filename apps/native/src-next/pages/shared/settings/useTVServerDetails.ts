import {useEffect, useState} from 'react';
import {captureReportContext} from '../../../../../../packages/client-core/src/report-context';
import type {NativeShellRuntime} from '../../../shell/runtime';
import type {NativeScreenLifetime} from '../../../screens/settings-live/lifetime';
import type {NativeSettingsLivePorts} from '../../../screens/settings-live/ports';
import {createTVServerReader} from '../../../screens/settings-live/server/tv-server-reader';
import {readTVServerPage, type TVReadOnlyPage, type TVPageSnapshot} from '../../../screens/settings-live/server/tv-server-page-data';

/** Each opened TV details panel reads the current owner's display facts. */
export function useTVServerDetails({page, ports, runtime, lifetime}: {
  page: TVReadOnlyPage; ports: NativeSettingsLivePorts; runtime: NativeShellRuntime; lifetime: NativeScreenLifetime;
}) {
  const {t} = runtime.text;
  const [snapshot, setSnapshot] = useState<TVPageSnapshot>(), [error, setError] = useState<unknown>();
  const [revision, setRevision] = useState(0), [cursors, setCursors] = useState<readonly string[]>(['']);
  const cursor = cursors.at(-1) ?? '';
  // Each mounted page owns a fixed report timestamp.
  const [report] = useState(() => captureReportContext(Intl.DateTimeFormat().resolvedOptions().timeZone || 'UTC'));
  useEffect(() => {
    const abort = new AbortController(), api = createTVServerReader(ports.context, lifetime);
    const current = () => !abort.signal.aborted && lifetime.current();
    setSnapshot(undefined);setError(undefined);
    const load = async () => {
      const value = await readTVServerPage(page, api, current, report, cursor, lifetime.authority.serverId, t, runtime.text.locale ?? 'en-US', abort.signal);
      if (value.nextCursor && (value.nextCursor === cursor || cursors.includes(value.nextCursor))) throw new Error('invalid_play_history_continuation');
      return value;
    };
    void load().then(value => {if (current()) setSnapshot(value);}, failure => {if (current()) setError(failure);});
    return () => abort.abort();
  }, [page, ports, lifetime, runtime.text, t, revision, cursor, cursors, report]);
  const retry = () => setRevision(value => value + 1);
  const previous = () => setCursors(value => value.length > 1 ? value.slice(0, -1) : value);
  const next = () => {
    const nextCursor = snapshot?.nextCursor;
    if (nextCursor && lifetime.current()) setCursors(value => value.includes(nextCursor) ? value : [...value, nextCursor]);
  };
  const empty = t(page === 'history' ? 'next.settingsSpec.server.history.groups.0.rows.0.control.empty'
    : page === 'remote-sources' ? 'next.remoteSources.empty' : page === 'live' ? 'web.live.empty.noSourcesTitle' : 'error.unavailable');
  return {t, snapshot, error, retry, empty, previous: cursors.length > 1 ? previous : undefined, next: snapshot?.nextCursor ? next : undefined};
}
