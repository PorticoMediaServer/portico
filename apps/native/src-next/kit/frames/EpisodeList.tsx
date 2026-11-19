import React, {useRef, useState} from 'react';
import {Text, View} from 'react-native';
import {frameSpec} from '@portico/design';
import type {FactPart} from '../../../../../packages/client-core/src/presentation/kit-model';
import {Artwork, type ArtworkSource} from '../artwork';
import {EpisodeTile} from '../cards';
import type {CardSelection} from '../layout/types';
import {ActionRow, Button, type RowAction, type RowPrimary} from '../controls';
import {HeroMetadata} from '../layout/Hero';
import {RoundBarButton} from '../layout/TopBar';
import {alpha} from '../shell/visual';
import {useTheme} from '../theme';
import {useFrameMetrics} from './metrics';
import {EpisodeNeighbour} from './title-parts';
import {StateRow, type StateRowProps} from './parts';

const sizes = frameSpec.hero.title.parts, plateSpec = sizes.plate;

/** One episode row: what a page knows about it, never how big it is. */
export type EpisodeListItem = Readonly<{key: string; title: string; label: readonly FactPart[]; caption?: readonly FactPart[]; synopsis?: string; length?: string; status?: string;
  artwork?: ArtworkSource; backdropFallback?: boolean; watched?: boolean; progress?: number; selection?: CardSelection;
  onNavigate: () => void; onMore?: (anchor: React.RefObject<View | null>) => void; onDownload?: () => void}>;
export type EpisodeNeighbourItem = Readonly<{label: string; title: string; detail?: string; artwork?: ArtworkSource; onPress: () => void}>;
/** One person in a credits row: the name, and the character muted after it ("as Bill"). */
export type EpisodeCreditPerson = Readonly<{name: string; role?: string; onPress?: () => void}>;
/** The episode opened in place of its row (phone-detail d2 frame 4 `.epo`, desktop-detail f5). */
export type EpisodePlateProps = Readonly<{
  still?: ArtworkSource; length?: string; code?: string; title: string; facts: readonly FactPart[]; synopsis?: string;
  primary?: RowPrimary; actions?: readonly RowAction[];
  /** Guest stars, Directed by, Written by: one person per line. */
  credits?: readonly Readonly<{label: string; people: readonly EpisodeCreditPerson[]}>[];
  /** "Show all 17": under a row longer than `plate.guests`. */
  showAll?: (count: number) => string;
  previous?: EpisodeNeighbourItem; next?: EpisodeNeighbourItem;
  /** Folds the plate back into its row (the chevron on its still). */
  close: Readonly<{label: string; onPress: () => void}>;
  /** Its detail still reading, or failed (the row's own words show meanwhile). */
  state?: StateRowProps; testID?: string}>;
export type EpisodeListProps = Readonly<{items: readonly EpisodeListItem[];
  /** The opened episode: drawn in place of its row. */
  opened?: Readonly<{key: string} & EpisodePlateProps>; testID?: string}>;

/** A season's episodes (phone-detail d2, tablet f7): rows at the panel's width with their still, code, length and
 * synopsis; the opened one becomes its plate. The frame spaces the list; a page passes episodes, never sizes. */
export function EpisodeList({items, opened, testID}: EpisodeListProps) {
  const theme = useTheme(), frame = useFrameMetrics(), width = frame.width - frame.gutter - frame.rightGutter;
  const {key: openedKey, ...plate} = opened ?? {key: undefined};
  return <View testID={testID} style={{marginHorizontal: frame.gutter, gap: theme.space.sm}}>
    {items.map(item => opened && openedKey === item.key
      ? <EpisodePlate key={item.key} {...(plate as EpisodePlateProps)}/>
      : <EpisodeRow key={item.key} item={item} width={width}/>)}
  </View>;
}

function EpisodeRow({item, width}: Readonly<{item: EpisodeListItem; width: number}>) {
  const {form} = useFrameMetrics(), anchor = useRef<View>(null), more = item.onMore;
  return <EpisodeTile layout="row" width={width} artWidth={form === 'phone' ? undefined : sizes.episodeArt} title={item.title} label={item.label} caption={item.caption}
    synopsis={item.synopsis} length={item.length} status={item.status} artwork={item.artwork} backdropFallback={item.backdropFallback} watched={item.watched}
    progress={item.progress} selection={item.selection} onNavigate={item.onNavigate} onDownload={item.onDownload}
    menuAnchorRef={anchor} onMore={more ? () => more(anchor) : undefined}/>;
}

