import React from 'react';
import {ScrollView, Text, View, useWindowDimensions} from 'react-native';
import Svg, {Defs, LinearGradient, Rect, Stop} from 'react-native-svg';
import {defaultI18n} from '../../../../../packages/i18n/src';
import {Artwork, type ArtworkSource} from '../artwork';
import {Button, Focusable} from '../controls';
import {FocusRegion} from '../focus';
import {Icon} from '../Icon';
import {useTheme} from '../theme';
import {TVRow} from '../layout/TVRow';

/** One related title on the end card: its poster, title and year. */
export type PlayerEndCardTitle = Readonly<{id: string; title: string; year?: number; poster?: ArtworkSource; onPress: () => void}>;
export type PlayerEndCardProps = Readonly<{
  title: string; year?: number;
  /** "2 h 1 min". */
  runtime?: string;
  /** The film's own backdrop, under the scrims. */
  backdrop?: ArtworkSource;
  /** Rate this: the viewer's stars (0 to 5); left out until the title's details have arrived. */
  rating?: Readonly<{stars: number; onRate: (star: number) => void}>;
  related: readonly PlayerEndCardTitle[];
  onReplay: () => void;
  onClose: () => void;
  /** Phone geometry (phone-player 6): facts left, 85-wide cards right. */
  compact?: boolean;
}>;

/** tv-player 7, phone-player 6d: the end of a film the queue has nothing after, drawn on the film's own backdrop with
 * Replay focused, Rate this and the server's related titles. The drawn part of `screens/watch/PostPlayScreen`, which
 * reads the title and passes it in; the web's is `PlayerEndCard` too. */
