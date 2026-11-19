import React, {useRef} from 'react';
import {Text, View} from 'react-native';
import {frameSpec} from '@portico/design';
import type {FactPart} from '../../../../../packages/client-core/src/presentation/kit-model';
import type {ArtworkSource} from '../artwork';
import {EpisodeTile} from '../cards';
import type {CardSelection} from '../layout/types';
import {ActionRow, Button, type RowAction, type RowPrimary} from '../controls';
import {HeroMetadata} from '../layout/Hero';
import {useTheme} from '../theme';
import {useFrameMetrics} from './metrics';
import {AboutTable, EpisodeNeighbour, type AboutValue} from './title-parts';
import {StateRow, type StateRowProps} from './parts';

const sizes = frameSpec.hero.title.parts;

/** One episode row: what a page knows about it, never how big it is. */
export type EpisodeListItem = Readonly<{key: string; title: string; label: readonly FactPart[]; caption?: readonly FactPart[]; synopsis?: string; length?: string; status?: string;
  artwork?: ArtworkSource; backdropFallback?: boolean; watched?: boolean; progress?: number; selection?: CardSelection;
  onNavigate: () => void; onMore?: (anchor: React.RefObject<View | null>) => void; onDownload?: () => void}>;
export type EpisodeNeighbourItem = Readonly<{label: string; title: string; detail?: string; artwork?: ArtworkSource; onPress: () => void}>;
/** The episode opened in place under its row (phone-detail d2 frame 4, desktop-detail f5). */
export type EpisodePlateProps = Readonly<{code?: string; title: string; facts: readonly FactPart[]; synopsis?: string;
  primary?: RowPrimary; actions?: readonly RowAction[];
  /** Guest stars, Directed by, Written by: label and values, as the About table draws them. */
  about?: readonly Readonly<{label: string; values: readonly AboutValue[]}>[];
  previous?: EpisodeNeighbourItem; next?: EpisodeNeighbourItem;
  close?: Readonly<{label: string; onPress: () => void}>;
  /** Its detail still reading, or failed (the row's own words show meanwhile). */
  state?: StateRowProps; testID?: string}>;
export type EpisodeListProps = Readonly<{items: readonly EpisodeListItem[];
  /** The opened episode: its plate under its row. */
  opened?: Readonly<{key: string} & EpisodePlateProps>; testID?: string}>;

/** A season's episodes (phone-detail d2, tablet f7): rows at the panel's width with their still, code, length and
 * synopsis; the opened one has its plate under it. The frame spaces the list; a page passes episodes, never sizes. */
export function EpisodeList({items, opened, testID}: EpisodeListProps) {
  const theme = useTheme(), frame = useFrameMetrics(), width = frame.width - frame.gutter - frame.rightGutter;
  const {key: openedKey, ...plate} = opened ?? {key: undefined};
  return <View testID={testID} style={{marginHorizontal: frame.gutter, gap: theme.space.sm}}>
    {items.map(item => <View key={item.key} style={{gap: theme.space.sm}}>
      <EpisodeRow item={item} width={width}/>
      {opened && openedKey === item.key && <EpisodePlate {...(plate as EpisodePlateProps)}/>}
    </View>)}
  </View>;
}

function EpisodeRow({item, width}: Readonly<{item: EpisodeListItem; width: number}>) {
  const {form} = useFrameMetrics(), anchor = useRef<View>(null), more = item.onMore;
  return <EpisodeTile layout="row" width={width} artWidth={form === 'phone' ? undefined : sizes.episodeArt} title={item.title} label={item.label} caption={item.caption}
    synopsis={item.synopsis} length={item.length} status={item.status} artwork={item.artwork} backdropFallback={item.backdropFallback} watched={item.watched}
    progress={item.progress} selection={item.selection} onNavigate={item.onNavigate} onDownload={item.onDownload}
    menuAnchorRef={anchor} onMore={more ? () => more(anchor) : undefined}/>;
}

export function EpisodePlate({code, title, facts, synopsis, primary, actions, about, previous, next, close, state, testID}: EpisodePlateProps) {
  const theme = useTheme(), frame = useFrameMetrics();
  return <View testID={testID} style={{gap: theme.space.sm, padding: frame.cardPadding, borderRadius: frame.cardRadius, backgroundColor: theme.colors.raised}}>
    <View style={{flexDirection: 'row', alignItems: 'flex-start', gap: theme.space.sm}}>
      <View style={{flex: 1, minWidth: 0, gap: theme.space.tight}}>
        {code !== undefined && <Text style={[theme.type.rowHelp, {color: theme.colors.textSecondary}]}>{code}</Text>}
        <Text accessibilityRole="header" style={[theme.type.rowHeading, {color: theme.colors.text}]}>{title}</Text>
      </View>
      {close && <Button variant="glass" size="small" iconOnly icon="i-x" accessibilityLabel={close.label} onPress={close.onPress}/>}
    </View>
    {facts.length > 0 && <HeroMetadata facts={facts}/>}
    {synopsis !== undefined && <Text style={[theme.type.intro, {color: theme.colors.textSecondary}]}>{synopsis}</Text>}
    {state && <StateRow {...state}/>}
    {(primary || actions?.length) && <ActionRow primary={primary} actions={actions}/>}
    {about && about.length > 0 && <AboutTable rows={about}/>}
    {(previous || next) && <View style={{flexDirection: 'row', flexWrap: 'wrap', gap: theme.space.sm}}>
      {previous && <View style={{flex: 1, minWidth: sizes.neighbourMin}}><EpisodeNeighbour direction="previous" {...previous}/></View>}
      {next && <View style={{flex: 1, minWidth: sizes.neighbourMin}}><EpisodeNeighbour direction="next" {...next}/></View>}
    </View>}
  </View>;
}
