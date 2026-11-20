import React from 'react';
import {defaultI18n} from '../../../../../../packages/i18n/src';
import {SignInFrame} from '../../../kit/frames';

const t = defaultI18n.t;

/** Who's watching on the phone (design/ui-v1 final3-auth-05, frame 13): the kit picker carries the left-aligned
 * title, "On {server}", the faces, Add profile and Manage profiles; under it the remember row, a refusal and the
 * wait line. Sign-in and the app's own profile gate both draw this page. "‹ Back" shows only where there is a step
 * to go back to: the frame draws none, but taking it away would strand the person. */
export function ProfilesPage({testID, picker, children, onBack, backDisabled}: {
  testID: string; picker: React.ReactNode; children?: React.ReactNode; onBack?: () => void; backDisabled?: boolean;
}) {
  return <SignInFrame testID={testID}
    back={onBack ? {label: t('action.back'), onPress: onBack, disabled: backDisabled, testID: `${testID}-back`} : undefined}>
    {picker}{children}
  </SignInFrame>;
}
