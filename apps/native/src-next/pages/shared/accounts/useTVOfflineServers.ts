import {serverNow} from '../../../../../../packages/client-core/src/server-clock';
import {defaultI18n} from '../../../../../../packages/i18n/src';
import {useTVBusyEntryRevision} from '../../../tv/auth/entry-revision';
import {useDestinationEntryFocus} from '../../../screens/destinations/useEntryFocus';
import type {DirectoryRow} from '../../../screens/accounts/directory-chooser';
import {useOfflineServersGuard} from './useOfflineServersGuard';

export type TVOfflineServersProps = {rows: readonly DirectoryRow[]; scope: object | undefined; revision: number; isCurrent: () => boolean;
  busy: boolean; retryIn?: number; retryProgress?: number; activeId?: string; onRetry: (id: string) => void; onBack: () => void; onRefresh: () => void;
  /** The phone page's own ways out (Downloads, Settings, Join, Find); connect 37 draws none of them on a TV. */
  onDownloads?: () => void; username?: string; onSettings?: () => void; onJoin?: () => void; onFind?: () => void};

/** connect 37's same-day form: the cached time is drawn only when it falls on the viewer's own local day, in the profile's zone
 * and clock; other days keep the directory's existing last-seen wording. */
export function lastSeenText(row: DirectoryRow, now: number): string | undefined {
  if (!row.subtitle) return undefined;
  const at = row.lastSeen?.at, parsed = at ? Date.parse(at) : NaN;
  if (at && Number.isFinite(parsed)) {
    const day = (value: number) => defaultI18n.formatDate(value, {year: 'numeric', month: '2-digit', day: '2-digit'});
    if (day(parsed) === day(now)) return defaultI18n.t('web.chooser.lastSeenToday', {time: defaultI18n.time(at)});
  }
  return defaultI18n.t(row.subtitle.key, row.subtitle.values);
}

/** Every server of a signed-in Portico Account not answering, on a TV (connect 37): the directory's own observations only. The
 * parent owns one serialized connection and its retry timer; this never starts a session. A retry (a row's own, the automatic
 * one) detaches every control, so the list asks for its first row's Try again again when the run ends. */
export function useTVOfflineServers(props: TVOfflineServersProps) {
  const {rows, busy} = props;
  const guard = useOfflineServersGuard({scope: props.scope, revision: props.revision, isCurrent: props.isCurrent, busy, rowIds: rows.map(row => row.id)});
  const current = guard.current;
  const first = rows[0], now = serverNow(), entryRevision = useTVBusyEntryRevision(busy);
  useDestinationEntryFocus({visible: !busy, intent: 'enter', revision: entryRevision}, first ? {regionId: 'accounts.offline.rows', childId: `retry:${first.id}`} : undefined);
  return {first, current,
    rows: rows.map(row => ({id: row.id, name: row.name, owner: !!row.owner, seen: lastSeenText(row, now) ?? '', loading: busy && props.activeId === row.id,
      retry: () => guard.retry(row.id, props.onRetry)})),
    countdown: props.retryIn !== undefined && !busy ? defaultI18n.t('web.chooser.checkingAgain', {seconds: props.retryIn}) : undefined,
    refresh: () => {if (current()) props.onRefresh();}};
}
