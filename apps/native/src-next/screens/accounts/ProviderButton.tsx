import React from 'react';
import {Button} from '../../kit/controls';
import {useTheme} from '../../kit/theme';

export type IdentityProviderId = 'google' | 'apple';

/** The Google and Apple buttons, drawn the providers' way (dark Google, black Apple), wherever a
 * Portico Account is proved with one: signing in, and Forgot PIN's provider step. */
export function ProviderButton({provider, label, busy = false, testID, onPress}: {
  provider: IdentityProviderId; label: string; busy?: boolean; testID?: string; onPress: () => void;
}) {
  const theme = useTheme(), google = provider === 'google';
  return <Button variant="text" icon={google ? 'i-google' : 'i-apple'} iconSize={19} iconColor={google ? undefined : '#fff'} testID={testID} disabled={busy}
    style={{width: '100%', alignSelf: 'stretch', height: 48, borderRadius: 11, borderWidth: 1, borderColor: google ? '#5f6368' : 'rgba(237,245,250,.22)',
      backgroundColor: google ? '#131314' : '#000'}}
    textStyle={{fontSize: 16 * theme.textScale, fontWeight: '700', color: google ? '#e3e3e3' : '#fff'}}
    onPress={() => {if (!busy) onPress();}}>{label}</Button>;
}
