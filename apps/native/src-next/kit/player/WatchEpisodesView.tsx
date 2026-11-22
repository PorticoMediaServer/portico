import React from 'react';
import {ScrollView, Text, View} from 'react-native';
import Svg, {Defs, LinearGradient, Rect, Stop} from 'react-native-svg';
import {defaultI18n} from '../../../../../packages/i18n/src';
import {Artwork} from '../artwork';
import {Button, Focusable, Switch} from '../controls';
import {EpisodeTile} from '../cards';
import {FocusRegion} from '../focus';
import {textLines, useTheme} from '../theme';
import {shellType} from '../shell/visual';
import {NativeSeasonsSheet} from '../layout/ShowSeasonsSheet';
import {Quiet} from '../states';
import {TVRow} from '../layout/TVRow';
import type {useWatchEpisodes} from '../../pages/shared/player/useWatchEpisodes';

export type WatchEpisodesViewProps = ReturnType<typeof useWatchEpisodes>;

/** The player's episode list: a band of title-page tiles over the picture on a TV (tv-player 1), the sideways panel and
 * the upright tab's sheet on a phone or tablet (collections 37/38), or the following episodes alone (foldable4/5). */
export function WatchEpisodesView(view: WatchEpisodesViewProps) {
  const {pane, playing, onClose, episodes, following, initialId, loading, count, seasonLabel, seasons, selected, interactive, total, hideWatched,
    scroll, first, seasonOpener, seasonsOpen, openSeasons, closeSeasons, episode, nearEnd, selectSeason, moreSeasons, sheetHideWatched, panelHideWatched} = view;
  const theme = useTheme(), t = defaultI18n.t;
  const row = (value: (typeof episodes)[number]) => {
    const {entry, isPlaying, isNext, title, label, length, progress, artwork, play} = episode(value);
    return <Focusable key={entry.id} ref={entry.id === initialId ? first : undefined} focusId={`episodes-${entry.id}`} testID={`episodes-${entry.id}`} focusKind="row" accessibilityRole="button"
      accessibilityLabel={[title, isPlaying ? t('player.miniPlaying') : isNext ? t('player.queue') : undefined].filter(Boolean).join(', ')}
      onPress={play} onFocus={() => {}} style={({focused}) => ({minHeight: pane ? 66 : 64, paddingHorizontal: pane ? 8 : 10, paddingVertical: 8, borderRadius: 10,
        flexDirection: 'row', alignItems: 'center', gap: 12, backgroundColor: focused ? 'rgba(237,245,250,.12)' : 'transparent'})}>
      <View style={{width: pane ? 88 : 84, height: pane ? 50 : 48, borderRadius: 8, overflow: 'hidden', backgroundColor: theme.colors.raised}}>
        <Artwork source={artwork} width={pane ? 88 : 84} height={pane ? 50 : 48} style={{opacity: .45}}/>
      </View>
      <View style={{flex: 1, minWidth: 0}}>
        <View style={{flexDirection: 'row', alignItems: 'center', gap: 8}}>
          <Text {...textLines(1)} style={[shellType(15, 600, 'normal', 0, 1), {flexShrink: 1, color: theme.colors.text}]}>{title}</Text>
          {isPlaying && <View style={{flexDirection: 'row', alignItems: 'center', gap: 6}}>
            <View style={{flexDirection: 'row', alignItems: 'flex-end', gap: 2, height: 12}}>{[.55, 1, .38].map((factor, index) =>
              <View key={index} style={{width: 3, height: 12 * factor, borderRadius: 1.5, backgroundColor: theme.colors.accentStrong}}/>)}</View>
            <Text style={[shellType(12, 700, 'normal', 0, 1), {color: theme.colors.accentStrong}]}>{t('player.miniPlaying')}</Text>
          </View>}
          {isNext && <Text style={[shellType(12, 700, 'normal', 0, 1), {color: theme.colors.textSecondary}]}>{t('player.queue')}</Text>}
        </View>
        {/* DESIGN: facts are separated by spacing, never a middle dot. */}
        <View style={{flexDirection: 'row', columnGap: 12, marginTop: 2, minWidth: 0}}>
          {[label?.text, length].filter(Boolean).map((part, at) =>
            <Text key={at} {...textLines(1)} style={[shellType(12.5, 600, 'normal', 0, 1), {flexShrink: at ? 0 : 1, color: theme.colors.textTertiary}]}>{part}</Text>)}
        </View>
        {progress !== undefined && <View style={{height: 3, borderRadius: 2, marginTop: 5, backgroundColor: 'rgba(237,245,250,.18)'}}>
          <View style={{height: 3, borderRadius: 2, width: `${progress * 100}%`, backgroundColor: theme.colors.accentStrong}}/></View>}
      </View>
      {isPlaying && playing && <View style={{width: 4, height: 24, borderRadius: 2, backgroundColor: theme.colors.accentStrong}}/>}
    </Focusable>;
  };
  if (theme.isTV && !pane) {
    // tv-player 1 / F-PLAYER-27: this season's episodes as a band over the bottom of the picture,
    // with the title page's tiles; the playing one is marked and focus starts on it.
    const tile = (value: (typeof episodes)[number]) => {
      const {entry, isPlaying, isNext, title, label, length, progress, artwork, stillMissing, play} = episode(value);
      return <EpisodeTile key={entry.id} menuAnchorRef={entry.id === initialId ? first : undefined} focusId={`episodes-${entry.id}`} testID={`episodes-${entry.id}`}
        layout="tile" width={384} title={title} label={label ? [label] : []} length={length}
        status={isPlaying ? t('player.miniPlaying') : isNext ? t('player.queue') : undefined} watched={entry.watched}
        progress={progress} artwork={artwork} backdropFallback={stillMissing} primaryAction="play" onPlay={play}/>;
    };
    return <FocusRegion id="watch-episodes" orientation="free" entry={initialId ? `episodes-${initialId}` : 'watch-episodes-seasons'} enabled={interactive && !seasonsOpen} style={{flex: 1, justifyContent: 'flex-end'}}>
      <Svg pointerEvents="none" width="100%" height={560} style={{position: 'absolute', left: 0, right: 0, bottom: 0}}><Defs><LinearGradient id="watch-episodes-band" x1="0" y1="0" x2="0" y2="1">
        <Stop offset="0" stopColor={theme.colors.ink} stopOpacity={0}/><Stop offset="0.38" stopColor={theme.colors.ink} stopOpacity={.78}/><Stop offset="1" stopColor={theme.colors.ink} stopOpacity={.94}/></LinearGradient></Defs>
        <Rect width="100%" height="100%" fill="url(#watch-episodes-band)"/></Svg>
      <View testID="watch-episodes-panel" style={{paddingBottom: 56}}>
        <View style={{flexDirection: 'row', alignItems: 'center', gap: 24, paddingHorizontal: 96, marginBottom: 8}}>
          <Text accessibilityRole="header" {...textLines(1)} style={[theme.type.heading, {color: theme.colors.text, flexShrink: 1}]}>{t('title.episodes')}</Text>
          <Button ref={seasonOpener} testID="watch-episodes-seasons" focusId="watch-episodes-seasons" variant="glass" trailingIcon="i-chdown" onPress={openSeasons}>{seasonLabel}</Button>
        </View>
        {/* Room above and below for the focused tile's 1.08 scale and ring inside the clipping scroll. */}
        <TVRow><ScrollView ref={scroll} horizontal showsHorizontalScrollIndicator={false} contentContainerStyle={{paddingHorizontal: 96, paddingVertical: 28, gap: 28}} scrollEventThrottle={32}
          onScroll={event => {
            const {contentOffset, contentSize, layoutMeasurement} = event.nativeEvent;
            nearEnd(contentSize.width - contentOffset.x - layoutMeasurement.width, layoutMeasurement.width);
          }}>
          {loading && !episodes.length && <Text style={[theme.type.body, {color: theme.colors.textTertiary}]}>{t('next.states.loading')}</Text>}
          {episodes.map(tile)}
        </ScrollView></TVRow>
      </View>
      <NativeSeasonsSheet open={seasonsOpen} returnFocusRef={seasonOpener} onOpenChange={value => {if (value) openSeasons(); else closeSeasons();}} seasons={seasons}
        total={total} selectedId={selected} onSelect={selectSeason} onMore={moreSeasons} hideWatched={hideWatched} onHideWatched={sheetHideWatched}/>
    </FocusRegion>;
  }
  const body = <View testID="watch-episodes-panel" style={{flex: 1, backgroundColor: pane ? 'transparent' : theme.colors.raised, borderRadius: pane || theme.isTV ? 0 : 20, overflow: 'hidden'}}>
    {!pane && <View style={{flexDirection: 'row', alignItems: 'center', gap: 10, paddingHorizontal: 16, paddingTop: 12, paddingBottom: 8}}>
      <Text accessibilityRole="header" {...textLines(1)} style={[shellType(18, 800, 'normal', -.02 / 18, 1), {color: theme.colors.text, flexShrink: 1}]}>{t('title.episodes')}</Text>
      <View style={{flex: 1}}/>
      <Button ref={seasonOpener} testID="watch-episodes-seasons" variant="glass" size="small" trailingIcon="i-chdown" onPress={openSeasons}>{seasonLabel}</Button>
      <Button testID="watch-episodes-close" variant="text" iconOnly icon="i-close" accessibilityLabel={t('action.close')} onPress={onClose}/>
    </View>}
    {!pane && <View style={{flexDirection: 'row', alignItems: 'center', gap: 10, paddingHorizontal: 16, paddingBottom: 6}}>
      {count !== undefined && <Text style={[shellType(12.5, 600, 'normal', 0, 1), {color: theme.colors.textTertiary}]}>
        {t('pref.unit.episodes', {count})}</Text>}
      <View style={{flex: 1}}/>
      <Text style={[theme.type.bodyStrong, shellType(12.5, 600, 'normal', 0, 1), {color: theme.colors.textSecondary}]}>{t('next.controls.hideWatched')}</Text>
      <Switch testID="watch-episodes-hide-watched" variant={theme.platform === 'phone' ? 'episode' : 'selection'} checked={hideWatched}
        accessibilityLabel={t('next.controls.hideWatched')} onCheckedChange={panelHideWatched}/>
    </View>}
    {pane && following.length > 0 && <Text testID="watch-episodes-pane-heading" accessibilityRole="header" style={[shellType(12.5, 700, 'normal', 0, 1), {paddingHorizontal: 0, paddingBottom: 4,
      color: theme.colors.accentStrong}]}>{t('player.post.upNext')}</Text>}
    <ScrollView ref={scroll} style={{flex: 1}} contentContainerStyle={pane ? {paddingHorizontal: 8, paddingBottom: 12, gap: 10} : {paddingHorizontal: 8, paddingBottom: 16}} scrollEventThrottle={32}
      onScroll={event => {
        // foldable3g/5: the season keeps going as the list reaches its end.
        const {contentOffset, contentSize, layoutMeasurement} = event.nativeEvent;
        nearEnd(contentSize.height - contentOffset.y - layoutMeasurement.height, layoutMeasurement.height);
      }}>
      {loading && !episodes.length && <Quiet><Text style={{padding: 16, color: theme.colors.textTertiary}}>{t('next.states.loading')}</Text></Quiet>}
      {following.map(row)}
    </ScrollView>
    {!pane && <NativeSeasonsSheet open={seasonsOpen} returnFocusRef={seasonOpener} onOpenChange={value => {if (value) openSeasons(); else closeSeasons();}} seasons={seasons}
      total={total} selectedId={selected} onSelect={selectSeason} onMore={moreSeasons}/>}
  </View>;
  return theme.isTV && !pane ? <FocusRegion id="watch-episodes" orientation="vertical" entry={initialId ? `episodes-${initialId}` : 'watch-episodes-seasons'} enabled={interactive && !seasonsOpen} style={{flex: 1}}>{body}</FocusRegion> : body;
}
