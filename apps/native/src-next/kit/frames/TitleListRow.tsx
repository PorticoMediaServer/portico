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
import {MusicWindow} from '../music/MusicWindow';
import {fontFamilies, textLine, useTheme} from '../theme';
import {PanelBlock, PanelHeading} from './title-parts';

const row = frameSpec.hero.title.parts.listRow;
/** The picture's width: a phone's list size (phone-detail d3 frame 8), a tablet's the numbered list's (desktop-detail f8, 106);
 * a square cover (a book) the same width as a tablet's poster. TV keeps the phone's sizes it had. */
export function titleListArt(theme: ReturnType<typeof useTheme>, square?: boolean): number {
  const tablet = theme.form === 'medium' || theme.form === 'expanded';
  return tablet ? frameSpec.hero.title.parts.numberedArt[theme.form] : square ? row.square : row.poster;
}

/** A row's height, the stride a list of them is windowed at: the taller of its poster and its words with the title on two
 * lines (a long title wraps; it is never cut), the facts line and the Watched / time-left line, and room above and below. */
export function titleListRowHeight(theme: ReturnType<typeof useTheme>, kicker = false): number {
  const help = textLine(theme.type.rowHelp);
  // A row with a kicker (a book's place in its series) has one more line, and one more gap, over its title.
  const words = (kicker ? help + theme.space.hairline * 2 : 0) + 2 * textLine(theme.type.episodeTitle) + help + theme.space.tight + Math.max(row.check, help) + 2 * theme.space.hairline * 2;
  return Math.max(titleListArt(theme) * 1.5, words) + 2 * theme.space.tight;
}

/** One title in a phone's list of titles: what the page knows about it, never how big it is. */
export type TitleListItem = Readonly<{title: string;
  /** A small accent line over the title (a book's place in its series, collections 41c "Book 1"). */
  kicker?: string;
  /** Its year and length, drawn apart ("1995  1 h 21 min"), or who read a book. */
  facts: readonly string[];
  /** Strong accent words at the end of the facts line (a book's time left, collections 41b "4 h 34 min left"). */
  note?: string;
  /** A square cover (a book) in place of a poster. */
  square?: boolean;
  watched?: boolean;
  /** The word beside the check when it is done: films "Watched" (the default), books "Finished". */
  doneLabel?: string;
  /** How far in (0 to 1), drawn as a short bar with `remaining` ("1 h 2 min left"). */
  progress?: number; remaining?: string;
  artwork?: ArtworkSource; onNavigate: () => void;
  /** Opens the title's menu from the row's own place. */
  onMore?: (anchor: React.RefObject<View | null>) => void;
  selection?: CardSelection; testID?: string}>;

/** A title in a list (phone-detail d3 frame 8, a collection's films; larger on a tablet, desktop-detail f8): its poster, title, year and length, then
 * Watched or how far in, and its ⋯ menu. Moved from the old collection screen's row (`CollectionRow`). */
