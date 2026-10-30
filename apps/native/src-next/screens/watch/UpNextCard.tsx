import React from 'react';
import {StyleSheet, Text, View} from 'react-native';
import type {QueuePlaybackView} from '../../../../../packages/client-core/src/queue-controller';
import type {UpNextCountdown, UpNextOffer} from '../../../../../packages/client-core/src/up-next';
import {clockText} from '../../../../../packages/client-core/src/presentation/kit-model/text';
import {defaultI18n} from '../../../../../packages/i18n/src';
import {Artwork, type ArtworkSource} from '../../kit/artwork';
import {Button} from '../../kit/controls';
import {BlurView, glassRecipe} from '../../platform/native-blur';
import {useTheme} from '../../kit/theme';

type Item = QueuePlaybackView['items'][number];

/** tv-player 5 and 6: the next episode offered over the closing credits. Its
 * picture is the item's own still, dimmed; nothing starts early unless the
 * viewer turned that on, and credits followed by a scene never count down. */
export type NativeUpNextCardProps = Readonly<{
  item: Item;
  artwork?: ArtworkSource;
  offer?: UpNextOffer;
  countdown: UpNextCountdown | null;
  /** The credits end before the title does: a scene follows them. */
  sceneAfter: boolean;
  /** The post-play countdown the bar fills from. */
  countdownTotal: number;
  /** Phone geometry (phone-player 6): a 360-wide card in the corner. */
  compact?: boolean;
  onPlayNow: () => void;
  onWatchCredits: () => void;
  onSkipCredits: () => void;
  /** Once the title has ended, its canonical policy owns Cancel and the next
   * action. The same card also asks for an explicit still-watching response. */
  postPlay?: Readonly<{onCancel: () => void; onReplay: () => void; onClose: () => void}>;
}>;

