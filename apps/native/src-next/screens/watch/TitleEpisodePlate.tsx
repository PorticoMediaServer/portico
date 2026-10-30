import React, {useRef} from 'react';
import {Text, View} from 'react-native';
import type {ContentEntry} from '../../../../../packages/client-core/src/library-content';
import type {FactPart} from '../../../../../packages/client-core/src/presentation/kit-model';
import {durationText} from '../../../../../packages/client-core/src/presentation/kit-model/text';
import type {PersonalIntent} from '../../../../../packages/client-core/src/detail';
import {defaultI18n} from '../../../../../packages/i18n/src';
import {Artwork, type ArtworkSource} from '../../kit/artwork';
import {Button} from '../../kit/controls';
import {nativeType, useTheme} from '../../kit/theme';
import type {NativeTitleEpisodeSnapshot} from './title-episode';

export type NativeTitleEpisodeNeighbour = Readonly<{id: string; number: number; title: string}>;

export type NativeTitleEpisodePlateProps = Readonly<{
  entry: ContentEntry;
  code?: FactPart;
  /** The row width inside the drawn page inset. */
  width: number;
  still?: ArtworkSource;
  backdrop?: ArtworkSource;
  snapshot: NativeTitleEpisodeSnapshot;
  more: (entry: ContentEntry, anchor: React.RefObject<View | null>) => void;
  download?: (entry: ContentEntry) => void;
  onClose: () => void;
  onPlay: () => void;
  onStartOver: () => void;
  onPersonal: (intent: PersonalIntent) => void;
  onNeighbour: (neighbour: NativeTitleEpisodeNeighbour) => void;
  onRetry: () => void;
  renderError: (error: unknown, retry: () => void) => React.ReactNode;
  locale: string;
}>;

const departments = (credits: readonly Readonly<{name: string; department: string; role: string}>[], names: readonly string[]) =>
  [...new Set(credits.filter(credit => names.includes(credit.department.toLowerCase())).map(credit => credit.name))];

/** phone-detail frame 4: the episode opened in place under its own row. It is
 * the show page's flow, never a sheet or a layer (Justin, 18 Oct 2026). */
