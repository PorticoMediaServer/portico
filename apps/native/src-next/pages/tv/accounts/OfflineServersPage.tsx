import React from 'react';
import {defaultI18n} from '../../../../../../packages/i18n/src';
import {TVAccountLinks, TVAccountWidePage, TVRetryNote, TVServerList} from '../../../kit/accounts/TVNoServerParts';
import {Button} from '../../../kit/controls';
import {TVAccountRow} from '../../../kit/controls/TVAccountRow';
import {FocusProvider} from '../../../kit/focus';
import {useTVOfflineServers, type TVOfflineServersProps} from '../../shared/accounts/useTVOfflineServers';

const t = defaultI18n.t;

/** Every server of a signed-in Portico Account not answering, on a TV (connect 37): each server with when it was last seen,
 * Owner on the viewer's own, and its own Try again; the countdown to the next automatic check and what to look at. Focus
 * starts on the first server's Try again. */
export function OfflineServersPage(props: TVOfflineServersProps) {
  const view = useTVOfflineServers(props), {busy} = props;
  const rows = 'accounts.offline.rows', foot = 'accounts.offline.foot';
  return <FocusProvider><TVAccountWidePage testID="tv-offline-servers" kicker={t('web.chooser.offlineWhy')}
    title={t('accounts.tvOffline.title')} body={t('accounts.tvOffline.body')}>
    <TVServerList region={{id: rows, entry: view.first ? `retry:${view.first.id}` : undefined, enabled: !busy, edges: {down: foot}}}>
      {view.rows.map(row => <TVAccountRow key={row.id} title={row.name} detail={row.seen} icon="i-server"
        badge={row.owner ? t('accounts.settings.owner') : undefined}>
        <Button focusId={`retry:${row.id}`} variant="glass" icon="i-retry" disabled={busy} loading={row.loading || undefined}
          onPress={row.retry}>{t('web.chooser.tryAgain')}</Button>
      </TVAccountRow>)}
    </TVServerList>
    <TVRetryNote countdown={view.countdown} progress={props.retryProgress} help={t('accounts.tvOffline.help')}/>
    {/* Refresh servers reads the account's list again, so a server added or removed on the account shows up. */}
    <TVAccountLinks region={{id: foot, entry: 'refresh', order: ['refresh'], edges: view.first ? {up: rows} : undefined, enabled: !busy}}>
      <Button focusId="refresh" variant="link" disabled={busy} onPress={view.refresh}>{t('accounts.servers.refresh')}</Button>
    </TVAccountLinks>
  </TVAccountWidePage></FocusProvider>;
}