export function NativeUpNextCard({item, artwork, countdown, countdownTotal, sceneAfter, compact = false, onPlayNow, onWatchCredits, onSkipCredits, postPlay}: NativeUpNextCardProps) {
  const theme = useTheme(), t = defaultI18n.t;
  const code = item.episode ? t('next.kit.episodeCode', {season: item.episode.seasonNumber, episode: item.episode.number}) : undefined;
  const minutes = item.durationMs && item.durationMs > 0 ? t('next.kit.duration.minutes', {minutes: Math.round(item.durationMs / 60_000)}) : undefined;
  const still = compact ? {width: 112, height: 63} : {width: 256, height: 144};
  // tv-player 5 draws the card's own buttons smaller than the player's: 64 high, 22px.
  const button = (id: string, variant: 'primary' | 'glass', icon: 'i-play' | 'i-next' | 'i-replay' | undefined, label: string, onPress: () => void) => <Button key={id} focusId={`watch-up-next-${id}`} testID={`watch-up-next-${id}`}
    variant={variant} size={compact ? 'compact' : 'hero'} hasTVPreferredFocus={false} {...(icon ? {icon} : {})}
    style={compact ? {height: 40, paddingHorizontal: 15, borderRadius: 10} : {height: 64, paddingHorizontal: 28}}
    textStyle={compact ? {fontSize: 14.5, fontWeight: '700'} : {fontSize: 22, fontWeight: '700'}} onPress={onPress}>{label}</Button>;
  // tv-player 5 draws the card as glass over the picture: a 24px backdrop blur with its own tint.
  const glass = glassRecipe();
  const radius = compact ? 16 : 22;
  return <View testID="watch-up-next" style={{width: compact ? sceneAfter ? 424 : 360 : sceneAfter ? 780 : 680, padding: compact ? 14 : 26, borderRadius: radius,
    boxShadow: compact ? 'inset 0 0 0 1px rgba(237,245,250,.12), 0 20px 40px -16px rgba(0,0,0,.9)' : '0 30px 80px -20px rgba(0,0,0,.85), inset 0 0 0 1px rgba(237,245,250,.1)'}}>
    <View pointerEvents="none" style={[StyleSheet.absoluteFill, {borderRadius: radius, overflow: 'hidden'}]}>
      {theme.isTV && glass.nativeBlur && <BlurView style={StyleSheet.absoluteFill} blurType="dark" blurAmount={24} reducedTransparencyFallbackColor={theme.colors.ink}/>}
      <View style={[StyleSheet.absoluteFill, {backgroundColor: compact ? 'rgba(7,16,24,.85)' : 'rgba(9,19,27,.84)'}]}/>
    </View>
    <View style={{flexDirection: 'row', alignItems: 'center', gap: compact ? 13 : 24}}>
      <View style={{...still, borderRadius: compact ? 8 : 12, overflow: 'hidden', backgroundColor: '#0b1822'}}>
        <Artwork source={artwork} width={still.width} height={still.height} style={{opacity: .34}}/>
      </View>
      <View style={{flex: 1, minWidth: 0}}>
        <Text style={{fontFamily: 'Manrope-Bold', fontSize: compact ? 12.5 : 20, fontWeight: '700', color: compact ? theme.colors.textTertiary : theme.colors.accentStrong}}>{t('player.post.upNext')}</Text>
        <Text numberOfLines={compact ? 1 : 2} style={{fontFamily: 'Manrope-ExtraBold', fontSize: compact ? 16.5 : 34, lineHeight: compact ? 19 : 37.4, fontWeight: '800',
          letterSpacing: compact ? -.25 : -.85, marginTop: compact ? 2 : 6, marginBottom: compact ? 0 : 4, color: theme.colors.text}}>{item.title}</Text>
        {(code || minutes) && <Text style={{fontFamily: compact ? 'Manrope-SemiBold' : 'Manrope-Regular', fontSize: compact ? 12.5 : 22, fontWeight: compact ? '600' : '400', marginTop: compact ? 2 : 0, color: compact ? theme.colors.textTertiary : theme.colors.textSecondary}}>{[code, minutes].filter(Boolean).join(' · ')}</Text>}
        {countdown && <View style={{flexDirection: 'row', alignItems: 'baseline', marginTop: compact ? 5 : 10}}>
          {countdown.automatic
            ? <Text style={{fontFamily: 'Manrope-SemiBold', fontSize: compact ? 13 : 20, fontWeight: '600', fontVariant: ['tabular-nums'], color: compact ? theme.colors.textSecondary : theme.colors.textTertiary}}>{t('player.post.playingIn', {seconds: Math.ceil(countdown.seconds)})}</Text>
            : <><Text style={{fontFamily: 'Manrope-SemiBold', fontSize: compact ? 13 : 20, fontWeight: '600', fontVariant: ['tabular-nums'], color: compact ? theme.colors.textSecondary : theme.colors.textTertiary}}>{t('player.post.startsIn', {time: ''})}</Text>
              <Text style={{fontFamily: 'Manrope-SemiBold', fontSize: compact ? 13 : 20, fontWeight: '600', fontVariant: ['tabular-nums'], color: theme.colors.text}}>{clockText(countdown.seconds)}</Text></>}
        </View>}
      </View>
    </View>
    {countdown?.automatic && <View style={{height: compact ? 3 : 4, borderRadius: 2, marginTop: compact ? 12 : 18, overflow: 'hidden', backgroundColor: 'rgba(237,245,250,.16)'}}>
      <View style={{height: compact ? 3 : 4, borderRadius: 2, backgroundColor: '#fff', width: `${Math.max(0, Math.min(1, countdown.seconds / Math.max(1, countdownTotal))) * 100}%`}}/>
    </View>}
    <View style={{flexDirection: 'row', flexWrap: postPlay ? 'wrap' : undefined, gap: compact ? 8 : 18, marginTop: compact ? 12 : 24}}>
      {button('play', 'primary', 'i-play', t('player.post.playNow'), onPlayNow)}
      {postPlay ? <>
        {countdown?.automatic && button('cancel', 'glass', undefined, t('action.cancel'), postPlay.onCancel)}
        {button('replay', 'glass', 'i-replay', t('player.post.replay'), postPlay.onReplay)}
        {button('close', 'glass', undefined, t('player.post.close'), postPlay.onClose)}
      </> : <>
        {sceneAfter && button('skip', 'glass', 'i-next', t('player.skip', {kind: 'credits'}), onSkipCredits)}
        {button('watch', 'glass', undefined, t('player.post.watchCredits'), onWatchCredits)}
      </>}
    </View>
    {sceneAfter && !compact && <View style={{flexDirection: 'row', alignItems: 'center', gap: 12, marginTop: 20}}>
      <View style={{width: 9, height: 9, borderRadius: 4.5, backgroundColor: theme.colors.accentStrong}}/>
      <Text style={{fontFamily: 'Manrope-SemiBold', fontSize: 20, fontWeight: '600', color: theme.colors.textSecondary}}>{t('player.post.sceneAfterSkip')}</Text>
    </View>}
  </View>;
}
