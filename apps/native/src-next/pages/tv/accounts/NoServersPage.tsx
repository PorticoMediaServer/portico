import React from 'react';
import {defaultI18n} from '../../../../../../packages/i18n/src';
import {DoorTiles, TVJoinInvitationFields, TVJoinInvitationHelp, TVNoServersActions, TVNoServersNotice, TVNoServersOverview} from '../../../kit/accounts/TVNoServersParts';
import {Button} from '../../../kit/controls';
import {FocusProvider} from '../../../kit/focus';
import {TVAccountCanvas, TVAuthLayout} from '../../../kit/frames/TVAuthLayout';
import {useTVNoServers, type TVNoServersProps} from '../../shared/accounts/useTVNoServers';

const {t} = defaultI18n;

export function TVNoServers(props: TVNoServersProps) {
  const {busy, notice, error, onFind, onSignOut} = props;
  const form = useTVNoServers(props);
  const message = notice || error ? <TVNoServersNotice notice={notice} busy={busy} current={form.current}>{error}</TVNoServersNotice> : undefined;
  return <FocusProvider>
    {form.joining ? <TVAuthLayout testID="tv-no-servers" eyebrow={t('services.start.ready')} title={t('accounts.invitation.tvTitle')}
      body={t('accounts.invitation.tvLede')} kicker={<Button variant="text" disabled={busy} onPress={form.back}>{t('action.back')}</Button>}
      beforeActions={message} aside={<TVJoinInvitationHelp/>}>
      <TVJoinInvitationFields form={form} busy={busy} onFind={onFind} onSignOut={onSignOut}/>
    </TVAuthLayout> : <TVAccountCanvas testID="tv-no-servers" onAccessibilityEscape={form.back}>
      <TVNoServersOverview notice={message} actions={<TVNoServersActions busy={busy} current={form.current} onFind={onFind} onSignOut={onSignOut}/>}>
        <DoorTiles busy={busy} onJoin={form.openJoin}/>
      </TVNoServersOverview>
    </TVAccountCanvas>}
  </FocusProvider>;
}
