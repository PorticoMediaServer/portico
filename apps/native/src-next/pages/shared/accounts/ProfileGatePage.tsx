import React from 'react';
import {FormError} from '../../../kit/controls';
import {PinStatusLine} from '../../../kit/accounts/PinStatusLine';
// Named through pages/shared (not `./`) so the screen harness loads the shared view for real.
import {ProfilePickerStage, ProfilePINStage} from '../../../pages/shared/accounts/ProfileGateView';
import {useLifecycleProfileGate} from './useLifecycleProfileGate';

/** Who's watching, and a profile's PIN, as the session lifecycle asks for them (phone, tablet and TV). The drawn frames
 * are AccountEntry's profile stages (auth.html 13–17). "Remember this profile on this device" is offered under the faces
 * and under the PIN, only where the server allows remembering (auth.html section 8, 2 Nov). */
export function ProfileGatePage() {
  const gate = useLifecycleProfileGate();
  if (!gate) return null;
  const {tv, busy, server, state} = gate;
  const status = gate.failureText !== undefined ? <FormError placement="block">{gate.failureText}</FormError> : null;

  if (gate.kind === 'choosing') {
    return ProfilePickerStage({
      tv,
      testID: 'lifecycle-profiles',
      serverName: server.name,
      focusScope: `tv.auth.profiles.${server.serverId}`,
      busy,
      profiles: gate.state.profiles.map(gate.option),
      preferredProfileId: gate.state.focus,
      onSelect: gate.select,
      onManage: gate.management,
      onAdd: gate.management && gate.state.profiles.length < 8 ? gate.management : undefined,
      canRemember: gate.state.canRemember,
      remember: gate.remember,
      onRememberChange: gate.setRemember,
      rememberFocusId: 'lifecycle-remember-profile',
      status,
      waitTestID: 'lifecycle-profile-wait',
      onBack: gate.state.switching ? gate.back : undefined,
    });
  }

  const {profile, connection} = gate;
  const pinStatus = gate.failureText !== undefined ? <PinStatusLine>{gate.failureText}</PinStatusLine> : undefined;
  return ProfilePINStage({
    tv,
    testID: 'lifecycle-profile-pin',
    profile: {
      id: profile.id,
      name: profile.name,
      face: gate.face,
      primary: profile.primary,
      pinRequired: profile.pinRequired,
      disabled: busy,
    },
    code: gate.code,
    onCodeChange: gate.changeCode,
    onSubmit: gate.send,
    busy,
    rejected: !!gate.state.rejected,
    error: pinStatus,
    status,
    canRemember: state.canRemember,
    remember: gate.remember,
    onRememberChange: gate.changeRemember,
    rememberFocusId: 'lifecycle-remember-profile',
    waitTestID: 'lifecycle-profile-wait',
    serverName: server.name,
    onBack: gate.back,
    onForgot: tv ? gate.startForgot : gate.openRecovery,
    forgotDisabled: !connection,
    forgot: tv && gate.forgot && connection
      ? {profileName: profile.name, serverAddress: connection.api.baseUrl, server: {id: server.serverId, signIn: server.signIn}}
      : undefined,
    recovery: gate.recovery && connection
      ? {
          controller: gate.recovery.controller,
          profileName: gate.recovery.name,
          username: connection.account.account.username,
          onOpen: gate.recoveryOpen,
          onClose: gate.recoveryClose,
        }
      : undefined,
  });
}
