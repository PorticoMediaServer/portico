import React, {useRef} from 'react';
import {Text, View} from 'react-native';
import {frameSpec, typeScale} from '@portico/design';
import {defaultI18n} from '../../../../../packages/i18n/src';
import {Artwork, missingArt, type ArtworkSource} from '../artwork';
import {CardFrame, Progress} from '../cards/primitives';
import type {CardSelection} from '../layout/types';
import {Focusable} from '../controls';
import {Icon} from '../Icon';
import {SelectionCircle} from '../selection';
import {MusicWindow} from '../music/MusicWindow';
import {fontFamilies, nativeType, textLine, useTheme} from '../theme';
import {PanelBlock, PanelHeading} from './title-parts';
import {isTabletForm} from './metrics';

const row = frameSpec.hero.title.parts.listRow, tvRow = frameSpec.hero.title.parts.listRowTV;
const tvListTitle = (theme: ReturnType<typeof useTheme>) => nativeType({...typeScale.tenFoot.episodeTitle, size: tvRow.title}, theme.textScale);
/** The picture's width: a phone's list size (phone-detail d3 frame 8), a tablet's the numbered list's (desktop-detail f8, 106);
 * a square cover (a book) the same width as a tablet's poster. TV book rows use their own art size. */
export function titleListArt(theme: ReturnType<typeof useTheme>, square?: boolean): number {
  const tablet = isTabletForm(theme.form);
  return tablet || theme.isTV && square ? frameSpec.hero.title.parts.numberedArt[theme.form] : square ? row.square : row.poster;
}

/** A row's height, the stride a list of them is windowed at: the taller of its poster and its words with the title on two
 * lines (a long title wraps; it is never cut), the facts line and the Watched / time-left line, and room above and below. */
export function titleListRowHeight(theme: ReturnType<typeof useTheme>, kicker = false, tvList = false): number {
  if (theme.isTV && tvList) {
    // collections 18 `.trow`: room for a title that wraps to two lines (a list never cuts it) and its facts line.
    const words = 2 * textLine(tvListTitle(theme)) + theme.space.hairline * 5 + textLine(theme.type.about);
    return Math.max(tvRow.box, words) + 2 * tvRow.pad;
  }
  const help = textLine(theme.type.rowHelp);
  // A row with a kicker (a book's place in its series) has one more line, and one more gap, over its title.
  const words = (kicker ? help + theme.space.hairline * 2 : 0) + 2 * textLine(theme.type.episodeTitle) + help + theme.space.tight + Math.max(row.check, help) + 2 * theme.space.hairline * 2;
  return Math.max(titleListArt(theme) * 1.5, theme.isTV ? titleListArt(theme, true) : 0, words) + 2 * theme.space.tight;
}

