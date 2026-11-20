import React, {useRef} from 'react';
import {Text, View} from 'react-native';
import {useFocusWaiting} from '../../kit/focus';
import type {ContentEntry} from '../../../../../packages/client-core/src/library-content';
import type {SearchEntry, SearchGroup} from '../../../../../packages/client-core/src/search';
import {PosterCard, SquareCard, CircleCard, EpisodeTile, type CardSelection} from '../../kit/cards';
import type {NativeShellRuntime} from '../../shell/runtime';
import {Artwork} from '../../kit/artwork';
import {ArtFrame, CardFrame, type CardFrameSurfaceProps} from '../../kit/cards/primitives';
import {SongRow} from '../../kit/browse';
import {SelectionCircle} from '../../kit/selection';
import {Icon} from '../../kit/Icon';
import {browseType} from '../../kit/browse/presentation';
import {useTheme} from '../../kit/theme';
import {cardText, nameInitials} from '../../../../../packages/client-core/src/presentation/kit-model';
import {nativeSearchCardText} from '../home/model';
import {nativeSearchEntryKey} from './model';
import type {NativeSearchActions} from './types';
const media = (entry: SearchEntry): entry is ContentEntry => entry.kind !== 'person' && entry.kind !== 'channel';

export function SearchGridCard({entry, group, runtime, actions, selection, width, disabled, onFocus, onBlur, focusId, testID, index, previousFocused, titleLines, onCaptionLayout}: {onCaptionLayout?: (height: number) => void; titleLines?: 0 | 1 | 2; entry?: SearchEntry; index?: number; previousFocused?: boolean; focusId?: string; testID?: string; group: SearchGroup; runtime: NativeShellRuntime; actions: NativeSearchActions; selection?: CardSelection; width: number; disabled?: boolean; onFocus?: () => void; onBlur?: () => void}) {
  const waiting = useFocusWaiting(), theme = useTheme();
  const menuAnchorRef = useRef<View>(null), content = entry && media(entry) ? entry : undefined;
  if (!entry && !waiting) throw new Error('A search card requires a served entry or an owned waiting slot.');
  const text = content ? theme.isTV && group === 'songs' ? cardText({id: content.id, kind: 'song', title: content.displayTitle ?? content.title, artist: content.artist?.name, album: content.album?.name, durationSeconds: content.duration}, 'song-row', runtime.text) : nativeSearchCardText(content, runtime.text) : undefined;
  const props = {...(theme.isTV ? {titleLines: 2 as const, titleTextStyle: browseType(theme, 20, 600, 0, 26), captionTextStyle: browseType(theme, 18, 400)} : {}), ...(titleLines !== undefined ? {titleLines} : {}), onCaptionLayout, testID: testID ?? (entry ? `search-result-${nativeSearchEntryKey(group, entry)}` : undefined), focusId: focusId ?? (entry ? nativeSearchEntryKey(group, entry) : undefined), disabled, onFocus, onBlur, density: 'grid' as const, primaryAction: 'navigate' as const, title: content?.displayTitle || entry?.title || '', width, caption: text?.caption,
    onNavigate: () => {if (entry) actions.open(entry, group);}, onMore: !entry || actions.canMenu?.(entry, group) === false ? undefined : () => actions.menu(entry, group, menuAnchorRef), selection, menuAnchorRef, moreExpanded: entry ? actions.isMenuOpen?.(entry, group) : undefined};
  if (theme.isTV && group === 'songs' && entry && text) return <View style={{width}}><SongRow row={{id: entry.id, text,
    artwork: <Artwork source={runtime.artwork(entry.posterUrl)} width={56} height={56}/>}} focusId={props.focusId} testID={props.testID} index={index} previousFocused={previousFocused} disabled={disabled} onFocus={onFocus} onBlur={onBlur}
    onPlay={() => {if (!disabled) actions.play(entry, group);}} onMore={props.onMore} selected={selection?.selected} onEnter={() => selection?.onEnter()}
    onSelectionChange={selection?.active ? () => selection.onToggle() : undefined}
    selection={selection?.active ? <SelectionCircle selected={selection.selected} selecting interactive={false} variant="row" accessibilityLabel={entry.title}/> : undefined}/></View>;
  if (theme.isTV && group === 'live-tv') return <ChannelCard {...props} onCaptionLayout={onCaptionLayout} subtitle={entry?.subtitle} artwork={runtime.artwork(entry?.posterUrl)}/>;
  if (group === 'movies' || group === 'shows') return <PosterCard {...props} artwork={runtime.artwork(entry?.posterUrl)}/>;
  if (group === 'episodes') return <EpisodeTile {...props} caption={undefined} label={text?.caption ?? []} layout="tile" artwork={runtime.artwork(content?.stillUrl ?? content?.backdropUrl ?? entry?.posterUrl)}/>;
  if (group === 'people') return <CircleCard {...props} person art={{source: runtime.artwork(entry?.posterUrl), initials: nameInitials(entry?.title ?? '')}}/>;
  if (group === 'artists') return <CircleCard {...props} art={{covers: content?.artworkPaths?.flatMap(path => {const source = runtime.artwork(path); return source ? [source] : [];}) ?? [], alt: entry?.title}}/>;
  return <SquareCard {...props} artwork={runtime.artwork(entry?.posterUrl)}/>;
}


/** Search's channel projection has a name and optional logo, not a programme or
 * an authoritative monogram. Keep the channel shape without inventing either. */
function ChannelCard({subtitle, artwork, onCaptionLayout, ...props}: CardFrameSurfaceProps & {
  subtitle?: string; artwork?: ReturnType<NativeShellRuntime['artwork']>; onCaptionLayout?: (height: number) => void;
}) {
  const theme = useTheme();
  return <CardFrame {...props}>{state => <>
    <ArtFrame width={props.width} height={225} radius={12} focused={state.focused} hovered={state.hovered} holding={state.holding}>
      <View style={{flex: 1, alignItems: 'center', justifyContent: 'center', backgroundColor: theme.colors.raised}}>
        {artwork ? <Artwork source={artwork} width={112} height={112} fit="contain"/> : <Icon name="i-tv" size={64} color={theme.colors.textSecondary}/>}
      </View>
    </ArtFrame>
    <View onLayout={onCaptionLayout && (event => onCaptionLayout(event.nativeEvent.layout.height))} style={{paddingTop: theme.space.tight, gap: theme.space.hairline}}>
      <Text style={[theme.type.rowHeading, {color: theme.colors.text}]}>{props.title}</Text>
      {!!subtitle && <Text style={[theme.type.rowHelp, {color: theme.colors.textTertiary}]}>{subtitle}</Text>}
    </View>
  </>}</CardFrame>;
}