export function NativeTitleEpisodePlate(props: NativeTitleEpisodePlateProps) {
  const {entry, code, snapshot, width} = props, theme = useTheme(), t = defaultI18n.t, more = useRef<View>(null);
  const data = snapshot.data, personal = data?.personal;
  const credits = data?.metadata.credits ?? [];
  const guestStars = credits.filter(credit => ['acting', 'cast'].includes(credit.department.toLowerCase()));
  const directors = departments(credits, ['directing', 'director', 'direction']);
  const writers = departments(credits, ['writing', 'writer', 'writers']);
  const progress = personal?.progressSeconds ?? entry.progressSeconds ?? 0;
  const resuming = progress > 0 && !(personal?.watched ?? entry.watched);
  const playable = data?.actions.some(action => action.enabled && action.playback && (action.id === 'resume' || action.id === 'play'));
  const canWatchlist = data?.actions.some(action => action.id === 'watchlist' && action.enabled);
  const overview = data?.item.overview ?? entry.overview;
  const duration = data?.item.duration ?? entry.duration;
  const length = duration !== undefined ? durationText(duration, t) : undefined;
  const rating = data?.facts?.contentRating ?? entry.contentRating;
  const airDate = entry.airDate ?? data?.item.episode?.airDate;
  const meta = [
    airDate ? new Intl.DateTimeFormat(props.locale, {month: 'short', day: 'numeric', year: 'numeric', timeZone: 'UTC'}).format(new Date(`${airDate}T00:00:00Z`)) : undefined,
    length,
  ].filter(Boolean);
  const label = {...nativeType({size: 13, weight: 600, tracking: 0, lineHeight: 'normal'}, theme.textScale), color: theme.colors.textTertiary, width: 86};
  const value = {...nativeType({size: 13, weight: 600, tracking: 0, lineHeight: 'normal'}, theme.textScale), color: theme.colors.textSecondary, flex: 1};
  // tablet7: the expanded page draws this panel in two columns — the picture with
  // Previous and Next beneath it, then the episode's own words and buttons. The
  // phone keeps its single column, and both share every real value above.
  const row = width >= 720;
  const pictureWidth = row ? Math.round(width * 0.33) : width;
  const documents = data?.episodeDetails && (data.episodeDetails.previous || data.episodeDetails.next) ? <View style={{flexDirection: 'row', gap: 8, marginHorizontal: row ? 0 : 6, marginTop: row ? 12 : 14}}>
    {data.episodeDetails.previous ? <NeighbourButton direction="previous" neighbour={data.episodeDetails.previous} onPress={props.onNeighbour}/> : <View style={{flex: 1}}/>}
    {data.episodeDetails.next ? <NeighbourButton direction="next" neighbour={data.episodeDetails.next} onPress={props.onNeighbour}/> : <View style={{flex: 1}}/>}
  </View> : null;
  const picture = <View style={{borderRadius: 12, overflow: 'hidden'}}>
    <Artwork source={props.still ?? props.backdrop} width={pictureWidth} height={pictureWidth * 9 / 16} brightness={0.62} saturation={0.6}
      style={!row && !props.still && props.backdrop ? {transform: [{scale: 2.2}]} : undefined}/>
    {!row && length && <Text style={[nativeType({size: 12, weight: 700, tracking: 0, lineHeight: 'normal'}, theme.textScale),
      {position: 'absolute', right: 10, top: 10, paddingVertical: 3, paddingHorizontal: 8, borderRadius: 6, color: theme.colors.text, backgroundColor: 'rgba(7,16,24,.65)'}]}>{length}</Text>}
    {!row && <Button testID={`watch-episode-close-${entry.id}`} variant="glass" icon="i-up" iconOnly accessibilityLabel={t('action.close')} onPress={props.onClose}
      style={{position: 'absolute', right: 8, bottom: 8, width: 34, height: 34, borderRadius: 17}}/>}
  </View>;
  const metaParts = row && code ? [code.text, ...meta] : meta;
  const body = <>
    {!row && code ? <View style={{flexDirection: 'row', gap: 10, marginTop: 12, marginHorizontal: 6}}><Text style={[nativeType({size: 13, weight: 700, tracking: 0, lineHeight: 'normal'}, theme.textScale), {color: theme.colors.textTertiary}]}>{code.text}</Text></View> : null}
    <Text accessibilityRole="header" numberOfLines={2} style={[nativeType({size: row ? 32 : 22, weight: 800, tracking: -.025, lineHeight: row ? 36 : 25.3}, theme.textScale), {marginHorizontal: 6, marginTop: row ? 0 : 2, color: theme.colors.text}]}>{entry.displayTitle ?? entry.title}</Text>
    {(metaParts.length > 0 || rating) && <View style={{flexDirection: 'row', flexWrap: 'wrap', alignItems: 'center', gap: 12, marginHorizontal: 6, marginTop: 6}}>
      {metaParts.map((part, index) => <Text key={`${index}:${part}`} style={[nativeType({size: row ? 14 : 13, weight: 600, tracking: 0, lineHeight: 'normal'}, theme.textScale), {color: theme.colors.textSecondary}]}>{part}</Text>)}
      {rating ? <Text style={[nativeType({size: row ? 12 : 11.5, weight: 600, tracking: 0, lineHeight: 'normal'}, theme.textScale), {color: theme.colors.textSecondary, borderWidth: 1, borderColor: theme.colors.lineStrong, borderRadius: 4, paddingHorizontal: 7, paddingVertical: 1}]}>{rating}</Text> : null}
    </View>}
    {overview ? <Text style={[nativeType({size: row ? 15 : 13.5, weight: 400, tracking: 0, lineHeight: row ? 22.5 : 20.25}, theme.textScale), {marginHorizontal: 6, marginTop: 10, color: theme.colors.textSecondary}]}>{overview}</Text> : null}
    {snapshot.phase === 'error' && snapshot.error ? <View style={{marginHorizontal: 6, marginTop: 10}}>{props.renderError(snapshot.error, props.onRetry)}</View> : null}
    <View style={{flexDirection: 'row', alignItems: 'center', gap: 10, marginHorizontal: 6, marginTop: 14}}>
      {playable && <Button testID={`watch-episode-play-${entry.id}`} style={row ? undefined : {flex: 1}} icon="i-play" onPress={props.onPlay}>{t(resuming ? 'entry.resume' : 'entry.play')}</Button>}
      {canWatchlist && <Button variant="glass" style={!row ? {borderRadius: 10} : undefined} iconOnly={!row} icon={personal?.watchlisted ? 'i-check' : 'i-plus'} accessibilityState={{selected: personal?.watchlisted}}
        accessibilityLabel={t(personal?.watchlisted ? 'entry.removeFromWatchlist' : 'entry.addToWatchlist')} onPress={() => props.onPersonal({action: 'watchlist', value: !personal?.watchlisted})}>{t('next.controls.myList')}</Button>}
      {props.download && <Button variant="glass" style={!row ? {borderRadius: 10} : undefined} iconOnly={!row} icon="i-dl" accessibilityLabel={t('next.cards.download')} onPress={() => props.download!(entry)}>{t('next.cards.download')}</Button>}
      <Button ref={more} variant="glass" style={{borderRadius: 10}} iconOnly icon="i-more" accessibilityLabel={t('card.more', {title: entry.displayTitle ?? entry.title})} onPress={() => props.more(entry, more)}/>
    </View>
    {resuming && data?.actions.some(action => action.id === 'start_over' && action.enabled) && <Button variant="text" size="small" icon="i-replay" onPress={props.onStartOver} style={{alignSelf: 'flex-start', marginHorizontal: 6, marginTop: 6}}>{t('action.startOver')}</Button>}
    {(guestStars.length > 0 || directors.length > 0 || writers.length > 0) && <View style={{marginHorizontal: 6, marginTop: 14, gap: row ? 8 : 4}}>
      {guestStars.length > 0 && <View style={{flexDirection: 'row', gap: 10}}>
        <Text style={label}>{t('next.watch.guestStars')}</Text>
        <View style={{flex: 1, gap: 4}}>{guestStars.map(credit => <Text key={credit.id} style={value}>{credit.name}{credit.role ? <Text style={[nativeType({size: 13, weight: 500, tracking: 0, lineHeight: 'normal'}, theme.textScale), {color: theme.colors.textTertiary}]}>{'  '}{t('next.watch.asCharacter', {character: credit.role})}</Text> : null}</Text>)}</View>
      </View>}
      {directors.length > 0 && <Text style={value}>{t('title.directedBy', {names: directors.join(', ')})}</Text>}
      {writers.length > 0 && <Text style={value}>{t('title.writtenBy', {names: writers.join(', ')})}</Text>}
    </View>}
    {!row && documents}
  </>;
  return <View testID={`watch-episode-open-${entry.id}`} style={{marginHorizontal: -8, marginBottom: 16}}>
    <View style={{backgroundColor: '#edf5fa0b', borderRadius: 18, borderWidth: 1, borderColor: '#edf5fa0f', paddingHorizontal: 8, paddingTop: 8, paddingBottom: 14, gap: 2}}>
      {row ? <View style={{flexDirection: 'row', gap: 22, alignItems: 'flex-start'}}>
        <View style={{width: pictureWidth}}>{picture}{documents}</View>
        <View style={{flex: 1, minWidth: 0, paddingRight: 40}}>{body}</View>
        <Button testID={`watch-episode-close-${entry.id}`} variant="glass" icon="i-x" iconOnly accessibilityLabel={t('action.close')} onPress={props.onClose}
          style={{position: 'absolute', right: 8, top: 8, width: 40, height: 40, borderRadius: 20}}/>
      </View> : <>{picture}{body}</>}
    </View>
  </View>;
}