/** One title in a phone's list of titles: what the page knows about it, never how big it is. */
export type TitleListItem = Readonly<{title: string;
  /** A small accent line over the title (a book's place in its series, collections 41c "Book 1"). */
  kicker?: string;
  /** Its year and length, drawn apart ("1995  1 h 21 min"), or who read a book. */
  facts: readonly string[];
  /** Strong accent words at the end of the facts line (a book's time left, collections 41b "4 h 34 min left"). */
  note?: string;
  /** A duration aligned after a TV book row’s title and narrator. */
  trailing?: string;
  /** TV playlist and collection rows (collections 18): the item's library, in its own column before the state. */
  library?: string;
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
export function TitleListRow({item, kickerRoom = !!item.kicker, longPressOnly, onFocus, tvList = false}: Readonly<{item: TitleListItem; kickerRoom?: boolean; longPressOnly?: boolean; onFocus?: () => void;
  /** TV: a playlist's or a collection's row (collections 18, 20 `.trow`), not a book's (collections 44 `.bkr`). */
  tvList?: boolean}>) {
  const theme = useTheme(), anchor = useRef<View>(null), more = item.onMore, t = defaultI18n.t;
  if (theme.isTV && tvList) return <TVListRow item={item} onFocus={onFocus}/>;
  const artWidth = titleListArt(theme, item.square), artHeight = item.square ? artWidth : artWidth * 1.5;
  const coverProgress = theme.isTV && item.square;
  // The card fills the row's own height (CardFrame grows into its parent).
  return <View style={{height: titleListRowHeight(theme, kickerRoom)}}><CardFrame testID={item.testID} title={item.title} menuAnchorRef={anchor} onNavigate={item.onNavigate} onMore={more ? () => more(anchor) : undefined}
    // CardFrame's own wrapper takes `style`: it fills the row, so the card (which grows into it) has the row's height.
    selection={item.selection} onFocus={onFocus} style={{flex: 1}} pressableStyle={{flex: 1, flexDirection: 'row', alignItems: 'center', gap: row.gap}}>
    {() => <>
      {item.selection?.active && <SelectionCircle selected={item.selection.selected} selecting interactive={false} variant="row" size={theme.space.md} accessibilityLabel={t('card.select', {title: item.title})}/>}
      <View>
        <Artwork source={item.artwork} width={artWidth} height={artHeight} style={{borderRadius: theme.radii.artwork}}
          fallback={!item.artwork && item.square ? missingArt({title: item.title, shape: 'square', width: artWidth, height: artHeight}) : undefined}/>
        {coverProgress && <Progress value={item.progress} style={{position: 'absolute', bottom: theme.space.xs, left: theme.space.xs, right: theme.space.xs}}/>}
      </View>
      <View style={{flex: 1, minWidth: 0, gap: theme.space.hairline * 2}}>
        {item.kicker && <Text style={[theme.type.rowHelp, {fontFamily: fontFamilies[700], color: theme.colors.accentStrong}]}>{item.kicker}</Text>}
        <Text style={[theme.type.episodeTitle, {color: theme.colors.text}]}>{item.title}</Text>
        {(item.facts.length > 0 || item.note) && <View style={{flexDirection: 'row', flexWrap: 'wrap', columnGap: theme.space.controlGap}}>
          {item.facts.map(fact => <Text key={fact} style={[theme.type.rowHelp, {color: theme.colors.textTertiary}]}>{fact}</Text>)}
          {item.note && <Text style={[theme.type.rowHelp, {fontFamily: fontFamilies[700], color: theme.colors.accentStrong}]}>{item.note}</Text>}</View>}
        {(item.watched || (!coverProgress && item.progress !== undefined)) && <View style={{flexDirection: 'row', alignItems: 'center', gap: theme.space.tight, marginTop: theme.space.tight}}>
          {item.watched ? <Icon name="i-check" size={row.check} color={theme.colors.accentStrong}/>
            : <View style={{width: row.bar, height: theme.spec.card.progress.height[theme.column], borderRadius: theme.space.hairline * 2, backgroundColor: theme.colors.raisedBright}}>
              <View style={{width: `${Math.min(1, item.progress ?? 0) * 100}%`, height: '100%', borderRadius: theme.space.hairline * 2, backgroundColor: theme.colors.accentStrong}}/></View>}
          <Text style={[theme.type.rowHelp, {fontFamily: fontFamilies[700], color: theme.colors.textSecondary}]}>{item.watched ? item.doneLabel ?? t('saved.watched') : item.remaining}</Text>
        </View>}
      </View>
      {item.trailing && <Text style={[theme.type.rowHelp, {color: theme.colors.textTertiary, fontVariant: ['tabular-nums']}]}>{item.trailing}</Text>}
      {more && !longPressOnly && <Focusable accessibilityLabel={t('card.more', {title: item.title})} onPress={() => more(anchor)} style={{width: row.more, height: row.more, alignItems: 'center', justifyContent: 'center'}}>
        <Icon name="i-more" size={row.moreIcon} color={theme.colors.textTertiary}/></Focusable>}
    </>}
  </CardFrame></View>;
}

/** collections 18 and 20 (`.trow`): the picture in a 76 box, the title at 28/700 over its facts at 22, and at the right how
 * far in (accent), Watched, or its length. The row's fill shows focus (`.trow.fz`); OK opens it, Hold OK its menu. */
function TVListRow({item, onFocus}: Readonly<{item: TitleListItem; onFocus?: () => void}>) {
  const theme = useTheme(), anchor = useRef<View>(null), more = item.onMore, t = defaultI18n.t;
  const artWidth = item.square ? tvRow.box : tvRow.poster;
  const state = item.watched ? item.doneLabel ?? t('saved.watched') : item.remaining ?? item.trailing;
  return <View style={{height: titleListRowHeight(theme, false, true)}}><CardFrame testID={item.testID} title={item.title} menuAnchorRef={anchor} onNavigate={item.onNavigate} onMore={more ? () => more(anchor) : undefined}
    selection={item.selection} onFocus={onFocus} style={{flex: 1}} focusScale={theme.spec.focus.scale.row.tv}
    pressableStyle={{flex: 1, flexDirection: 'row', alignItems: 'center', gap: tvRow.gap, paddingLeft: tvRow.insetLeft, paddingRight: tvRow.insetRight}}>
    {({focused}) => <>
      {/* `.trow.fz`: a focused row is filled, and grows a little like any row (R1). */}
      {focused && <View pointerEvents="none" style={{position: 'absolute', left: 0, right: 0, top: 0, bottom: 0, borderRadius: tvRow.radius, backgroundColor: `${theme.colors.text}${Math.round(tvRow.focusFill * 255).toString(16).padStart(2, '0')}`}}/>}
      {item.selection?.active && <SelectionCircle selected={item.selection.selected} selecting interactive={false} variant="row" size={theme.space.md} accessibilityLabel={t('card.select', {title: item.title})}/>}
      <View style={{width: tvRow.box, height: tvRow.box, alignItems: 'center', justifyContent: 'center'}}>
        <Artwork source={item.artwork} width={artWidth} height={tvRow.box} style={{borderRadius: theme.radii.artwork}}
          fallback={!item.artwork ? missingArt({title: item.title, shape: item.square ? 'square' : 'poster', width: artWidth, height: tvRow.box}) : undefined}/>
        {!item.watched && item.progress !== undefined && <View style={{position: 'absolute', bottom: 0, width: tvRow.bar, height: theme.spec.card.progress.height[theme.column], borderRadius: theme.space.hairline * 3, backgroundColor: theme.colors.ink, overflow: 'hidden'}}>
          <View style={{width: `${Math.min(1, item.progress) * 100}%`, height: '100%', backgroundColor: theme.colors.accentStrong}}/></View>}
      </View>
      <View style={{flex: 1, minWidth: 0, gap: theme.space.hairline * 5}}>
        {item.kicker && <Text style={[theme.type.rowHelp, {fontFamily: fontFamilies[700], color: theme.colors.accentStrong}]}>{item.kicker}</Text>}
        <Text style={[tvListTitle(theme), {color: theme.colors.text}]}>{item.title}</Text>
        {item.facts.length > 0 && <View style={{flexDirection: 'row', flexWrap: 'wrap', columnGap: theme.space.controlGap}}>
          {item.facts.map(fact => <Text key={fact} style={[theme.type.about, {color: theme.colors.textTertiary}]}>{fact}</Text>)}</View>}
      </View>
      {item.library !== undefined && <Text style={[theme.type.about, {width: tvRow.library, color: theme.colors.textTertiary}]}>
        {item.library}</Text>}
      {state !== undefined && <View style={{width: tvRow.state, flexDirection: 'row', justifyContent: 'flex-end', alignItems: 'center', gap: theme.space.controlGap}}>
        {item.watched && <Icon name="i-check" size={row.check} color={theme.colors.textTertiary}/>}
        <Text style={[theme.type.about, {textAlign: 'right', fontVariant: ['tabular-nums'], ...(item.remaining && !item.watched ? {fontFamily: fontFamilies[700], color: theme.colors.accentStrong} : {color: item.watched ? theme.colors.textTertiary : theme.colors.textSecondary})}]}>{state}</Text>
      </View>}
    </>}
  </CardFrame></View>;
}

/** A page's list of titles inside a music scroller (`musicScroller`: an author's or a series' books, collections 41b/41c):
 * its `TitleListRow`s, only the ones in view mounted, a hairline between them from the words on, and a heading drawn
 * with them as one section. A list read as a `WindowedCollection` takes `titleRowsScroller` instead. */
export function TitleRowWindow({heading, rows, testID, inset = true, longPressOnly, onFocusRow, tvList = false}: Readonly<{heading?: Readonly<{title: string; trailing?: string}>;
  rows: readonly (TitleListItem & Readonly<{id: string}>)[]; testID?: string; inset?: boolean; longPressOnly?: boolean; onFocusRow?: (index: number) => void;
  /** TV: the rows are a playlist's or a collection's (`.trow`), not a book list's. */ tvList?: boolean}>) {
  // One stride for the list: room for a kicker line on every row when any row carries one (a series' books).
  const theme = useTheme(), kicker = rows.some(item => !!item.kicker);
  return <View testID={testID}>
    {heading && <PanelBlock inset={inset}><PanelHeading title={heading.title} trailing={heading.trailing}/></PanelBlock>}
    <PanelBlock inset={inset}><MusicWindow items={rows} itemHeight={titleListRowHeight(theme, kicker, tvList)} renderItem={(item, index) => <View>
      {/* A TV list's rule runs the row's width under each row (`.trow::after`); elsewhere it starts at the words. */}
      {index > 0 && <View pointerEvents="none" style={{position: 'absolute', top: 0, right: theme.isTV && tvList ? tvRow.insetLeft : 0, left: theme.isTV && tvList ? tvRow.insetLeft : titleListArt(theme, item.square) + row.gap, height: theme.isTV && tvList ? theme.space.hairline * 1.5 : theme.space.hairline, backgroundColor: theme.colors.line}}/>}
      <TitleListRow item={item} kickerRoom={kicker} longPressOnly={longPressOnly} tvList={tvList} onFocus={onFocusRow && (() => onFocusRow(index))}/></View>}/></PanelBlock>
  </View>;
}
