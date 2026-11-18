import React, {useId, type ReactNode} from 'react';
import {Text, View} from 'react-native';
import Svg, {Defs, LinearGradient, Rect, Stop} from 'react-native-svg';
import {Artwork, type ArtworkSource} from '../artwork';
import {Button} from '../controls/Button';
import {Chip} from '../controls/Chip';
import {Focusable} from '../controls/Focusable';
import {Icon} from '../Icon';
import {useFrameMetrics} from '../frames/metrics';
import {useTheme} from '../theme';

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
  synopsis?: string;
  artwork?: ArtworkSource;
  /** The title's own buttons from tablet width (Resume, My List, More); a phone draws one round Play. */
  actions?: ReactNode;
  play?: Readonly<{label: string; onPress: () => void}>;
  onPress: () => void;
}>;

/** The Top result (phone-app 8, desktop-search s-top, final3-tv-09): the title's picture with its words over it. A phone's
 * is a card the width of the page with a round Play; wider forms put the words beside the picture with the title's own
 * buttons. The live phone screen's copy (`screens/search/SearchScreen.tsx` `SearchTopResult`) moves onto this in Stage 2. */
export function TopResult({eyebrow, title, facts, synopsis, artwork, actions, play, onPress}: TopResultProps) {
  const theme = useTheme(), frame = useFrameMetrics(), gradient = useId(), phone = frame.form === 'phone';
  const width = frame.width - 2 * frame.cardInset, height = phone ? width * .5 : Math.min(width * .32, frame.form === 'tv' ? 300 : 360);
  const words = <View style={{gap: theme.space.tight}}>
    <Text style={[theme.type.rowHelp, {color: theme.colors.accentStrong, fontFamily: theme.type.listLabel.fontFamily}]}>{eyebrow}</Text>
    <Text accessibilityRole="header" style={[phone ? theme.type.rowHeading : theme.type.pageTitle, {color: theme.colors.text}]}>{title}</Text>
    {facts}
    {!phone && synopsis !== undefined && <Text numberOfLines={2} style={[theme.type.intro, {color: theme.colors.textSecondary}]}>{synopsis}</Text>}
  </View>;
  return <View style={{marginHorizontal: frame.cardInset}}>
    <Focusable accessibilityLabel={title} onPress={onPress} focusRadius={frame.cardRadius} style={{height, borderRadius: frame.cardRadius, overflow: 'hidden', backgroundColor: theme.colors.raised}}>
      <View style={{position: 'absolute', top: 0, bottom: 0, right: 0, width: phone ? width : width * .62}}><Artwork source={artwork} height={height}/></View>
      <Svg pointerEvents="none" width={width} height={height} style={{position: 'absolute', top: 0, left: 0}}>
        <Defs><LinearGradient id={gradient} x1="0" y1="0" x2="1" y2="0"><Stop offset="0" stopColor={theme.colors.ink} stopOpacity={.94}/><Stop offset={phone ? .5 : .42} stopColor={theme.colors.ink} stopOpacity={.6}/><Stop offset={phone ? .8 : .66} stopColor={theme.colors.ink} stopOpacity={0}/></LinearGradient></Defs>
        <Rect width={width} height={height} fill={`url(#${gradient})`}/>
      </Svg>
      <View style={{position: 'absolute', left: frame.cardPadding, right: phone ? frame.cardPadding + theme.targets.touch + theme.space.sm : width * .5, bottom: frame.cardPadding, gap: frame.headingGap}}>
        {words}
        {!phone && actions && <View style={{flexDirection: 'row', flexWrap: 'wrap', gap: theme.space.xs}}>{actions}</View>}
      </View>
      {phone && play && <View style={{position: 'absolute', right: frame.cardPadding, bottom: frame.cardPadding}}><Button variant="primary" size="small" icon="i-play" iconOnly accessibilityLabel={play.label} onPress={play.onPress}/></View>}
    </Focusable>
  </View>;
}
