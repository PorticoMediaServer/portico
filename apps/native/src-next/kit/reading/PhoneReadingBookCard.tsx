import {GuideProgrammeTitle} from '../live/GuideProgrammeTitle';
import React, {useRef, type RefObject} from 'react';
import {Text, View} from 'react-native';
import {generatedCoverTint} from '@portico/design';
import {cardAccessibilityLabel} from '../cards/model';
import {GeneratedCover, MediaArt} from '../artwork';
import type {ArtworkSource} from '../artwork';
import {Focusable} from '../controls';
import {SelectionCircle} from '../selection';
import type {CardSelection} from '../cards/model';
import {useTheme} from '../theme';

const glyph = (title: string): string => title.trim().slice(0, 1).toUpperCase() || '·';

/** The drawn sparse-metadata book cover: the offered artwork when the title has
 * one, otherwise the catalogue's canonical generated cover from the title and
 * author. It draws nothing else — no year, format, badge or progress — and it
 * takes its size from the caller, so the caller's pane geometry stays the only
 * source of width and height. */
export function PhoneReadingBookCover({title, author, width, height, artwork, periodical}: {
  title: string; author?: string; width: number; height: number; artwork?: ArtworkSource; periodical?: boolean;
}) {
  return <MediaArt kind={periodical ? 'periodical' : 'book'} source={artwork} width={width} height={height}
    fallback={<GeneratedCover title={title} author={author} glyph={glyph(title)} tint={generatedCoverTint(title)} width={width} height={height}/>}/>;
}

/** One book cover with its title and author, for the phone's reading rows
 * (phone-read frames 1-3): 2:3 for a book, 3:4 only when the item is a
 * periodical. Tap opens or toggles selection; a long press opens the item's
 * standard menu, which owns the Download offer. Every field drawn here is an offered
 * display value and every action is the caller's own handler; the component
 * holds no content entry, client or authorization state. */
export function PhoneReadingBookCard({testID, title, author, detail, width, artwork, periodical, onOpen, onMenu, selection, disabled, onCaptionLayout}: Readonly<{
  testID: string; title: string; author?: string; detail?: string; width: number; artwork?: ArtworkSource; periodical?: boolean;
  onOpen: () => void; onMenu?: (anchor: RefObject<View | null>) => void; selection?: CardSelection; disabled?: boolean; onCaptionLayout?: (height: number) => void;
}>) {
  const anchor = useRef<View>(null);
  const theme = useTheme(), height = periodical ? width * 4 / 3 : width * 3 / 2;
  return <Focusable testID={testID} accessibilityRole="button" accessibilityLabel={cardAccessibilityLabel(title, [], [], [author, detail])} focusKind="row"
    ref={anchor} disabled={disabled} accessibilityState={selection?.active ? {selected: selection.selected} : undefined}
    onPress={selection?.active ? selection.onToggle : onOpen} onLongPressAction={onMenu ? () => onMenu(anchor) : undefined} style={{width}}>
    <View accessible={false} importantForAccessibility="no-hide-descendants" style={{width, height, borderRadius: 10, overflow: 'hidden', borderWidth: selection?.selected ? 2 : 0, borderColor: theme.colors.accentStrong}}>
      <PhoneReadingBookCover title={title} author={author} width={width} height={height} artwork={artwork} periodical={periodical}/>
      {selection?.active ? <SelectionCircle selected={selection.selected} selecting interactive={false} accessibilityLabel={title}
        style={{position: 'absolute', right: 7, bottom: 7}}/> : null}
    </View>
    <View style={{paddingTop: theme.space.tight, gap: theme.space.hairline}}
      onLayout={onCaptionLayout && (event => onCaptionLayout(event.nativeEvent.layout.height))}>
      {/* A card in a grid or shelf: two lines, then "…" on a whole word (owner, 20 Nov); the card's label reads the whole title. */}
      <View style={{flexDirection: 'row'}}><GuideProgrammeTitle title={title} lines={2} style={[theme.type.cardTitle, {color: theme.colors.text}]}/></View>
      {author && <Text style={[theme.type.cardSubtitle, {color: theme.colors.textTertiary}]}>{author}</Text>}
      {detail && <Text style={[theme.type.cardSubtitle, {color: theme.colors.textTertiary}]}>{detail}</Text>}
    </View>
  </Focusable>;
}
