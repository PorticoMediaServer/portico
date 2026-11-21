import React from 'react';
import QRCode from 'react-native-qrcode-svg';
import {tvAccountSpec} from '@portico/design';
import {defaultI18n} from '../../../../../../packages/i18n/src';
import {TVAccountNotice, TVAccountWidePage, TVDoor, TVDoors, TVInvitationForm} from '../../../kit/accounts/TVNoServerParts';
import {Button, TextField} from '../../../kit/controls';
import {FocusProvider} from '../../../kit/focus';
import {NativeTVKeyboard} from '../../../screens/search/TVKeyboard';
import {useTVNoServers, type TVNoServersProps} from '../../shared/accounts/useTVNoServers';

const t = defaultI18n.t, download = 'https://getportico.tv/download/', door = tvAccountSpec.wide.door;

/** A Portico Account with no servers on a TV (connect 35; connect 38 when the account was just removed from one): Join with an
 * invitation and Set up your own server (its download code), Find a server on this Wi-Fi and Sign out under them. OK on Join
 * opens Enter the invitation (connect 36): the address and code typed on the keyboard under them, where the code is beside. */
export function NoServersPage(props: TVNoServersProps) {
  const view = useTVNoServers(props), {busy, notice, error} = props;
  const noticeView = (notice || error) && <>
    {notice && <TVAccountNotice title={notice.title} body={notice.body}
      action={view.retry && <Button focusId="notice-retry" variant="glass" disabled={busy} onPress={view.retry}>{t('web.chooser.tryAgain')}</Button>}/>}
    {error}
  </>;
  const foot = <>
    <Button focusId="find" variant="link" disabled={busy} onPress={view.find}>{t('connection.discovery.find')}</Button>
    <Button focusId="sign-out" variant="link" disabled={busy} onPress={view.signOut}>{t('accounts.signOut.portico')}</Button>
  </>;
  return <FocusProvider>{view.joining
    ? <TVAccountWidePage testID="tv-no-servers" kicker={t('services.start.join')} title={t('accounts.invitation.tvTitle')} body={t('accounts.invitation.tvLede')} notice={noticeView}>
      <TVInvitationForm region={{id: 'accounts.empty.fields', entry: 'address', order: ['address', 'invitation', 'join', 'back']}}
        fields={<>
          <TextField focusId="address" label={t('accounts.signIn.address')} value={view.address} onFocus={() => view.focusField('address')} showSoftInputOnFocus={false}
            editable={!busy} autoCapitalize="none" autoCorrect={false} onChangeText={view.setAddress}/>
          <TextField focusId="invitation" label={t('services.start.invitation')} value={view.invitation} onFocus={() => view.focusField('invitation')} showSoftInputOnFocus={false}
            editable={!busy} autoCapitalize="none" autoCorrect={false} autoComplete="off" onChangeText={view.setInvitation}/>
        </>}
        keyboard={<NativeTVKeyboard id={`accounts.empty.keyboard.${view.field}`} value={view.typed} rows={view.keys} columns={tvAccountSpec.wide.form.keyColumns}
          width={tvAccountSpec.wide.form.width} lead={view.field === 'invitation' ? '0' : undefined} leftRegionId="accounts.empty.fields"
          // The typed field owns the odd ticket; a completed pending run moves both kinds on.
          activation={{visible: view.current() && !busy, intent: 'enter', revision: view.entryRevision * 2 + (view.field === 'address' ? 0 : 1)}} onChangeText={view.type}/>}
        actions={<>
          <Button focusId="join" disabled={busy || !view.invitation.trim()} onPress={view.join}>{t('services.start.joinAction')}</Button>
          <Button focusId="back" variant="glass" disabled={busy} onPress={view.back}>{t('action.back')}</Button>
        </>}
        help={{title: t('accounts.invitation.tvCodeHelpTitle'), paragraphs: [t('accounts.invitation.tvCodeHelpBody'), t('accounts.invitation.tvCodeHelpPhone')]}}/>
    </TVAccountWidePage>
    : <TVAccountWidePage testID="tv-no-servers" kicker={t('services.start.ready')} title={t(notice ? 'services.start.returning' : 'services.start.welcome')} body={t('services.start.body')}
      notice={noticeView} foot={foot}>
      <TVDoors region={{id: 'accounts.empty.door', entry: 'join', order: ['join', 'setup']}}>
        <TVDoor focusId="join" icon="i-link" title={t('services.start.join')} body={t('services.start.joinBody')} disabled={busy} onPress={view.open}/>
        {/* Set up is informational: OK keeps its code and steps in front and runs nothing. */}
        <TVDoor focusId="setup" icon="i-server" title={t('services.start.setup')} body={t('services.start.phoneSetupBody')} disabled={busy}
          aside={<QRCode value={download} size={door.code - 2 * door.codePad}/>}/>
      </TVDoors>
    </TVAccountWidePage>}</FocusProvider>;
}
