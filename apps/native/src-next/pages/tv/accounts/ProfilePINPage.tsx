import React from 'react';
import {defaultI18n} from '../../../../../../packages/i18n/src';
import type {ProfileOption} from '../../../kit/artwork';
import {FocusProvider} from '../../../kit/focus';
import {TVCodeErrorLine, TVProfilePINLayout, TVProfilePINHints} from '../../../kit/frames/TVAuthLayout';
import {TVCodeBoxes, TVNumericPad} from '../../../tv/auth/TVNumericPad';
import {TVRememberRow} from '../../../tv/auth/TVRememberRow';
import {useTVProfilePIN} from '../../shared/accounts/useTVProfilePIN';

export type TVProfilePINFormProps = Readonly<{
  profile: ProfileOption; code: string; onCodeChange: (value: string) => void;
  busy: boolean; error?: React.ReactNode; hints?: React.ReactNode;
  onSubmit: (value: string) => void; onBack: () => void; onForgotPIN: () => void;
  /** "Remember this profile on this device", under the boxes (auth.html frame 17). Absent when the
   *  server doesn't allow remembering. */
  remember?: Readonly<{checked: boolean; onChange: (checked: boolean) => void}>;
}>;

/** Profile PIN uses only the drawn numeric pad, never a TextInput or system keyboard. */
export function TVProfilePINForm(props: TVProfilePINFormProps) {
  return <FocusProvider><ProfilePINForm {...props}/></FocusProvider>;
}

function ProfilePINForm(props: TVProfilePINFormProps) {
  const {id, digit, remove} = useTVProfilePIN(props);
  return <TVProfilePINLayout profile={props.profile} hints={props.hints ?? <TVProfilePINHints/>} onBack={props.onBack}
    pad={<TVNumericPad id={id} busy={props.busy} onDigit={digit} onDelete={remove} help={defaultI18n.t('pin.forgot')} onHelp={props.onForgotPIN}/>}>
    <TVCodeBoxes code={props.code} length={4} masked busy={props.busy} error={!!props.error}/>
    <TVCodeErrorLine error={props.error}/>
    {props.remember && <TVRememberRow {...props.remember} busy={props.busy}/>}
  </TVProfilePINLayout>;
}
