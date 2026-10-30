import {TVShelf, tvItemKey} from '../../tv/layout/TVShelf';
import React, {useEffect, useRef, useState} from 'react';
import {ScrollView, Text, View} from 'react-native';
import type {ContentEntry} from '../../../../../packages/client-core/src/library-content';
import type {WorkspaceSeason} from '../../../../../packages/client-core/src/show-workspace';
import {cardText, episodeLabel, type TextEnvironment} from '../../../../../packages/client-core/src/presentation/kit-model';
import {durationText} from '../../../../../packages/client-core/src/presentation/kit-model/text';
import {defaultI18n} from '../../../../../packages/i18n/src';
import {Focusable} from '../../kit/controls';
import {FocusRegion, type FocusRegionConfig} from '../../kit/focus';
import {tvEpisodeRegionMap} from './tv-title-focus';
import {EpisodeTile} from '../../kit/cards';
import type {ArtworkSource} from '../../kit/artwork';
import {Menu, type MenuEntry} from '../../kit/overlays';
import {browseType} from '../../kit/browse/presentation';
import {useTheme} from '../../kit/theme';
import type {NativeTitleEpisodeSnapshot} from './title-episode';

export type TVEpisodeBrowserProps = Readonly<{
  seasons: readonly WorkspaceSeason[];
  focus?: FocusRegionConfig;
  requestedSeasonId?: string;
  selectedSeasonId: string | null;
  episodes: readonly ContentEntry[];
  nextUp?: ContentEntry;
  show: Readonly<{title: string; overview?: string; backdropUrl?: string}>;
  open: NativeTitleEpisodeSnapshot;
  text: TextEnvironment;
  artwork: (path: string | undefined) => ArtworkSource | undefined;
  more: (entry: ContentEntry, anchor: React.RefObject<View | null>) => void;
  onSeasonFocus: () => void;
  onEpisodeFocus: (entry: ContentEntry) => void;
  onSelectSeason: (id: string) => void;
  onPlayEpisode: (entry: ContentEntry) => void;
  onPlaySeason: (id: string) => void;
  onMarkSeason: (id: string, watched: boolean) => void;
  onMoreSeasons: () => void;
  onMoreEpisodes: () => void;
  onRetryEpisode: () => void;
  renderError: (error: unknown, retry: () => void) => React.ReactNode;
}>;

type Focus = Readonly<{kind: 'empty'}> | Readonly<{kind: 'season'; season: WorkspaceSeason}> | Readonly<{kind: 'episode'; entry: ContentEntry}>;

const numbering = (entry: ContentEntry) => entry.absoluteNumber !== undefined ? {mode: 'absolute' as const, number: entry.absoluteNumber}
  : entry.dateBased && entry.airDate ? {mode: 'date' as const, day: entry.airDate}
    : entry.seasonNumber === 0 ? {mode: 'special' as const} : {mode: 'seasonal' as const, season: entry.seasonNumber, episode: entry.episodeNumber};

/** tv-detail frames 5a–5d: the season side list, the horizontal episode row and
 * the focus band that describes whatever holds focus. Only the visible page is
 * drawn; the list pages are the workspace's own. */
