import React from 'react';
import {defaultI18n} from '../../../../../../packages/i18n/src';
import {ProfileFace} from '../../../kit/artwork';
import {Button, NumberPad} from '../../../kit/controls';
import {SignInFrame} from '../../../kit/frames';
import {AccountHint, profileFaceSize} from '../../../kit/accounts/AccountParts';
import {useProfilePIN} from '../../shared/accounts/useProfilePIN';
import {PhonePINEntry} from '../../../kit/accounts/PhonePINEntry';

const t = defaultI18n.t;

export type ProfilePINPageProps = Readonly<{
  testID: string;
  profile: Readonly<{name: string; face: React.ComponentProps<typeof ProfileFace>}>;
  value: string; onChange: (value: string) => void; onSubmit: (pin: string) => void;
  busy: boolean; rejected: boolean;
  /** The wrong-PIN wait or other refusal (rules 28 and 29 of 01-sign-in), already worded by the caller. */
  status?: React.ReactNode;
  /** "Remember on this device", when this server allows it. */
  remember?: React.ReactNode;
  /** A line under Forgot PIN while the profile opens. */
  hints?: React.ReactNode;
  onForgot?: () => void; forgotDisabled?: boolean;
  onBack: () => void;
}>;

/** A profile's PIN on the phone (design/ui-v1 final3-auth-05, frame 14): "‹ Profiles", the profile's face, "Enter
 * profile PIN" and its name, four boxes, Forgot PIN centred, and the screen's own number pad. The fourth digit
 * opens the profile: there is no Continue. The launch picker, Switch profile and the app's lifecycle gate all use it. */
export function ProfilePINPage({testID, profile, value, onChange, onSubmit, busy, rejected, status, remember, hints, onForgot, forgotDisabled, onBack}: ProfilePINPageProps) {
  const pin = useProfilePIN({value, onChange, onSubmit});
  return <SignInFrame testID={testID} title={t('auth.enterProfilePin')} lede={profile.name}
    back={{label: t('profiles.title'), onPress: onBack, disabled: busy, testID: `${testID}-back`}}
    identity={<ProfileFace {...profile.face} size={profileFaceSize.pin}/>}
    footer={<NumberPad testID={`${testID}-pad`} disabled={busy} onDigit={pin.digit} onDelete={pin.remove}/>}>
    <AccountHint>{t('pin.fourDigit')}</AccountHint>
    <PhonePINEntry testID={`${testID}-pin`} value={value} onChange={pin.input} busy={busy} error={rejected} softKeyboard={false}/>
    {status}
    {remember}
    {onForgot && <Button align="center" variant="link" testID={`${testID}-forgot`} disabled={busy || forgotDisabled} onPress={onForgot}>{t('pin.forgot')}</Button>}
    {hints}
  </SignInFrame>;
}
