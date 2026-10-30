import React from 'react';
import {ScrollView, Text, View} from 'react-native';
import {useSafeAreaInsets} from 'react-native-safe-area-context';
import {defaultI18n} from '../../../../../packages/i18n/src';
import {ColourBackdrop} from '../../kit/artwork';
import {Button} from '../../kit/controls';
import {nativeType, useTheme} from '../../kit/theme';
import type {DirectoryRow} from './directory-chooser';

/** Presentation only: shared committed directory opening owns every command. */
export function PhoneOfflineServers({rows, busy, activeId, retryIn, username, onDownloads, onRetry, onSettings, onJoin, onFind, onRefresh, onBack}: {
  rows: readonly DirectoryRow[]; busy: boolean; activeId?: string; retryIn?: number; username?: string;
  onDownloads?: () => void; onRetry: (id: string) => void; onSettings?: () => void; onJoin?: () => void; onFind?: () => void; onRefresh: () => void; onBack: () => void;
}) {
  const theme = useTheme(), insets = useSafeAreaInsets(), t = defaultI18n.t;
  return <View testID="phone-offline-servers" style={{flex: 1, backgroundColor: theme.colors.ink}}>
    <ColourBackdrop shape="top"/>
    <ScrollView contentContainerStyle={{paddingHorizontal: 24, paddingTop: insets.top + 24, paddingBottom: Math.max(24, insets.bottom + 16), gap: 20}}>
      {onSettings && <Button variant="text" disabled={busy} onPress={onSettings}>{username ? username + ' · ' + t('settings.title') : t('settings.title')}</Button>}
      <Text style={[theme.type.captionStrong, {color: theme.colors.textSecondary}]}>{t('accounts.session.portico')}</Text>
      <Text accessibilityRole="header" style={[nativeType({size: 30, lineHeight: 34, weight: 700, tracking: 0}, theme.textScale), {color: theme.colors.text}]}>{t('accounts.tvOffline.title')}</Text>
      <Text style={[theme.type.body, {color: theme.colors.textSecondary}]}>{t('accounts.tvOffline.body')}</Text>
      {rows.map(row => <View key={row.id} style={{padding: 16, borderRadius: 14, backgroundColor: theme.colors.raised, gap: 10}}>
        <Text style={[theme.type.bodyStrong, {color: theme.colors.text}]}>{row.name}</Text>
        {row.subtitle && <Text style={[theme.type.caption, {color: theme.colors.textSecondary}]}>{t(row.subtitle.key, row.subtitle.values)}</Text>}
        <Button variant="glass" disabled={busy} loading={busy && activeId === row.id || undefined} onPress={() => onRetry(row.id)}>{t('web.chooser.tryAgain')}</Button>
      </View>)}
      {retryIn !== undefined && !busy && <Text style={[theme.type.caption, {color: theme.colors.textSecondary}]}>{t('web.chooser.checkingAgain', {seconds: retryIn})}</Text>}
      <Text style={[theme.type.body, {color: theme.colors.textSecondary}]}>{t('accounts.tvOffline.help')}</Text>
      {onDownloads && <Button variant="glass" disabled={busy} onPress={onDownloads}>{t('downloads.title')}</Button>}
      {onSettings && <Button variant="glass" disabled={busy} onPress={onSettings}>{t('settings.row.porticoAccount')}</Button>}
      {onJoin && <Button variant="glass" disabled={busy} onPress={onJoin}>{t('services.start.join')}</Button>}
      {onFind && <Button variant="glass" disabled={busy} onPress={onFind}>{t('connection.discovery.find')}</Button>}
      <Button variant="text" disabled={busy} onPress={onRefresh}>{t('accounts.servers.refresh')}</Button>
      <Button variant="text" disabled={busy} onPress={onBack}>{t('action.back')}</Button>
    </ScrollView>
  </View>;
}
