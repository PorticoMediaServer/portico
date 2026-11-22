import React, {type ReactNode} from 'react';
import {ScrollView, Text, View} from 'react-native';
import {useSafeAreaInsets} from 'react-native-safe-area-context';
import {defaultI18n} from '../../../../../packages/i18n/src';
import {Button} from '../controls';
import {useTheme} from '../theme';

/** The offline servers page on phones and tablets: the account line, the kicker, title and body, then its rows and ways out. */
export function OfflineServersFrame({account, busy, children}: {account?: Readonly<{label: string; onPress: () => void}>; busy: boolean; children: ReactNode}) {
  const theme = useTheme(), insets = useSafeAreaInsets(), t = defaultI18n.t;
  return <View testID="phone-offline-servers" style={{flex: 1}}>
    <ScrollView contentContainerStyle={{paddingHorizontal: 24, paddingTop: insets.top + 24, paddingBottom: Math.max(24, insets.bottom + 16), gap: 20}}>
      {account && <Button variant="text" disabled={busy} onPress={account.onPress}>{account.label}</Button>}
      <Text style={[theme.type.captionStrong, {color: theme.colors.textSecondary}]}>{t('accounts.session.portico')}</Text>
      <Text accessibilityRole="header" style={[theme.type.accountTitle, {color: theme.colors.text}]}>{t('accounts.tvOffline.title')}</Text>
      <Text style={[theme.type.body, {color: theme.colors.textSecondary}]}>{t('accounts.tvOffline.body')}</Text>
      {children}
    </ScrollView>
  </View>;
}

/** One server not answering: its name, when it was last seen, and Try again. */
export function OfflineServerRow({name, detail, busy, loading, onRetry}: {name: string; detail?: string; busy: boolean; loading: boolean; onRetry: () => void}) {
  const theme = useTheme();
  return <View style={{padding: 16, borderRadius: 14, backgroundColor: theme.colors.raised, gap: 10}}>
    <Text style={[theme.type.bodyStrong, {color: theme.colors.text}]}>{name}</Text>
    {detail && <Text style={[theme.type.caption, {color: theme.colors.textSecondary}]}>{detail}</Text>}
    <Button variant="glass" disabled={busy} loading={loading || undefined} onPress={onRetry}>{defaultI18n.t('web.chooser.tryAgain')}</Button>
  </View>;
}

/** A line of help or status under the rows. */
export function OfflineServersNote({children}: {children: string}) {
  const theme = useTheme();
  return <Text style={[theme.type.body, {color: theme.colors.textSecondary}]}>{children}</Text>;
}

/** The countdown to the next automatic check. */
export function OfflineServersCountdown({children}: {children: string}) {
  const theme = useTheme();
  return <Text style={[theme.type.caption, {color: theme.colors.textSecondary}]}>{children}</Text>;
}
