import React, {useId, useRef, type ReactNode} from 'react';
import {Text, View} from 'react-native';
import Svg, {Defs, LinearGradient, Rect, Stop} from 'react-native-svg';
import {frameSpec} from '@portico/design';
import {Artwork, missingArt, type ArtworkSource} from '../artwork';
import {Button} from '../controls/Button';
import {Chip} from '../controls/Chip';
import {Focusable} from '../controls/Focusable';
import {Icon} from '../Icon';
import {useFrameMetrics} from '../frames/metrics';
import {useTheme} from '../theme';
import type {CardSelection} from '../cards';
import {browseType} from '../browse/presentation';
import {ArtFrame, CardFrame} from '../cards/primitives';
import {SelectionCircle} from '../selection';

export type SearchHistoryItem = Readonly<{query: string; onPress: () => void; remove?: Readonly<{label: string; onPress: () => void}>}>;
export type SearchHistoryProps = Readonly<{
  title: string;
  clear: Readonly<{label: string; onPress: () => void; disabled?: boolean}>;
  items: readonly SearchHistoryItem[];
}>;

/** Recent searches (phone-app 7, desktop-search s-empty): the heading with Clear, then each search. A phone lists them as
 * rows with a clock and a remove ×; wider forms as chips on one line that wraps. The live phone screen (`screens/search/SearchScreen.tsx`) still draws its own rows, which its tests read; Stage 2 moves it onto this part. */
export function SearchHistory({title, clear, items}: SearchHistoryProps) {
  const theme = useTheme(), frame = useFrameMetrics(), phone = frame.form === 'phone';
  if (!items.length) return null;
  const edge = {marginLeft: frame.gutter, marginRight: frame.rightGutter};
  return <View>
    <View style={[edge, {flexDirection: 'row', alignItems: 'center', gap: theme.space.sm, marginBottom: phone ? 0 : frame.headingGap}]}>
      <Text accessibilityRole="header" style={[theme.type.rowHeading, {flex: 1, color: theme.colors.text}]}>{title}</Text>
      <Button variant="text" size="small" disabled={clear.disabled} onPress={clear.onPress}>{clear.label}</Button>
    </View>
    {phone ? items.map(item => <View key={item.query} style={[edge, {flexDirection: 'row', alignItems: 'center', minHeight: theme.targets.touch}]}>
      <Focusable testID={`search-recent-${encodeURIComponent(item.query)}`} accessibilityLabel={item.query} onPress={item.onPress} style={{flex: 1, flexDirection: 'row', alignItems: 'center', gap: theme.space.controlGap, minHeight: theme.targets.touch}}>
        <Icon name="i-clock" size={19} color={theme.colors.textTertiary}/>
        <Text numberOfLines={2} style={[theme.type.rowLabel, {flex: 1, color: theme.colors.textSecondary}]}>{item.query}</Text>
      </Focusable>
      {item.remove && <Button variant="icon" size="small" icon="i-x" iconOnly accessibilityLabel={item.remove.label} onPress={item.remove.onPress}/>}
    </View>)
      : <View style={[edge, {flexDirection: 'row', flexWrap: 'wrap', gap: theme.space.xs}]}>{items.map(item => <Chip key={item.query} icon="i-clock" accessibilityLabel={item.query} onPress={item.onPress}>{item.query}</Chip>)}</View>}
  </View>;
}

export type TopResultProps = Readonly<{
  /** "Top result". */
  eyebrow: string;
  title: string;
  /** The facts: kind, year, seasons. */
  facts?: ReactNode;
  details?: ReactNode;
  synopsis?: string;
  artwork?: ArtworkSource;
  /** The title's own buttons from tablet width (Resume, My List, More); a phone draws one round Play. */
  actions?: ReactNode;
  play?: Readonly<{label: string; title?: string; onPress: () => void}>;
  onPress: () => void;
  testID?: string;
  focusId?: string;
  disabled?: boolean;
  selection?: CardSelection;
  selectionLabel?: string;
  onMenu?: (anchor: React.RefObject<View | null>) => void;
  menuOpen?: boolean;
}>;

/** The Top result (phone-app 8, desktop-search s-top, final3-tv-09): the title's picture with its words over it. A phone's
 * is a card the width of the page with a round Play; wider forms put the words beside the picture with the title's own
 * buttons. The live phone screen's copy (`screens/search/SearchScreen.tsx` `SearchTopResult`) moves onto this in Stage 2. */
