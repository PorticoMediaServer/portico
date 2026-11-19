import React, {useRef} from 'react';
import {Text, View} from 'react-native';
import {frameSpec} from '@portico/design';
import {defaultI18n} from '../../../../../packages/i18n/src';
import {Artwork, type ArtworkSource} from '../artwork';
import {CardFrame} from '../cards/primitives';
import type {CardSelection} from '../layout/types';
import {Focusable} from '../controls';
import {Icon} from '../Icon';
import {SelectionCircle} from '../selection';
import {fontFamilies, useTheme} from '../theme';

const row = frameSpec.hero.title.parts.listRow;

/** A row's height: its poster and the room above and below it. A list of them is windowed at this stride. */
export function titleListRowHeight(theme: ReturnType<typeof useTheme>): number {
  return row.poster * 1.5 + 2 * theme.space.tight;
}

/** One title in a phone's list of titles: what the page knows about it, never how big it is. */
export type TitleListItem = Readonly<{title: string;
  /** Its year and length, drawn apart ("1995  1 h 21 min"). */
  facts: readonly string[];
  watched?: boolean;
  /** How far in (0 to 1), drawn as a short bar with `remaining` ("1 h 2 min left"). */
  progress?: number; remaining?: string;
  artwork?: ArtworkSource; onNavigate: () => void;
  /** Opens the title's menu from the row's own place. */
  onMore?: (anchor: React.RefObject<View | null>) => void;
  selection?: CardSelection; testID?: string}>;

/** A title in a phone's list (phone-detail d3 frame 8, a collection's films): its poster, title, year and length, then
 * Watched or how far in, and its ⋯ menu. Moved from the old collection screen's row (`CollectionRow`). */
export function TitleListRow({item}: Readonly<{item: TitleListItem}>) {
  const theme = useTheme(), anchor = useRef<View>(null), more = item.onMore, t = defaultI18n.t;
  // The card fills the row's own height (CardFrame grows into its parent).
  return <View style={{height: titleListRowHeight(theme)}}><CardFrame testID={item.testID} title={item.title} menuAnchorRef={anchor} onNavigate={item.onNavigate} onMore={more ? () => more(anchor) : undefined}
    // CardFrame's own wrapper takes `style`: it fills the row, so the card (which grows into it) has the row's height.
    selection={item.selection} style={{flex: 1}} pressableStyle={{flex: 1, flexDirection: 'row', alignItems: 'center', gap: row.gap}}>
    {() => <>
      {item.selection?.active && <SelectionCircle selected={item.selection.selected} selecting interactive={false} variant="row" size={theme.space.md} accessibilityLabel={t('card.select', {title: item.title})}/>}
      <Artwork source={item.artwork} width={row.poster} height={row.poster * 1.5} style={{borderRadius: theme.radii.artwork}}/>
      <View style={{flex: 1, minWidth: 0, gap: theme.space.hairline * 2}}>
        <Text style={[theme.type.episodeTitle, {color: theme.colors.text}]}>{item.title}</Text>
        {item.facts.length > 0 && <View style={{flexDirection: 'row', flexWrap: 'wrap', columnGap: theme.space.controlGap}}>
          {item.facts.map(fact => <Text key={fact} style={[theme.type.rowHelp, {color: theme.colors.textTertiary}]}>{fact}</Text>)}</View>}
        {(item.watched || item.progress !== undefined) && <View style={{flexDirection: 'row', alignItems: 'center', gap: theme.space.tight, marginTop: theme.space.tight}}>
          {item.watched ? <Icon name="i-check" size={row.check} color={theme.colors.accentStrong}/>
            : <View style={{width: row.bar, height: theme.spec.card.progress.height[theme.column], borderRadius: theme.space.hairline * 2, backgroundColor: theme.colors.raisedBright}}>
              <View style={{width: `${Math.min(1, item.progress ?? 0) * 100}%`, height: '100%', borderRadius: theme.space.hairline * 2, backgroundColor: theme.colors.accentStrong}}/></View>}
          <Text style={[theme.type.rowHelp, {fontFamily: fontFamilies[700], color: theme.colors.textSecondary}]}>{item.watched ? t('saved.watched') : item.remaining}</Text>
        </View>}
      </View>
      {more && <Focusable accessibilityLabel={t('card.more', {title: item.title})} onPress={() => more(anchor)} style={{width: row.more, height: row.more, alignItems: 'center', justifyContent: 'center'}}>
        <Icon name="i-more" size={row.moreIcon} color={theme.colors.textTertiary}/></Focusable>}
    </>}
  </CardFrame></View>;
}