export function EpisodePlate({still, length, code, title, facts, synopsis, primary, actions, credits, showAll, previous, next, close, state, testID}: EpisodePlateProps) {
  const theme = useTheme(), frame = useFrameMetrics(), [all, setAll] = useState(false);
  // The plate sits `inset` from the page's edges, wider than the list's gutter; its words sit a little inside its own padding (`.epo` 6).
  const stillWidth = frame.width - 2 * plateSpec.inset - 2 * plateSpec.padding, words = {marginHorizontal: theme.space.tight};
  // Neighbours with a still keep a width each (desktop-detail f5); the phone's small cards share the row (frame 4).
  const pictured = !!(previous?.artwork || next?.artwork);
  return <View testID={testID} style={{marginHorizontal: plateSpec.inset - frame.gutter, padding: plateSpec.padding, paddingBottom: plateSpec.foot, gap: theme.space.sm,
    borderRadius: plateSpec.radius, backgroundColor: alpha(theme.colors.text, plateSpec.fill), borderWidth: 1, borderColor: alpha(theme.colors.text, plateSpec.edge)}}>
    <View style={{width: stillWidth, height: stillWidth * 9 / 16, borderRadius: plateSpec.stillRadius, overflow: 'hidden', backgroundColor: theme.colors.raised}}>
      {still && <Artwork source={still} width={stillWidth} height={stillWidth * 9 / 16}/>}
      {length !== undefined && <View style={{position: 'absolute', top: theme.space.controlGap, right: theme.space.controlGap, paddingHorizontal: theme.space.xs, borderRadius: theme.space.tight, backgroundColor: alpha(theme.colors.ink, plateSpec.badge)}}>
        <Text style={[theme.type.badge, {color: theme.colors.text}]}>{length}</Text></View>}
      <View style={{position: 'absolute', right: theme.space.xs, bottom: theme.space.xs}}><RoundBarButton icon="i-up" label={close.label} onPress={close.onPress}/></View>
    </View>
    <View style={[words, {gap: theme.space.tight}]}>
      {code !== undefined && <Text style={[theme.type.episodeCode, {color: theme.colors.textTertiary}]}>{code}</Text>}
      <Text accessibilityRole="header" style={[theme.type.episodePlateTitle, {color: theme.colors.text}]}>{title}</Text>
      {facts.length > 0 && <HeroMetadata facts={facts}/>}
      {synopsis !== undefined && <Text style={[theme.type.episodeBody, {color: theme.colors.textSecondary}]}>{synopsis}</Text>}
    </View>
    {state && <StateRow {...state}/>}
    {(primary || actions?.length) ? <View style={words}><ActionRow primary={primary} actions={actions}/></View> : null}
    {credits && credits.length > 0 && <View style={[words, {gap: theme.space.tight}]}>{credits.map(row => {
      const shown = all ? row.people : row.people.slice(0, plateSpec.guests);
      return <View key={row.label} style={{flexDirection: 'row', gap: theme.space.controlGap}}>
        <Text style={[theme.type.captionStrong, {width: plateSpec.labelColumn, color: theme.colors.textTertiary}]}>{row.label}</Text>
        <View style={{flex: 1, minWidth: 0}}>
          {shown.map((person, index) => <Text key={index} accessibilityRole={person.onPress ? 'link' : undefined} onPress={person.onPress} style={[theme.type.captionStrong, {color: theme.colors.textSecondary}]}>
            {person.name}{person.role ? <Text style={[theme.type.caption, {color: theme.colors.textTertiary}]}>{'  '}{person.role}</Text> : null}</Text>)}
          {!all && row.people.length > shown.length && showAll && <Button variant="link" size="xs" align="start" onPress={() => setAll(true)}>{showAll(row.people.length)}</Button>}
        </View>
      </View>;
    })}</View>}
    {(previous || next) && <View style={[words, {flexDirection: 'row', flexWrap: 'wrap', gap: theme.space.sm}]}>
      {previous && <View style={{flex: 1, minWidth: pictured ? sizes.neighbourMin : 0}}><EpisodeNeighbour direction="previous" {...previous}/></View>}
      {next && <View style={{flex: 1, minWidth: pictured ? sizes.neighbourMin : 0}}><EpisodeNeighbour direction="next" {...next}/></View>}
    </View>}
  </View>;
}
