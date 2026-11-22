import {defaultI18n} from '../../../../../../packages/i18n/src';
import type {DirectoryRow} from '../../../screens/accounts/directory-chooser';
import {useOfflineServersGuard} from './useOfflineServersGuard';

export type OfflineServersProps = {rows: readonly DirectoryRow[]; scope: object | undefined; revision: number; isCurrent: () => boolean;
  onDownloads?: () => void; username?: string; onSettings?: () => void; onJoin?: () => void; onFind?: () => void;
  busy: boolean; retryIn?: number; retryProgress?: number; activeId?: string; onRetry: (id: string) => void; onBack: () => void; onRefresh: () => void};

/** Phones and tablets: every server of the signed-in Portico Account not answering, from the directory's own observations.
 * Each command belongs to the committed opening it was drawn for (useOfflineServersGuard). Moved from the old screen. */
export function useOfflineServers(props: OfflineServersProps) {
  const t = defaultI18n.t, {busy} = props;
  const guard = useOfflineServersGuard({scope: props.scope, revision: props.revision, isCurrent: props.isCurrent, busy, rowIds: props.rows.map(row => row.id)});
  return {busy,
    account: props.onSettings ? {label: props.username ? `${props.username} · ${t('settings.title')}` : t('settings.title'), onPress: guard.action(props.onSettings)!} : undefined,
    rows: props.rows.map(row => ({id: row.id, name: row.name, detail: row.subtitle ? t(row.subtitle.key, row.subtitle.values) : undefined,
      loading: busy && props.activeId === row.id, retry: () => guard.retry(row.id, props.onRetry)})),
    countdown: props.retryIn !== undefined && !busy ? t('web.chooser.checkingAgain', {seconds: props.retryIn}) : undefined,
    ways: [
      ...(props.onDownloads ? [{id: 'downloads', label: t('downloads.title'), onPress: guard.action(props.onDownloads)!}] : []),
      ...(props.onSettings ? [{id: 'account', label: t('settings.row.porticoAccount'), onPress: guard.action(props.onSettings)!}] : []),
      ...(props.onJoin ? [{id: 'join', label: t('services.start.join'), onPress: guard.action(props.onJoin)!}] : []),
      ...(props.onFind ? [{id: 'find', label: t('connection.discovery.find'), onPress: guard.action(props.onFind)!}] : []),
    ],
    refresh: guard.action(props.onRefresh)!, back: guard.action(props.onBack)!};
}