/** `kickerRoom`: the row is as tall as one with a kicker, so a list whose rows may carry one keeps one stride. */
export function TitleListRow({item, kickerRoom = !!item.kicker}: Readonly<{item: TitleListItem; kickerRoom?: boolean}>) {
  // The card's own anchor (a long press) and the ⋯'s: one ref on both left the ⋯'s menu at the row's left edge.
  const theme = useTheme(), anchor = useRef<View>(null), dots = useRef<View>(null), more = item.onMore, t = defaultI18n.t;
  // The card fills the row's own height (CardFrame grows into its parent).
  return <View style={{height: titleListRowHeight(theme, kickerRoom)}}><CardFrame testID={item.testID} title={item.title} menuAnchorRef={anchor} onNavigate={item.onNavigate} onMore={more ? () => more(anchor) : undefined}
    // CardFrame's own wrapper takes `style`: it fills the row, so the card (which grows into it) has the row's height.
    selection={item.selection} style={{flex: 1}} pressableStyle={{flex: 1, flexDirection: 'row', alignItems: 'center', gap: row.gap}}>
    {() => <>
      {item.selection?.active && <SelectionCircle selected={item.selection.selected} selecting interactive={false} variant="row" size={theme.space.md} accessibilityLabel={t('card.select', {title: item.title})}/>}
      <Artwork source={item.artwork} width={titleListArt(theme, item.square)} height={item.square ? titleListArt(theme, true) : titleListArt(theme) * 1.5} style={{borderRadius: theme.radii.artwork}}/>
      <View style={{flex: 1, minWidth: 0, gap: theme.space.hairline * 2}}>
        {item.kicker && <Text style={[theme.type.rowHelp, {fontFamily: fontFamilies[700], color: theme.colors.accentStrong}]}>{item.kicker}</Text>}
        <Text style={[theme.type.episodeTitle, {color: theme.colors.text}]}>{item.title}</Text>
        {(item.facts.length > 0 || item.note) && <View style={{flexDirection: 'row', flexWrap: 'wrap', columnGap: theme.space.controlGap}}>
          {item.facts.map(fact => <Text key={fact} style={[theme.type.rowHelp, {color: theme.colors.textTertiary}]}>{fact}</Text>)}
          {item.note && <Text style={[theme.type.rowHelp, {fontFamily: fontFamilies[700], color: theme.colors.accentStrong}]}>{item.note}</Text>}</View>}
        {(item.watched || item.progress !== undefined) && <View style={{flexDirection: 'row', alignItems: 'center', gap: theme.space.tight, marginTop: theme.space.tight}}>
          {item.watched ? <Icon name="i-check" size={row.check} color={theme.colors.accentStrong}/>
            : <View style={{width: row.bar, height: theme.spec.card.progress.height[theme.column], borderRadius: theme.space.hairline * 2, backgroundColor: theme.colors.raisedBright}}>
              <View style={{width: `${Math.min(1, item.progress ?? 0) * 100}%`, height: '100%', borderRadius: theme.space.hairline * 2, backgroundColor: theme.colors.accentStrong}}/></View>}
          <Text style={[theme.type.rowHelp, {fontFamily: fontFamilies[700], color: theme.colors.textSecondary}]}>{item.watched ? item.doneLabel ?? t('saved.watched') : item.remaining}</Text>
        </View>}
      </View>
      {more && <Focusable ref={dots} accessibilityLabel={t('card.more', {title: item.title})} onPress={() => more(dots)} style={{width: row.more, height: row.more, alignItems: 'center', justifyContent: 'center'}}>
        <Icon name="i-more" size={row.moreIcon} color={theme.colors.textTertiary}/></Focusable>}
    </>}
  </CardFrame></View>;
}

/** A page's list of titles inside a music scroller (`musicScroller`: an author's or a series' books, collections 41b/41c):
 * its `TitleListRow`s, only the ones in view mounted, a hairline between them from the words on, and a heading drawn
 * with them as one section. A list read as a `WindowedCollection` takes `titleRowsScroller` instead. */
export function TitleRowWindow({heading, rows, testID}: Readonly<{heading?: Readonly<{title: string; trailing?: string}>;
  rows: readonly (TitleListItem & Readonly<{id: string}>)[]; testID?: string}>) {
  // One stride for the list: room for a kicker line on every row when any row carries one (a series' books).
  const theme = useTheme(), kicker = rows.some(item => !!item.kicker);
  return <View testID={testID}>
    {heading && <PanelBlock><PanelHeading title={heading.title} trailing={heading.trailing}/></PanelBlock>}
    <PanelBlock><MusicWindow items={rows} itemHeight={titleListRowHeight(theme, kicker)} renderItem={(item, index) => <View>
      {index > 0 && <View pointerEvents="none" style={{position: 'absolute', top: 0, right: 0, left: titleListArt(theme, item.square) + row.gap, height: theme.space.hairline, backgroundColor: theme.colors.line}}/>}
      <TitleListRow item={item} kickerRoom={kicker}/></View>}/></PanelBlock>
  </View>;
}