function NeighbourButton({direction, neighbour, onPress}: {direction: 'previous' | 'next'; neighbour: NativeTitleEpisodeNeighbour; onPress: (neighbour: NativeTitleEpisodeNeighbour) => void}) {
  const theme = useTheme(), t = defaultI18n.t, next = direction === 'next';
  return <Button testID={`watch-episode-${direction}-${neighbour.id}`} variant="glass" onPress={() => onPress(neighbour)}
    icon={next ? 'i-right' : 'i-left'} style={{flex: 1, height: 50, flexDirection: next ? 'row-reverse' : 'row', justifyContent: 'space-between', paddingHorizontal: 10}}>
    <View style={{flex: 1, minWidth: 0, alignItems: next ? 'flex-end' : 'flex-start'}}>
      <Text style={[nativeType({size: 11.5, weight: 700, tracking: 0, lineHeight: 'normal'}, theme.textScale), {color: theme.colors.textTertiary}]}>{t(next ? 'title.nextEpisode' : 'title.previousEpisode')}</Text>
      <Text numberOfLines={1} style={[nativeType({size: 13.5, weight: 700, tracking: 0, lineHeight: 'normal'}, theme.textScale), {color: theme.colors.text}]}>{neighbour.title}</Text>
    </View>
  </Button>;
}
