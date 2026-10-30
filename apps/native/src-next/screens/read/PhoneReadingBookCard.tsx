import React, {useRef, type RefObject} from 'react';
import {Text, View} from 'react-native';
import {generatedCoverTint} from '@portico/design';
import {cardAccessibilityLabel} from '../../kit/cards/model';
import {GeneratedCover, MediaArt} from '../../kit/artwork';
import type {ArtworkSource} from '../../kit/artwork';
import {Focusable} from '../../kit/controls';
import {SelectionCircle} from '../../kit/selection';
import type {CardSelection} from '../../kit/cards/model';
import {browseType} from '../../kit/browse/presentation';
import {useTheme} from '../../kit/theme';

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
export function PhoneReadingBookCard({testID, title, author, width, artwork, periodical, onOpen, onMenu, selection}: Readonly<{
  testID: string; title: string; author?: string; width: number; artwork?: ArtworkSource; periodical?: boolean;
  onOpen: () => void; onMenu?: (anchor: RefObject<View | null>) => void; selection?: CardSelection;
}>) {
  const anchor = useRef<View>(null);
  const theme = useTheme(), height = periodical ? width * 4 / 3 : width * 3 / 2;
  return <Focusable testID={testID} accessibilityRole="button" accessibilityLabel={cardAccessibilityLabel(title, [], [], [author])} focusKind="row"
    ref={anchor} accessibilityState={selection?.active ? {selected: selection.selected} : undefined}
    onPress={selection?.active ? selection.onToggle : onOpen} onLongPressAction={onMenu ? () => onMenu(anchor) : undefined} style={{width}}>
    <View accessible={false} importantForAccessibility="no-hide-descendants" style={{width, height, borderRadius: 10, overflow: 'hidden', borderWidth: selection?.selected ? 2 : 0, borderColor: theme.colors.accentStrong}}>
      <PhoneReadingBookCover title={title} author={author} width={width} height={height} artwork={artwork} periodical={periodical}/>
      {selection?.active ? <SelectionCircle selected={selection.selected} selecting interactive={false} accessibilityLabel={title}
        style={{position: 'absolute', right: 7, bottom: 7}}/> : null}
    </View>
    <Text numberOfLines={1} style={[browseType(theme, 12.5, 600), {color: theme.colors.text, marginTop: 7}]}>{title}</Text>
    {author ? <Text numberOfLines={1} style={[browseType(theme, 11.5, 400), {color: theme.colors.textTertiary, marginTop: 2}]}>{author}</Text> : null}
  </Focusable>;
}