export function TopResult({eyebrow, title, facts, details, synopsis, artwork, actions, play, onPress, testID, focusId, disabled, selection, selectionLabel, onMenu, menuOpen}: TopResultProps) {
  const theme = useTheme(), frame = useFrameMetrics(), gradient = useId(), anchor = useRef<View>(null), phone = frame.form === 'phone', tv = frame.form === 'tv';
  const width = frame.width - 2 * frame.cardInset, height = phone ? width * .5 : Math.min(width * .32, frame.form === 'tv' ? 300 : 360);
  const words = <View style={{gap: theme.space.tight}}>
    <Text style={[theme.type.rowHelp, {color: theme.colors.accentStrong, fontFamily: theme.type.listLabel.fontFamily}]}>{eyebrow}</Text>
    <Text accessibilityRole="header" style={[phone ? theme.type.rowHeading : theme.type.pageTitle, {color: theme.colors.text}]}>{title}</Text>
    {facts}
    {!phone && !tv && synopsis !== undefined && <Text numberOfLines={2} style={[theme.type.intro, {color: theme.colors.textSecondary}]}>{synopsis}</Text>}
  </View>;
  // TV (final3-tv-09): beside the keyboard, the 16:9 picture with the words beside it; the title's buttons are its page's.
  if (tv) {
    const art = frameSpec.search.topResultArt, artHeight = art * 9 / 16;
    return <CardFrame focusId={focusId} testID={testID} title={title} disabled={disabled} selection={selection}
      menuAnchorRef={anchor} moreExpanded={menuOpen} onNavigate={onPress} onMore={onMenu && (() => onMenu(anchor))}
      style={{marginTop: 44, transformOrigin: 'left center'}} pressableStyle={{flexDirection: 'row', gap: 36, alignItems: 'center'}}>
      {state => <><ArtFrame width={art} height={artHeight} radius={14} focused={state.focused} hovered={state.hovered} holding={state.holding} selected={selection?.selected}>
        <Artwork source={artwork} width={art} height={artHeight} fallback={artwork ? undefined : missingArt({title, shape: 'wide', width: art, height: artHeight})}/>
      </ArtFrame><View style={{flex: 1}}>
        <Text style={[browseType(theme, 20, 700), {color: theme.colors.accentStrong, marginBottom: 10}]}>{eyebrow}</Text>
        <Text accessibilityRole="header" style={[browseType(theme, 52, 800, -.04, 52), {color: theme.colors.text, marginBottom: 14}]}>{title}</Text>
        {facts}{details}
      </View></>}
    </CardFrame>;
  }
  return <View style={{marginHorizontal: frame.cardInset}}>
    <CardFrame testID={testID} title={title} selection={selection} menuAnchorRef={anchor} moreExpanded={menuOpen}
      onNavigate={onPress} onMore={onMenu && (() => onMenu(anchor))}
      pressableStyle={{minHeight: height, borderRadius: frame.cardRadius, overflow: 'hidden', backgroundColor: theme.colors.raised, justifyContent: 'flex-end'}}>
      {() => <>
      <View style={{position: 'absolute', top: 0, bottom: 0, right: 0, width: phone ? width : width * .62}}><Artwork source={artwork} height={height}/></View>
      <Svg pointerEvents="none" width={width} height={height} style={{position: 'absolute', top: 0, left: 0}}>
        <Defs><LinearGradient id={gradient} x1="0" y1="0" x2="1" y2="0"><Stop offset="0" stopColor={theme.colors.ink} stopOpacity={.94}/><Stop offset={phone ? .5 : .42} stopColor={theme.colors.ink} stopOpacity={.6}/><Stop offset={phone ? .8 : .66} stopColor={theme.colors.ink} stopOpacity={0}/></LinearGradient></Defs>
        <Rect width={width} height={height} fill={`url(#${gradient})`}/>
      </Svg>
      <View style={{marginLeft: frame.cardPadding, marginRight: phone ? frame.cardPadding + theme.targets.touch + theme.space.sm : width * .5, marginTop: frame.cardPadding, marginBottom: frame.cardPadding, gap: frame.headingGap}}>
        {words}
        {!phone && (actions || play && !selection?.active) && <View style={{flexDirection: 'row', flexWrap: 'wrap', gap: theme.space.xs}}>
          {play && !selection?.active && <Button testID="search-top-play" variant="primary" size="small" icon="i-play" accessibilityLabel={play.label} onPress={play.onPress}>
            {play.title}
          </Button>}
          {actions}
        </View>}
      </View>
      {selection?.active && <View style={{position: 'absolute', top: frame.cardPadding, right: frame.cardPadding}}>
        <SelectionCircle selected={selection.selected} selecting interactive={false} variant="row" accessibilityLabel={selectionLabel ?? title}/>
      </View>}
      {phone && play && !selection?.active && <View style={{position: 'absolute', right: frame.cardPadding, bottom: frame.cardPadding}}>
        <Button testID="search-top-play" variant="primary" size="small" icon="i-play" iconOnly accessibilityLabel={play.label} onPress={play.onPress}/>
      </View>}
      </>}
    </CardFrame>
  </View>;
}
