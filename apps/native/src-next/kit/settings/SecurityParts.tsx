import React, {type ReactNode} from 'react';
import {Text, View} from 'react-native';
import QRCode from 'react-native-qrcode-svg';
import {BottomSheet, Dialog} from '../overlays';
import {dialogSheetGutter} from '../overlays/surface';
import {useTheme} from '../theme';

export function SecuritySheet({id, open, onClose, title, description, locked, actions, children}: {
  id: string; open: boolean; onClose: () => void; title: string; description?: string;
  locked?: boolean; actions: ReactNode; children: ReactNode;
}) {
  const theme = useTheme();
  const surface = {testID: id, open, title, description, dismissOnBack: !locked,
    dismissOnOutsidePress: !locked, showClose: !locked, onOpenChange: (value: boolean) => {if (!value) onClose();}};
  const content = <View style={{gap: theme.space.sm}}>{children}</View>;
  const footer = <View style={{flexDirection: 'row', flexWrap: 'wrap', justifyContent: 'flex-end', gap: theme.space.xs}}>{actions}</View>;
  return theme.platform === 'phone'
    ? <BottomSheet {...surface} footer={footer} dismissOnGesture={!locked} fitContent keyboardAvoiding
      contentStyle={{paddingHorizontal: dialogSheetGutter}}>{content}</BottomSheet>
    : <Dialog {...surface} footer={footer}>{content}</Dialog>;
}

export function SecurityText({children, secret = false, announce = false, testID}: {
  children: ReactNode; secret?: boolean; announce?: boolean; testID?: string;
}) {
  const theme = useTheme();
  return <Text testID={testID} selectable={secret} accessibilityLiveRegion={announce ? 'polite' : undefined}
    style={[secret ? theme.type.rowLabel : theme.type.rowHelp, {color: secret ? theme.colors.text : theme.colors.textSecondary,
      fontVariant: secret ? ['tabular-nums'] : undefined, padding: announce ? theme.space.sm : undefined}]}>{children}</Text>;
}

export function PasswordMeter({level, label, hint}: {level: number; label: string; hint?: string}) {
  const theme = useTheme();
  return <View accessibilityLabel={label} style={{gap: theme.space.xs}}>
    <View style={{height: 4, flexDirection: 'row', gap: theme.space.hairline}}>
      {[1, 2, 3, 4].map(step => <View key={step} style={{flex: 1, borderRadius: 2,
        backgroundColor: step <= level ? theme.colors.accentStrong : theme.colors.line}}/>)}
    </View>
    <SecurityText>{[label, hint].filter(Boolean).join(' · ')}</SecurityText>
  </View>;
}

export function EnrolmentCode({uri, secret, help}: {uri: string; secret: string; help: string}) {
  const theme = useTheme();
  return <>
    <View style={{alignSelf: 'center', padding: theme.space.sm, borderRadius: theme.baseRadii.control, backgroundColor: theme.colors.white}}>
      <QRCode value={uri} size={180}/>
    </View>
    <SecurityText>{help}</SecurityText>
    <SecurityText secret>{secret.match(/.{1,4}/g)?.join(' ')}</SecurityText>
  </>;
}
