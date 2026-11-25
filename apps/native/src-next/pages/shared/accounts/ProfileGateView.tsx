import React from 'react';
import {defaultI18n} from '../../../../../../packages/i18n/src';
import {ProfilePicker, type ProfileFace, type ProfileOption} from '../../../kit/artwork';
import {RememberProfileRow} from '../../../kit/accounts/RememberProfileRow';
import type {NativeProfileRecovery} from '../../../platform/profile-recovery';
import {BootWaitLine} from '../../../shell/StartupBoot';
import {TVForgotPINHandoff, TVPickerRememberRow, TVProfilePINForm, TVWhosWatching} from '../../../tv/auth';
import {ProfilePINPage} from '../../touch/accounts/ProfilePINPage';
import {ProfilesPage} from '../../touch/accounts/ProfilesPage';
import {ProfileRecoveryPage} from '../../touch/accounts/ProfileRecoveryPage';

const t = defaultI18n.t;

export type ProfilePickerStageProps = Readonly<{
  tv: boolean;
  testID: string;
  serverName: string;
  focusScope: string;
  busy: boolean;
  profiles: readonly ProfileOption[];
  preferredProfileId?: string;
  onFocusChange?: (id: string | undefined) => void;
  onSelect: (id: string) => void;
  onManage?: () => void;
  onAdd?: () => void;
  canRemember: boolean;
  remember: boolean;
  onRememberChange: (checked: boolean) => void;
  rememberFocusId: string;
  status?: React.ReactNode;
  waitTestID: string;
  onBack?: () => void;
}>;

/** Shared Who's Watching presentation for TV and touch (used by lifecycle gate and post-sign-in entry). */
export function ProfilePickerStage({
  tv, testID, serverName, focusScope, busy, profiles, preferredProfileId, onFocusChange, onSelect,
  onManage, onAdd, canRemember, remember, onRememberChange, rememberFocusId, status, waitTestID, onBack,
}: ProfilePickerStageProps) {
  const picker = <ProfilePicker
    focusScope={focusScope}
    focusBusy={busy}
    profiles={profiles}
    preferredProfileId={preferredProfileId}
    {...(onFocusChange ? {onFocusChange} : {})}
    onSelect={onSelect}
    {...(onManage ? {onManage} : {})}
    {...(onAdd ? {onAdd} : {})}
    title={tv ? t('accounts.profiles.onServer', {server: serverName}) : t('auth.whosWatching')}
    {...(tv ? {} : {description: t('accounts.profiles.serverContext', {server: serverName})})}/>;
  const tick = canRemember
    ? tv
      ? <TVPickerRememberRow checked={remember} onChange={onRememberChange} busy={busy}/>
      : <RememberProfileRow focusId={rememberFocusId} checked={remember} onChange={onRememberChange} disabled={busy} underFaces/>
    : null;
  const wait = <BootWaitLine active={busy} serverName={serverName} testID={waitTestID}/>;
  if (tv) return <TVWhosWatching testID={testID}>{picker}{tick}{status}{wait}</TVWhosWatching>;
  return <ProfilesPage testID={testID} picker={picker} onBack={onBack} backDisabled={busy}>{tick}{status}{wait}</ProfilesPage>;
}

export type ProfilePINRecoveryState = Readonly<{
  controller: NativeProfileRecovery;
  profileName: string;
  username: string;
  onOpen: (pin: string) => void;
  onClose: () => void;
}>;

export type ProfileForgotPINHandoff = Readonly<{
  profileName: string;
  serverAddress: string;
  server: Readonly<{id: string; signIn: 'local' | 'portico'}>;
}>;

export type ProfilePINStageProps = Readonly<{
  tv: boolean;
  testID?: string;
  profile?: Readonly<{
    id: string;
    name: string;
    face: React.ComponentProps<typeof ProfileFace>;
    primary?: boolean;
    pinRequired?: boolean;
    disabled?: boolean;
  }>;
  code: string;
  onCodeChange: (value: string) => void;
  onSubmit: (pin: string) => void;
  busy: boolean;
  rejected: boolean;
  error?: React.ReactNode;
  status?: React.ReactNode;
  canRemember?: boolean;
  remember?: boolean;
  onRememberChange?: (checked: boolean) => void;
  rememberFocusId?: string;
  waitTestID?: string;
  serverName?: string;
  onBack: () => void;
  onForgot: () => void;
  forgotDisabled?: boolean;
  forgot?: ProfileForgotPINHandoff;
  recovery?: ProfilePINRecoveryState;
}>;

/** Shared Profile PIN, TV Forgot PIN handoff, and touch PIN recovery presentation across lifecycle, entry, and switch flows. */
export function ProfilePINStage({
  tv, testID = 'accounts-entry', profile, code, onCodeChange, onSubmit, busy, rejected,
  error, status, canRemember, remember, onRememberChange, rememberFocusId, waitTestID, serverName,
  onBack, onForgot, forgotDisabled, forgot, recovery,
}: ProfilePINStageProps) {
  if (recovery) {
    return <ProfileRecoveryPage controller={recovery.controller} profileName={recovery.profileName} username={recovery.username}
      onOpen={recovery.onOpen} onClose={recovery.onClose}/>;
  }
  if (tv && forgot) {
    return <TVForgotPINHandoff profileName={forgot.profileName} serverAddress={forgot.serverAddress} server={forgot.server} onBack={onBack}/>;
  }
  if (!profile) return null;
  const wait = waitTestID ? <BootWaitLine active={busy} serverName={serverName} testID={waitTestID}/> : undefined;
  if (tv) {
    const option: ProfileOption = {
      id: profile.id,
      name: profile.name,
      ...profile.face,
      primary: !!profile.primary,
      pinRequired: profile.pinRequired ?? true,
      ...(profile.disabled !== undefined ? {disabled: profile.disabled} : {}),
    };
    return <TVProfilePINForm profile={option} code={code} onCodeChange={onCodeChange} busy={busy} error={error}
      {...(canRemember && onRememberChange ? {remember: {checked: !!remember, onChange: onRememberChange}} : {})}
      hints={busy ? wait : undefined} onSubmit={onSubmit} onBack={onBack} onForgotPIN={onForgot}/>;
  }
  return <ProfilePINPage testID={testID} profile={{name: profile.name, face: profile.face}}
    value={code} onChange={onCodeChange} onSubmit={onSubmit} busy={busy} rejected={rejected} status={status}
    remember={canRemember && onRememberChange && rememberFocusId
      ? <RememberProfileRow focusId={rememberFocusId} checked={!!remember} onChange={onRememberChange} disabled={busy}/>
      : undefined}
    hints={wait} onForgot={onForgot} {...(forgotDisabled !== undefined ? {forgotDisabled} : {})} onBack={onBack}/>;
}
