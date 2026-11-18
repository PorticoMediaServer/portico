import React from 'react';
import {StyleSheet, Text, View} from 'react-native';
import type {QueuePlaybackView} from '../../../../../packages/client-core/src/queue-controller';
import {defaultI18n} from '../../../../../packages/i18n/src';
import {Button} from '../controls';
import {BlurView} from '../../platform/native-blur';
import {useTheme} from '../theme';

type Item = QueuePlaybackView['items'][number];

/** phone-player 6e: after the episodes set in Settings, playback asks before the
 * next one starts. It names that episode; nothing starts until the viewer confirms. */
export function NativeStillWatchingCard({item, onConfirm, onReplay, onClose}: Readonly<{item: Item; onConfirm: () => void; onReplay: () => void; onClose: () => void}>) {
  // R2: the player's cards (Up next, Still watching, the end card) read the form's roles: title `heading`, lines `body`,
  // the quieter line `caption`, buttons the kit Button's own sizes. TV is Up next's width.
  const theme = useTheme(), t = defaultI18n.t, tv = theme.isTV, {heading, body, bodyStrong} = theme.type;
  const code = item.episode ? t('next.kit.episodeCode', {season: item.episode.seasonNumber, episode: item.episode.number}) : undefined;
  const button = (id: string, variant: 'primary' | 'glass', label: string, onPress: () => void, icon?: 'i-restart') => <Button key={id} focusId={`watch-still-${id}`}
    testID={`watch-still-${id}`} variant={variant} size={tv ? undefined : 'compact'} preferredFocus={false} icon={icon} onPress={onPress}>{label}</Button>;
  return <View testID="watch-still-watching" accessibilityRole="alert" accessibilityLabel={t('player.post.stillWatching')}
    style={{width: tv ? 680 : 400, maxWidth: '92%', padding: tv ? theme.space.lg : theme.space.md, borderRadius: 18, boxShadow: 'inset 0 0 0 1px rgba(237,245,250,.12), 0 24px 60px -20px rgba(0,0,0,.9)'}}>
    <View pointerEvents="none" style={[StyleSheet.absoluteFill, {borderRadius: 18, overflow: 'hidden'}]}>
      <BlurView style={StyleSheet.absoluteFill} blurType="dark" blurAmount={24} reducedTransparencyFallbackColor={theme.colors.ink}/>
      <View style={[StyleSheet.absoluteFill, {backgroundColor: 'rgba(7,16,24,.88)'}]}/>
    </View>
    <Text accessibilityRole="header" style={[heading, {color: theme.colors.text}]}>{t('player.post.stillWatching')}</Text>
    <View style={{flexDirection: 'row', flexWrap: 'wrap', columnGap: theme.space.xs, marginTop: theme.space.tight}}>
      <Text style={[body, {color: theme.colors.textTertiary}]}>{t('player.post.upNext')}</Text>
      {code && <Text style={[bodyStrong, {color: theme.colors.text}]}>{code}</Text>}
      <Text style={[bodyStrong, {color: theme.colors.text, flexShrink: 1}]}>{item.title}</Text>
    </View>
    <Text style={[body, {color: theme.colors.textSecondary, marginTop: theme.space.tight, marginBottom: theme.space.md}]}>{t('player.post.pausedAfterEpisodes')}</Text>
    <View style={{flexDirection: 'row', flexWrap: 'wrap', gap: theme.space.xs}}>
      {button('confirm', 'primary', t('player.post.stillWatchingAction'), onConfirm)}
      {button('replay', 'glass', t('player.post.replay'), onReplay, 'i-restart')}
      {button('close', 'glass', t('action.close'), onClose)}
    </View>
  </View>;
}