export function TVEpisodeBrowser(props: TVEpisodeBrowserProps) {
  const theme = useTheme(), t = defaultI18n.t, {episodes, nextUp} = props;
  const fallback = nextUp && episodes.some(entry => entry.id === nextUp.id) ? nextUp : episodes[0];
  const [focus, setFocus] = useState<Focus>(() => fallback ? {kind: 'episode', entry: fallback} : props.seasons[0] ? {kind: 'season', season: props.seasons[0]} : {kind: 'empty'});
  const [menu, setMenu] = useState<WorkspaceSeason>();
  useEffect(() => {
    const current = focus.kind === 'episode' ? episodes.some(entry => entry.id === focus.entry.id) : focus.kind === 'season' && props.seasons.some(season => season.id === focus.season.id);
    if (current) return;
    const next = nextUp && episodes.some(entry => entry.id === nextUp.id) ? nextUp : episodes[0];
    if (next) setFocus({kind: 'episode', entry: next}); else if (props.seasons[0]) setFocus({kind: 'season', season: props.seasons[0]}); else if (focus.kind !== 'empty') setFocus({kind: 'empty'});
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [episodes, props.seasons, nextUp]);
  const requested = props.requestedSeasonId ?? props.selectedSeasonId;
  const lastRequested = useRef(requested), requestWitness = useRef(requested);
  if (requestWitness.current !== requested) {requestWitness.current = requested; lastRequested.current = requested;}
  const seasonFocus = (season: WorkspaceSeason) => {
    setFocus({kind: 'season', season}); props.onSeasonFocus();
    // Focus followed immediately by accessibility/OK activation is one request,
    // even before the service's requested route has reached this render.
    if (season.id !== lastRequested.current) {lastRequested.current = season.id; props.onSelectSeason(season.id);}
    if (props.seasons.indexOf(season) >= props.seasons.length - 3) props.onMoreSeasons();
  };
  const episodeFocus = (entry: ContentEntry) => {setFocus({kind: 'episode', entry}); props.onEpisodeFocus(entry);};
  const section = props.focus ?? {id: 'tv.episodes'};
  const regions = tvEpisodeRegionMap(section, props.selectedSeasonId, episodes.map(entry => entry.id), nextUp?.id);
  const rowReady = (!props.requestedSeasonId || props.requestedSeasonId === props.selectedSeasonId)
    && (focus.kind !== 'season' || focus.season.id === props.selectedSeasonId);
  const rowLive = useRef(rowReady); rowLive.current = rowReady;
  const selected = (season: WorkspaceSeason) => season.id === props.selectedSeasonId;
  const seasonMenu: MenuEntry[] = menu ? [
    {id: 'play-season', label: t('title.playSeason'), icon: 'i-play', onSelect: () => props.onPlaySeason(menu.id)},
    {id: 'mark-season', label: t(menu.watchedCount !== undefined && menu.episodeCount !== undefined && menu.watchedCount >= menu.episodeCount ? 'title.markSeasonUnwatched' : 'title.markSeasonWatched'),
      icon: 'i-check', onSelect: () => props.onMarkSeason(menu.id, !(menu.watchedCount !== undefined && menu.episodeCount !== undefined && menu.watchedCount >= menu.episodeCount))},
  ] : [];
  return <FocusRegion {...regions.section} style={{flexDirection: 'row', gap: 70}}>
    <FocusRegion {...regions.seasons} order={props.seasons.map(season => season.id)} style={{width: 330}}>
      <ScrollView showsVerticalScrollIndicator={false} onScroll={event => {const {contentOffset, contentSize, layoutMeasurement} = event.nativeEvent;
        if (contentSize.height - contentOffset.y - layoutMeasurement.height < layoutMeasurement.height) props.onMoreSeasons();}} scrollEventThrottle={64}>
        {props.seasons.map(season => {
          const active = selected(season) || (focus.kind === 'season' && focus.season.id === season.id);
          const total = season.episodeCount, watched = season.watchedCount;
          const counted = watched !== undefined && total !== undefined && total > 0;
          return <Focusable key={season.id} testID={`tv-season-${season.id}`} focusId={season.id} focusKind="row" focusRadius={14} accessibilityRole="button"
            accessibilityState={{selected: selected(season)}}
            onFocus={() => seasonFocus(season)} onPress={() => seasonFocus(season)}
            onLongPressAction={() => setMenu(season)} onLongPress={() => setMenu(season)}
            style={{minHeight: 84, marginBottom: 6, paddingHorizontal: 22, paddingVertical: 10, borderRadius: 14, justifyContent: 'center',
              backgroundColor: active ? '#409fd429' : 'transparent', overflow: 'hidden'}}>
            {active && <View pointerEvents="none" style={{position: 'absolute', left: 0, top: 10, bottom: 10, width: 3, borderTopRightRadius: 3, borderBottomRightRadius: 3, backgroundColor: theme.colors.accent}}/>}
            <Text numberOfLines={1} style={[browseType(theme, 24, 700), {color: theme.colors.text}]}>{season.title || t('title.season', {number: season.number})}</Text>
            {counted ? <View style={{flexDirection: 'row', alignItems: 'center', gap: 12, marginTop: 3}}>
              <View style={{width: 72, height: 6, borderRadius: 6, backgroundColor: '#edf5fa29'}}><View style={{height: 6, width: `${Math.min(1, watched! / total!) * 100}%`, borderRadius: 6, backgroundColor: theme.colors.accentStrong}}/></View>
              <Text style={[browseType(theme, 19, 400), {color: theme.colors.textTertiary}]}>{t('title.seasonProgress', {watched, total})}</Text>
            </View>
              : total !== undefined ? <Text style={[browseType(theme, 19, 400), {color: theme.colors.textTertiary, marginTop: 3}]}>{t('count.episodes', {count: total})}</Text> : null}
          </Focusable>;
        })}
      </ScrollView>
      {/* tv-detail frame 5 draws the season hint under the list, where the focus is. */}
      {focus.kind === 'season' && <View style={{flexDirection: 'row', alignItems: 'center', gap: 12, marginTop: 22, marginLeft: 22}}>
        <View style={{paddingHorizontal: 10, minWidth: 44, height: 36, borderRadius: 9, borderWidth: 1.5, borderColor: '#edf5fa4d', justifyContent: 'center'}}>
          <Text style={[browseType(theme, 17, 800), {color: theme.colors.textSecondary}]}>{t('next.layout.holdOk')}</Text></View>
        <Text style={[browseType(theme, 19, 600, 0, 26.6), {color: theme.colors.textTertiary}]}>{t('next.watch.seasonOptions')}</Text>
      </View>}
    </FocusRegion>
    <View style={{flex: 1, minWidth: 0}}>
      {/* A show with nothing loadable draws a plain state, never an empty void. */}
      {focus.kind === 'empty' && rowReady && <Text testID="tv-episodes-empty" accessibilityRole="header" style={[browseType(theme, 32, 700), {color: theme.colors.textSecondary}]}>{t('title.noEpisodes')}</Text>}
      <TVShelf items={rowReady ? episodes : []} focus={{...regions.row, enabled: rowReady}} getFocusId={tvItemKey} getKey={tvItemKey} itemWidth={384} itemHeight={320}
        onVisibleRange={(_first, last) => {if (rowLive.current && last >= episodes.length - 10) props.onMoreEpisodes();}}
        renderItem={(entry, slot) => {
          const model = cardText({id: entry.id, kind: entry.kind, title: entry.displayTitle ?? entry.title, episode: numbering(entry), durationSeconds: entry.duration, positionSeconds: entry.progressSeconds}, 'in-show', props.text);
          const upNext = nextUp?.id === entry.id;
          // tv-detail frames 5 to 8 draw one muted label under the title and the length as a badge on the still.
          return <TVTile entry={entry} title={entry.displayTitle ?? entry.title} label={model.caption} width={384}
            length={entry.duration === undefined ? undefined : durationText(entry.duration, t)} status={upNext ? t('player.post.upNext') : undefined}
            watched={entry.watched} progress={model.progress} artwork={props.artwork(entry.stillUrl ?? props.show.backdropUrl)} backdropFallback={!entry.stillUrl} more={props.more}
            primaryAction="play" focusId={slot.focusId} onFocus={() => {slot.onFocus(); episodeFocus(entry);}} onBlur={slot.onBlur} onPlay={() => {if (rowLive.current) props.onPlayEpisode(entry);}}/>;
        }}/>
      <FocusBand focus={focus} show={props.show} open={props.open} nextUp={nextUp} text={props.text} onRetry={props.onRetryEpisode} renderError={props.renderError}/>
    </View>
    <Menu testID={`${section.id}.season-menu`} open={!!menu} onOpenChange={open => {if (!open) setMenu(undefined);}} title={menu ? menu.title || t('title.season', {number: menu.number}) : ''} items={seasonMenu}/>
  </FocusRegion>;
}

export function TVTile({entry, more, ...props}: React.ComponentProps<typeof EpisodeTile> & {entry: ContentEntry; more: TVEpisodeBrowserProps['more']}) {
  const anchor = useRef<View>(null);
  return <EpisodeTile {...props} menuAnchorRef={anchor} onMore={() => more(entry, anchor)}/>;
}

export function FocusBand({focus, open, text, onRetry, renderError}: {
  focus: Focus; show: TVEpisodeBrowserProps['show']; open: NativeTitleEpisodeSnapshot; nextUp?: ContentEntry; text: TextEnvironment;
  onRetry: () => void; renderError: TVEpisodeBrowserProps['renderError'];
}) {
  const theme = useTheme(), t = defaultI18n.t;
  if (focus.kind === 'empty') return null;
  if (focus.kind === 'season') {
    const season = focus.season;
    // tv-detail frame 5 draws the season summary as one meta line over a two-line synopsis.
    return <View testID="tv-episode-band" style={{marginTop: 30}}>
      <View style={{flexDirection: 'row', flexWrap: 'wrap', alignItems: 'center', columnGap: 24, rowGap: 8, marginBottom: 8}}>
        <Text accessibilityRole="header" style={[browseType(theme, 22, 700), {color: theme.colors.text}]}>{season.title || t('title.season', {number: season.number})}</Text>
        {season.firstAired ? <Text style={[browseType(theme, 22, 400), {color: theme.colors.textTertiary}]}>{season.firstAired.slice(0, 4)}</Text> : null}
        {season.episodeCount !== undefined ? <Text style={[browseType(theme, 22, 400), {color: theme.colors.textTertiary}]}>{t('count.episodes', {count: season.episodeCount})}</Text> : null}
      </View>
      {season.overview ? <Text numberOfLines={2} style={[browseType(theme, 22, 400, 0, 31.9), {color: theme.colors.textSecondary}]}>{season.overview}</Text> : null}
    </View>;
  }
  const entry = focus.entry, data = open.entry?.id === entry.id ? open.data : undefined;
  const episode = data?.item.episode;
  const code = episodeLabel(numbering(entry), 'player', text);
  const credits = data?.metadata.credits ?? [];
  const cast = credits.filter(credit => ['acting', 'cast'].includes(credit.department.toLowerCase()));
  const names = (ids: readonly string[]) => [...new Set(credits.filter(credit => ids.includes(credit.department.toLowerCase())).map(credit => credit.name))];
  const directors = names(['directing', 'director', 'direction']), writers = names(['writing', 'writer', 'writers']);
  const progress = data?.personal.progressSeconds ?? entry.progressSeconds ?? 0, duration = data?.item.duration ?? entry.duration;
  const resuming = progress > 0 && !(data?.personal.watched ?? entry.watched);
  const remaining = duration !== undefined ? durationText(Math.max(0, duration - progress), t) : undefined;
  const rating = data?.facts?.contentRating ?? entry.contentRating;
  const airDate = entry.airDate ?? episode?.airDate;
  return <View testID="tv-episode-band" style={{marginTop: 30, flexDirection: 'row', gap: 24}}>
    <View style={{flex: 640, minWidth: 0}}>
      <Text accessibilityRole="header" numberOfLines={2} style={[browseType(theme, 42, 800, -.03, 45.36), {color: theme.colors.text, marginBottom: 10}]}>{entry.displayTitle ?? entry.title}</Text>
      <View style={{flexDirection: 'row', flexWrap: 'wrap', alignItems: 'center', columnGap: 24, rowGap: 8, marginBottom: 14}}>
        {code ? <Text style={[browseType(theme, 24, 700), {color: theme.colors.text}]}>{code.text}</Text> : null}
        {airDate ? <Text style={[browseType(theme, 24, 400), {color: theme.colors.textSecondary}]}>{defaultI18n.date(airDate, 'long')}</Text> : null}
        {duration !== undefined ? <Text style={[browseType(theme, 24, 400), {color: theme.colors.textSecondary}]}>{durationText(duration, t)}</Text> : null}
        {rating ? <View style={{borderWidth: 1.5, borderColor: '#edf5fa47', borderRadius: 8, paddingHorizontal: 12, paddingVertical: 3}}>
          <Text style={[browseType(theme, 20, 600), {color: theme.colors.text}]}>{rating}</Text></View> : null}
      </View>
      {(data?.item.overview ?? entry.overview) ? <Text numberOfLines={3} style={[browseType(theme, 22, 400, 0, 31.9), {color: theme.colors.textSecondary}]}>{data?.item.overview ?? entry.overview}</Text> : null}
      {resuming && remaining ? <View style={{flexDirection: 'row', alignItems: 'center', gap: 16, marginTop: 18}}>
        <View style={{width: 220, height: 6, borderRadius: 6, backgroundColor: '#edf5fa29'}}><View style={{height: 6, width: `${duration ? Math.min(1, progress / duration) * 100 : 0}%`, borderRadius: 6, backgroundColor: theme.colors.accentStrong}}/></View>
        <Text style={[browseType(theme, 22, 700), {color: theme.colors.text}]}>{t('entry.resume')}<Text style={{fontWeight: '500', color: theme.colors.textSecondary}}>, {t('next.kit.remaining', {time: remaining})}</Text></Text>
      </View> : data?.personal.watched ? <Text style={[browseType(theme, 22, 700), {color: theme.colors.textSecondary, marginTop: 18}]}>{t('card.watched')}</Text> : null}
      {open.phase === 'error' && open.error ? <View style={{marginTop: 12}}>{renderError(open.error, onRetry)}</View> : null}
    </View>
    <View style={{flex: 516, minWidth: 0, paddingTop: 8, gap: 14}}>
      {cast.length > 0 && <View style={{flexDirection: 'row', gap: 16}}>
        <Text style={[browseType(theme, 22, 400), {color: theme.colors.textTertiary, width: 128}]}>{t('next.watch.guestStars')}</Text>
        <View style={{flex: 1, gap: 6}}>
          {cast.slice(0, 3).map(credit => <Text key={credit.id} style={[browseType(theme, 22, 600), {color: theme.colors.text}]}>{credit.name}
            {credit.role ? <Text style={{fontWeight: '500', color: theme.colors.textTertiary}}>{'  '}{credit.role}</Text> : null}</Text>)}
          {cast.length > 3 ? <Text style={[browseType(theme, 22, 500), {color: theme.colors.textTertiary}]}>{t('next.watch.andMore', {count: cast.length - 3})}</Text> : null}
        </View>
      </View>}
      {directors.length > 0 && <Text style={[browseType(theme, 22, 600), {color: theme.colors.text}]}>{t('title.directedBy', {names: directors.join(', ')})}</Text>}
      {writers.length > 0 && <Text style={[browseType(theme, 22, 600), {color: theme.colors.text}]}>{t('title.writtenBy', {names: writers.join(', ')})}</Text>}
      {/* tv-detail frame 6 puts the remote hint under this episode's credits. */}
      <View style={{flexDirection: 'row', alignItems: 'center', gap: 12, marginTop: 26}}>
        <View style={{paddingHorizontal: 10, minWidth: 44, height: 36, borderRadius: 9, borderWidth: 1.5, borderColor: '#edf5fa4d', justifyContent: 'center'}}>
          <Text style={[browseType(theme, 17, 800), {color: theme.colors.textSecondary}]}>{t('next.layout.holdOk')}</Text></View>
        <Text style={[browseType(theme, 20, 600), {color: theme.colors.textTertiary}]}>{t('next.watch.episodeOptions')}</Text>
      </View>
    </View>
  </View>;
}