export function PlayerEndCard({title, year, runtime, backdrop, rating, related, onReplay, onClose, compact = false}: PlayerEndCardProps) {
  const theme = useTheme(), t = defaultI18n.t, window = useWindowDimensions();
  // phone-player 6d draws a phone on its side: the words left, the cards right. Held upright the two stack in the
  // window's middle, so neither covers the other.
  const upright = compact && window.height > window.width;
  const ratingRow = rating && <View style={{flexDirection: 'row', alignItems: 'center', gap: compact ? 0 : 12, marginBottom: compact ? 16 : 0, marginTop: compact ? 0 : 30}}>
        <Text style={{fontFamily: 'Manrope-Bold', fontSize: compact ? 13.5 : 22, fontWeight: '700', marginRight: compact ? 2 : 10, color: compact ? theme.colors.textSecondary : theme.colors.textTertiary}}>{t('player.rateThis')}</Text>
        {[1, 2, 3, 4, 5].map(star => <Focusable key={star} focusId={`watch-post-star-${star}`} testID={`watch-post-star-${star}`} accessibilityRole="button" accessibilityLabel={t('player.rated', {stars: star})}
          accessibilityState={{selected: star <= rating.stars}} onPress={() => rating.onRate(star)}
          style={{width: compact ? 32 : 56, height: compact ? 32 : 56, borderRadius: compact ? 8 : 28, alignItems: 'center', justifyContent: 'center', backgroundColor: star <= rating.stars ? 'rgba(92,179,226,.22)' : compact ? 'transparent' : 'rgba(237,245,250,.1)'}}>
          <Icon name={star <= rating.stars ? 'i-star-f' : 'i-star'} size={compact ? 22 : 28} color={star <= rating.stars ? theme.colors.accentStrong : compact ? theme.colors.textTertiary : theme.colors.textSecondary}/>
        </Focusable>)}
      </View>;
  return <View testID="watch-post-play" style={{position: 'absolute', inset: 0}}>
    <Artwork source={backdrop} style={{position: 'absolute', inset: 0}}/>
    <Svg pointerEvents="none" width="100%" height="100%" style={{position: 'absolute', inset: 0}}><Defs>
      <LinearGradient id="post-scrim" x1="0" y1="0" x2="1" y2="0"><Stop offset="0" stopColor="#071018" stopOpacity={.94}/><Stop offset="0.55" stopColor="#071018" stopOpacity={.55}/><Stop offset="1" stopColor="#071018" stopOpacity={.2}/></LinearGradient>
      <LinearGradient id="post-foot" x1="0" y1="1" x2="0" y2="0"><Stop offset="0" stopColor="#071018" stopOpacity={.95}/><Stop offset="1" stopColor="#071018" stopOpacity={0}/></LinearGradient>
    </Defs><Rect width="100%" height="100%" fill="url(#post-scrim)"/><Rect width="100%" height="100%" fill="url(#post-foot)"/></Svg>
    <View pointerEvents="box-none" style={upright ? {position: 'absolute', inset: 0, justifyContent: 'center', paddingHorizontal: 24, gap: 32} : {position: 'absolute', inset: 0}}>
    <FocusRegion id="watch-post" orientation="vertical" entry="watch-post-replay" edges={related.length ? {down: 'watch-post-related'} : undefined} style={upright ? undefined : compact
      ? {position: 'absolute', left: 60, width: 330, top: 0, bottom: 0, justifyContent: 'center'}
      : {position: 'absolute', left: 96, top: 84, width: 1000}}>
      <Text style={{fontFamily: 'Manrope-SemiBold', fontSize: compact ? 13.5 : 24, fontWeight: '700', marginBottom: compact ? 0 : 16, color: compact ? theme.colors.textTertiary : theme.colors.textSecondary}}>{t('player.post.finished')}</Text>
      <Text numberOfLines={2} style={{fontFamily: 'Manrope-ExtraBold', fontSize: compact ? 44 : 84, lineHeight: compact ? 43 : 84, fontWeight: '800',
        letterSpacing: compact ? -.045 : -3.6, marginTop: compact ? 6 : 0, marginBottom: compact ? 10 : 18, color: theme.colors.text}}>{title}</Text>
      <View style={{flexDirection: 'row', alignItems: 'center', gap: compact ? 12 : 26, marginBottom: compact ? 14 : 30}}>
        {year !== undefined && <Text style={{fontFamily: 'Manrope-ExtraBold', fontSize: compact ? 13 : 24, fontWeight: '800', color: theme.colors.text}}>{defaultI18n.number(year, {useGrouping: false})}</Text>}
        {runtime && <Text style={{fontFamily: 'Manrope-SemiBold', fontSize: compact ? 13 : 24, fontWeight: '600', color: theme.colors.textSecondary}}>{runtime}</Text>}
      </View>
      {compact && ratingRow}
      <View style={{flexDirection: 'row', alignItems: 'center', gap: compact ? 10 : 18}}>
        <Button focusId="watch-post-replay" testID="watch-post-replay" variant="primary" size={compact ? 'compact' : 'hero'} preferredFocus={!compact} icon="i-replay"
          style={compact ? {height: 40, paddingHorizontal: 15, borderRadius: 10} : undefined} textStyle={compact ? {fontSize: 14.5, fontWeight: '700'} : undefined} onPress={onReplay}>{t('player.post.replay')}</Button>
        <Button focusId="watch-post-close" testID="watch-post-close" variant="glass" size={compact ? 'compact' : 'hero'}
          style={compact ? {height: 40, paddingHorizontal: 15, borderRadius: 10} : undefined} textStyle={compact ? {fontSize: 14.5, fontWeight: '700'} : undefined} onPress={onClose}>{t('player.post.close')}</Button>
      </View>
      {!compact && ratingRow}
    </FocusRegion>
    {related.length > 0 && <View style={upright ? undefined : compact ? {position: 'absolute', right: 60, width: 370, top: 0, bottom: 0, justifyContent: 'center'} : {position: 'absolute', left: 96, right: 96, top: 578}}>
      <Text style={{fontFamily: 'Manrope-ExtraBold', fontSize: compact ? 15 : 30, fontWeight: compact ? '700' : '800', marginBottom: compact ? 10 : 18, color: theme.colors.text}}>{t('player.moreLike', {title})}</Text>
      {/* A horizontal list does not grow to its column's height, so the row stays centred beside the words (phone-player 6d). */}
      <TVRow><ScrollView horizontal showsHorizontalScrollIndicator={false} style={{flexGrow: 0}}>
        <FocusRegion id="watch-post-related" orientation="horizontal" entry={`watch-post-related-${related[0]!.id}`} edges={{up: 'watch-post'}} style={{flexDirection: 'row', gap: compact ? 10 : 28}}>
          {related.map(row => <Focusable key={row.id} focusId={`watch-post-related-${row.id}`} testID={`watch-post-related-${row.id}`} accessibilityRole="button" accessibilityLabel={row.title}
            onPress={row.onPress} style={{width: compact ? 85 : 184}}>
            {({focused}) => <>
              <View style={{width: compact ? 85 : 184, height: compact ? 128 : 276, borderRadius: compact ? 8 : 14, overflow: 'hidden', backgroundColor: '#0b1822', ...(focused ? {borderWidth: 2, borderColor: theme.colors.focus} : {})}}>
                <Artwork source={row.poster} width={compact ? 85 : 184} height={compact ? 128 : 276}/>
              </View>
              <Text numberOfLines={1} style={{fontFamily: 'Manrope-SemiBold', fontSize: compact ? 12 : 22, fontWeight: '600', marginTop: compact ? 7 : 10, color: focused ? theme.colors.accentStrong : theme.colors.text}}>{row.title}</Text>
              {row.year !== undefined && <Text style={{fontFamily: 'Manrope-Medium', fontSize: compact ? 11.5 : 20, fontWeight: '500', color: theme.colors.textTertiary}}>{defaultI18n.number(row.year, {useGrouping: false})}</Text>}
            </>}
          </Focusable>)}
        </FocusRegion>
      </ScrollView></TVRow>
    </View>}
    </View>
  </View>;
}
