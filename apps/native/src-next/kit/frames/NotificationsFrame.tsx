import React, {type ReactNode} from 'react';
import {View, useWindowDimensions} from 'react-native';
import {useSafeAreaInsets} from 'react-native-safe-area-context';
import {TopBar, type TopBarBack} from '../layout/TopBar';
import {SettingsSheet} from '../shell';
import {useTheme} from '../theme';

export function NotificationsFrame({children, back, trailing, title, testID, onClose}: {
  children?: ReactNode; back?: TopBarBack; trailing?: ReactNode; title: string; testID: string; onClose: () => void;
}) {
  const theme = useTheme(), insets = useSafeAreaInsets(), window = useWindowDimensions(), phone = theme.platform === 'phone';
  const header = back
    ? <TopBar safeArea={false} title={title} back={back} trailing={trailing}/>
    : <View style={{minHeight: theme.targets.touch}}/>;
  if (!phone) return <View testID={testID} style={{flex: 1, paddingTop: insets.top + theme.space.sm}}>{header}{children}</View>;
  return <SettingsSheet testID={testID} profile={null} shortcuts={null} open onOpenChange={open => {if (!open) onClose();}} accessibilityLabel={title}
    detents={[Math.max(1, window.height - 62)]} headerContent={header} scrollable={false} showClose={false} showGrabber={false}
    style={{paddingTop: 0}}>
    {children}
  </SettingsSheet>;
}
