import React, {useEffect, useMemo, useSyncExternalStore} from 'react';
import {ScrollView, Text, View} from 'react-native';
import Svg, {Defs, LinearGradient, Rect, Stop} from 'react-native-svg';
import {DetailService, type DetailApi, type DetailScope, type PersonalIntent} from '../../../../../packages/client-core/src/detail';
import type {ContentEntry} from '../../../../../packages/client-core/src/library-content';
import {defaultI18n} from '../../../../../packages/i18n/src';
import {Artwork, type ArtworkSource} from '../../kit/artwork';
import {Button, Focusable} from '../../kit/controls';
import {FocusRegion} from '../../kit/focus';
import {Icon} from '../../kit/Icon';
import {useTheme} from '../../kit/theme';

/** tv-player 7: a film with nothing queued ends on its own backdrop, with Replay
 * focused, a rating and the server's related titles. The episode cards stay as
 * they are; this screen is the end of a title the queue has nothing after. */
export type NativeWatchPostPlayProps = Readonly<{
  entry: ContentEntry;
  api: DetailApi;
  scope: DetailScope;
  requestId: () => Promise<string>;
  current: () => boolean;
  artwork: (path?: string) => ArtworkSource | undefined;
  onReplay: () => void;
  onClose: () => void;
  onOpen: (entry: ContentEntry) => void;
  reportError: (error: unknown) => void;
  /** Phone geometry (phone-player 6): facts left, 85-wide cards right. */
  compact?: boolean;
}>;

