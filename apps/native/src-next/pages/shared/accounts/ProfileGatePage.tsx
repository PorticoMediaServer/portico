import React from 'react';
import {defaultI18n} from '../../../../../../packages/i18n/src';
import {FormError} from '../../../kit/controls';
import {ProfilePicker} from '../../../kit/artwork';
import {PinStatusLine} from '../../../kit/accounts/PinStatusLine';
import {RememberProfileRow} from '../../../kit/accounts/RememberProfileRow';
import {BootWaitLine} from '../../../shell/StartupBoot';
import {TVForgotPINHandoff, TVPickerRememberRow, TVProfilePINForm, TVWhosWatching} from '../../../tv/auth';
import {ProfilePINPage} from '../../touch/accounts/ProfilePINPage';
import {ProfilesPage} from '../../touch/accounts/ProfilesPage';
import {ProfileRecoveryPage} from '../../touch/accounts/ProfileRecoveryPage';
import {useLifecycleProfileGate} from './useLifecycleProfileGate';

const t = defaultI18n.t;

/** Who's watching, and a profile's PIN, as the session lifecycle asks for them (phone, tablet and TV). The drawn frames
 * are AccountEntry's profile stages (auth.html 13–17). "Remember this profile on this device" is offered under the faces
 * and under the PIN, only where the server allows remembering (auth.html section 8, 2 Nov). */
export function ProfileGatePage() {
  const gate = useLifecycleProfileGate();
  if (!gate) return null;
  const {tv, busy, server, state} = gate;
  const status = gate.failureText !== undefined ? <FormError placement="block">{gate.failureText}</FormError> : null;
  const wait = <BootWaitLine active={busy} serverName={server.name} testID="lifecycle-profile-wait"/>;

  if (gate.kind === 'choosing') {
    const picker = <ProfilePicker focusScope={`tv.auth.profiles.${server.serverId}`} focusBusy={busy} profiles={gate.state.profiles.map(gate.option)}
      preferredProfileId={gate.state.focus} onSelect={gate.select}
      {...(gate.management ? {onManage: gate.management, ...(gate.state.profiles.length < 8 ? {onAdd: gate.management} : {})} : {})}
      title={tv ? t('accounts.profiles.onServer', {server: server.name}) : t('auth.whosWatching')}
      {...(tv ? {} : {description: t('accounts.profiles.serverContext', {server: server.name})})}/>;
    const tick = gate.state.canRemember ? tv ? <TVPickerRememberRow checked={gate.remember} onChange={gate.setRemember} busy={busy}/>
      : <RememberProfileRow focusId="lifecycle-remember-profile" checked={gate.remember} onChange={gate.setRemember} disabled={busy} underFaces/> : null;
    if (tv) return <TVWhosWatching testID="lifecycle-profiles">{picker}{tick}{status}{wait}</TVWhosWatching>;
    return <ProfilesPage testID="lifecycle-profiles" picker={picker} onBack={gate.state.switching ? gate.back : undefined} backDisabled={busy}>{tick}{status}{wait}</ProfilesPage>;
  }

  const {profile, connection} = gate;
  if (gate.recovery && connection) return <ProfileRecoveryPage controller={gate.recovery.controller} profileName={gate.recovery.name} username={connection.account.account.username}
    onOpen={gate.recoveryOpen} onClose={gate.recoveryClose}/>;
  if (tv && gate.forgot && connection) return <TVForgotPINHandoff profileName={profile.name}
    serverAddress={connection.api.baseUrl} server={{id: server.serverId, signIn: server.signIn}} onBack={gate.back}/>;
  const pinStatus = gate.failureText !== undefined ? <PinStatusLine>{gate.failureText}</PinStatusLine> : undefined;
  if (tv) return <TVProfilePINForm profile={gate.option(profile)} code={gate.code} onCodeChange={gate.changeCode} busy={busy} error={pinStatus}
    {...(state.canRemember ? {remember: {checked: gate.remember, onChange: gate.changeRemember}} : {})}
    hints={busy ? wait : undefined} onSubmit={gate.send} onBack={gate.back} onForgotPIN={gate.startForgot}/>;
  return <ProfilePINPage testID="lifecycle-profile-pin" profile={{name: profile.name, face: gate.face}}
    value={gate.code} onChange={gate.changeCode} onSubmit={gate.send} busy={busy} rejected={!!gate.state.rejected} status={status}
    remember={state.canRemember ? <RememberProfileRow focusId="lifecycle-remember-profile" checked={gate.remember} onChange={gate.changeRemember} disabled={busy}/> : undefined}
    hints={wait} onForgot={gate.openRecovery} forgotDisabled={!connection} onBack={gate.back}/>;
}
