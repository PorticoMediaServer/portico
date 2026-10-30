import React from 'react';
import {StyleSheet, Text, View} from 'react-native';
import type {QueuePlaybackView} from '../../../../../packages/client-core/src/queue-controller';
import {defaultI18n} from '../../../../../packages/i18n/src';
import {Button} from '../../kit/controls';
import {BlurView} from '../../platform/native-blur';
import {nativeType, useTheme} from '../../kit/theme';

type Item = QueuePlaybackView['items'][number];

/** phone-player 6e: after the episodes set in Settings, playback asks before the
 * next one starts. It names that episode; nothing starts until the viewer confirms. */
export function NativeStillWatchingCard({item, onConfirm, onReplay, onClose}: Readonly<{item: Item; onConfirm: () => void; onReplay: () => void; onClose: () => void}>) {
  const theme = useTheme(), t = defaultI18n.t;
  const code = item.episode ? t('next.kit.episodeCode', {season: item.episode.seasonNumber, episode: item.episode.number}) : undefined;
  const button = (id: string, variant: 'primary' | 'glass', label: string, onPress: () => void, icon?: 'i-restart') => <Button key={id} focusId={`watch-still-${id}`}
    testID={`watch-still-${id}`} variant={variant} size="compact" hasTVPreferredFocus={false} {...(icon ? {icon, iconSize: 17} : {})}
    style={{height: 40, paddingHorizontal: 15, borderRadius: 10}} textStyle={{fontSize: 14.5, fontWeight: '700'}} onPress={onPress}>{label}</Button>;
  return <View testID="watch-still-watching" accessibilityRole="alert" accessibilityLabel={t('player.post.stillWatching')}
    style={{width: 400, maxWidth: '92%', padding: 20, borderRadius: 18, boxShadow: 'inset 0 0 0 1px rgba(237,245,250,.12), 0 24px 60px -20px rgba(0,0,0,.9)'}}>
    <View pointerEvents="none" style={[StyleSheet.absoluteFill, {borderRadius: 18, overflow: 'hidden'}]}>
      <BlurView style={StyleSheet.absoluteFill} blurType="dark" blurAmount={24} reducedTransparencyFallbackColor={theme.colors.ink}/>
      <View style={[StyleSheet.absoluteFill, {backgroundColor: 'rgba(7,16,24,.88)'}]}/>
    </View>
    <Text accessibilityRole="header" style={[nativeType({size: 22, weight: 800, tracking: -.025, lineHeight: 'normal'}, theme.textScale), {color: theme.colors.text}]}>{t('player.post.stillWatching')}</Text>
    <View style={{flexDirection: 'row', flexWrap: 'wrap', columnGap: 10, marginTop: 6}}>
      <Text style={[nativeType({size: 14, weight: 600, tracking: 0, lineHeight: 'normal'}, theme.textScale), {color: theme.colors.textTertiary}]}>{t('player.post.upNext')}</Text>
      {code && <Text style={[nativeType({size: 14, weight: 700, tracking: 0, lineHeight: 'normal'}, theme.textScale), {color: theme.colors.text}]}>{code}</Text>}
      <Text numberOfLines={1} style={[nativeType({size: 14, weight: 700, tracking: 0, lineHeight: 'normal'}, theme.textScale), {color: theme.colors.text, flexShrink: 1}]}>{item.title}</Text>
    </View>
    <Text style={[nativeType({size: 14, weight: 400, tracking: 0, lineHeight: 21}, theme.textScale), {color: theme.colors.textSecondary, marginTop: 4, marginBottom: 16}]}>{t('player.post.pausedAfterEpisodes')}</Text>
    <View style={{flexDirection: 'row', flexWrap: 'wrap', gap: 8}}>
      {button('confirm', 'primary', t('player.post.stillWatchingAction'), onConfirm)}
      {button('replay', 'glass', t('player.post.replay'), onReplay, 'i-restart')}
      {button('close', 'glass', t('action.close'), onClose)}
    </View>
  </View>;
}