export function NativeWatchPostPlay({entry, api, scope, requestId, current, artwork, onReplay, onClose, onOpen, reportError, compact = false}: NativeWatchPostPlayProps) {
  const theme = useTheme(), t = defaultI18n.t;
  const service = useMemo(() => new DetailService({api, scope, requestId}), [api, scope, requestId]);
  const snapshot = useSyncExternalStore(service.subscribe, service.getSnapshot);
  useEffect(() => {
    if (entry.libraryId) void service.select({libraryId: entry.libraryId, itemId: entry.id}).catch(error => {if (current()) reportError(error);});
    return () => service.dispose();
  }, [service, entry.libraryId, entry.id, current, reportError]);
  const detail = snapshot.data && snapshot.data.item.id === entry.id ? snapshot.data : undefined;
  const rating = detail?.personal.rating ?? null;
  const stars = rating === null ? 0 : Math.round(rating / 2);
  const rate = (value: number | null) => {
    if (!current() || !detail) return;
    const intent: PersonalIntent = {action: 'rating', value};
    void service.mutate(intent).catch(error => {if (current()) reportError(error);});
  };
  const related = detail?.related?.rows.flatMap(row => row.entries).filter((row, index, all) => all.findIndex(other => other.id === row.id) === index).slice(0, 12) ?? [];
  const hours = entry.duration && entry.duration >= 3600 ? Math.floor(entry.duration / 3600) : undefined;
  const minutes = entry.duration ? Math.round(entry.duration % 3600 / 60) : undefined;
  const runtime = entry.duration ? hours ? t('next.kit.duration.hoursMinutes', {hours, minutes}) : t('next.kit.duration.minutes', {minutes: Math.round(entry.duration / 60)}) : undefined;
  const ratingRow = detail && <View style={{flexDirection: 'row', alignItems: 'center', gap: compact ? 0 : 12, marginBottom: compact ? 16 : 0, marginTop: compact ? 0 : 30}}>
        <Text style={{fontFamily: 'Manrope-Bold', fontSize: compact ? 13.5 : 22, fontWeight: '700', marginRight: compact ? 2 : 10, color: compact ? theme.colors.textSecondary : theme.colors.textTertiary}}>{t('player.rateThis')}</Text>
        {[1, 2, 3, 4, 5].map(star => <Focusable key={star} focusId={`watch-post-star-${star}`} testID={`watch-post-star-${star}`} accessibilityRole="button" accessibilityLabel={t('player.rated', {stars: star})}
          accessibilityState={{selected: star <= stars}} onPress={() => rate(star === stars ? null : star * 2)}
          style={{width: compact ? 32 : 56, height: compact ? 32 : 56, borderRadius: compact ? 8 : 28, alignItems: 'center', justifyContent: 'center', backgroundColor: star <= stars ? 'rgba(92,179,226,.22)' : compact ? 'transparent' : 'rgba(237,245,250,.1)'}}>
          <Icon name={star <= stars ? 'i-star-f' : 'i-star'} size={compact ? 22 : 28} color={star <= stars ? theme.colors.accentStrong : compact ? theme.colors.textTertiary : theme.colors.textSecondary}/>
        </Focusable>)}
      </View>;
  return <View testID="watch-post-play" style={{position: 'absolute', inset: 0}}>
    <Artwork source={artwork(entry.backdropUrl)} style={{position: 'absolute', inset: 0}}/>
    <Svg pointerEvents="none" width="100%" height="100%" style={{position: 'absolute', inset: 0}}><Defs>
      <LinearGradient id="post-scrim" x1="0" y1="0" x2="1" y2="0"><Stop offset="0" stopColor="#071018" stopOpacity={.94}/><Stop offset="0.55" stopColor="#071018" stopOpacity={.55}/><Stop offset="1" stopColor="#071018" stopOpacity={.2}/></LinearGradient>
      <LinearGradient id="post-foot" x1="0" y1="1" x2="0" y2="0"><Stop offset="0" stopColor="#071018" stopOpacity={.95}/><Stop offset="1" stopColor="#071018" stopOpacity={0}/></LinearGradient>
    </Defs><Rect width="100%" height="100%" fill="url(#post-scrim)"/><Rect width="100%" height="100%" fill="url(#post-foot)"/></Svg>
    <FocusRegion id="watch-post" orientation="vertical" entry="watch-post-replay" edges={related.length ? {down: 'watch-post-related'} : undefined} style={compact
      ? {position: 'absolute', left: 60, width: 330, top: 0, bottom: 0, justifyContent: 'center'}
      : {position: 'absolute', left: 96, top: 84, width: 1000}}>
      <Text style={{fontFamily: 'Manrope-SemiBold', fontSize: compact ? 13.5 : 24, fontWeight: '700', marginBottom: compact ? 0 : 16, color: compact ? theme.colors.textTertiary : theme.colors.textSecondary}}>{t('player.post.finished')}</Text>
      <Text numberOfLines={2} style={{fontFamily: 'Manrope-ExtraBold', fontSize: compact ? 44 : 84, lineHeight: compact ? 43 : 84, fontWeight: '800',
        letterSpacing: compact ? -.045 : -3.6, marginTop: compact ? 6 : 0, marginBottom: compact ? 10 : 18, color: theme.colors.text}}>{entry.displayTitle ?? entry.title}</Text>
      <View style={{flexDirection: 'row', alignItems: 'center', gap: compact ? 12 : 26, marginBottom: compact ? 14 : 30}}>
        {entry.year !== undefined && <Text style={{fontFamily: 'Manrope-ExtraBold', fontSize: compact ? 13 : 24, fontWeight: '800', color: theme.colors.text}}>{defaultI18n.number(entry.year, {useGrouping: false})}</Text>}
        {runtime && <Text style={{fontFamily: 'Manrope-SemiBold', fontSize: compact ? 13 : 24, fontWeight: '600', color: theme.colors.textSecondary}}>{runtime}</Text>}
      </View>
      {compact && ratingRow}
      <View style={{flexDirection: 'row', alignItems: 'center', gap: compact ? 10 : 18}}>
        <Button focusId="watch-post-replay" testID="watch-post-replay" variant="primary" size={compact ? 'compact' : 'hero'} hasTVPreferredFocus={!compact} icon="i-replay"
          style={compact ? {height: 40, paddingHorizontal: 15, borderRadius: 10} : undefined} textStyle={compact ? {fontSize: 14.5, fontWeight: '700'} : undefined} onPress={onReplay}>{t('player.post.replay')}</Button>
        <Button focusId="watch-post-close" testID="watch-post-close" variant="glass" size={compact ? 'compact' : 'hero'}
          style={compact ? {height: 40, paddingHorizontal: 15, borderRadius: 10} : undefined} textStyle={compact ? {fontSize: 14.5, fontWeight: '700'} : undefined} onPress={onClose}>{t('player.post.close')}</Button>
      </View>
      {!compact && ratingRow}
    </FocusRegion>
    {related.length > 0 && <View style={compact ? {position: 'absolute', right: 60, width: 370, top: 0, bottom: 0, justifyContent: 'center'} : {position: 'absolute', left: 96, right: 96, top: 578}}>
      <Text style={{fontFamily: 'Manrope-ExtraBold', fontSize: compact ? 15 : 30, fontWeight: compact ? '700' : '800', marginBottom: compact ? 10 : 18, color: theme.colors.text}}>{t('player.moreLike', {title: entry.displayTitle ?? entry.title})}</Text>
      <ScrollView horizontal showsHorizontalScrollIndicator={false}>
        <FocusRegion id="watch-post-related" orientation="horizontal" entry={`watch-post-related-${related[0]!.id}`} edges={{up: 'watch-post'}} style={{flexDirection: 'row', gap: compact ? 10 : 28}}>
          {related.map(row => <Focusable key={row.id} focusId={`watch-post-related-${row.id}`} testID={`watch-post-related-${row.id}`} accessibilityRole="button" accessibilityLabel={row.title}
            onPress={() => {if (current()) onOpen(row);}} style={{width: compact ? 85 : 184}}>
            {({focused}) => <>
              <View style={{width: compact ? 85 : 184, height: compact ? 128 : 276, borderRadius: compact ? 8 : 14, overflow: 'hidden', backgroundColor: '#0b1822', ...(focused ? {borderWidth: 2, borderColor: theme.colors.focus} : {})}}>
                <Artwork source={artwork(row.posterUrl)} width={compact ? 85 : 184} height={compact ? 128 : 276}/>
              </View>
              <Text numberOfLines={1} style={{fontFamily: 'Manrope-SemiBold', fontSize: compact ? 12 : 22, fontWeight: '600', marginTop: compact ? 7 : 10, color: focused ? theme.colors.accentStrong : theme.colors.text}}>{row.title}</Text>
              {row.year !== undefined && <Text style={{fontFamily: 'Manrope-Medium', fontSize: compact ? 11.5 : 20, fontWeight: '500', color: theme.colors.textTertiary}}>{defaultI18n.number(row.year, {useGrouping: false})}</Text>}
            </>}
          </Focusable>)}
        </FocusRegion>
      </ScrollView>
    </View>}
  </View>;
}
