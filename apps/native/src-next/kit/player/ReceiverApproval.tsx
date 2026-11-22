import React from 'react';
import {Text, View} from 'react-native';
import {defaultI18n} from '../../../../../packages/i18n/src';
import {Button} from '../controls';
import {Dialog} from '../overlays';
import {useTheme} from '../theme';

/** A TV asked once per sending device whether it may play here: closing the question declines it. */
export function ReceiverApproval({open, device, onAllow, onDecline}: {open: boolean; device: string; onAllow: () => void; onDecline: () => void}) {
  const theme = useTheme(), t = defaultI18n.t;
  return <Dialog testID="tv-receiver-approval" open={open} title={t('playOn.receiver.askTitle', {device})}
    onOpenChange={value => {if (!value) onDecline();}}>
    <View style={{gap: theme.space.sm}}>
      <Text style={[theme.type.body, {color: theme.colors.textSecondary}]}>{t('playOn.receiver.askBody', {device})}</Text>
      <Button testID="tv-receiver-decline" variant="glass" onPress={onDecline}>{t('playOn.receiver.decline')}</Button>
      <Button testID="tv-receiver-allow" onPress={onAllow}>{t('playOn.receiver.allow')}</Button>
    </View>
  </Dialog>;
}
