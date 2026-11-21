import React, {useRef} from 'react';
import {Text, View} from 'react-native';
import {generatedCoverTint} from '@portico/design';
import {defaultI18n} from '../../../../../packages/i18n/src';
import type {ReadingItem} from '../../../../../packages/client-core/src';
import type {NativeShellRuntime} from '../../shell/runtime';
import {Artwork, GeneratedCover} from '../artwork';
import {alpha} from '../browse/presentation';
import type {CardSelection} from '../cards/model';
import {Focusable} from '../controls';
import {GuideProgrammeTitle} from '../live/GuideProgrammeTitle';
import {SelectionCircle} from '../selection';
import {textLine, useTheme} from '../theme';

const {t} = defaultI18n;
const initial = (title: string) => title.trim().slice(0, 1).toUpperCase() || '·';

/** The drawn volume card, shared with the book page's series shelf. The shelf
 * variant only changes the measured title/number metrics and their order to the
 * frame's card (title above its "Book N" line); the series page keeps its own. */
export function VolumeCard({item, width, height, comic, theme, runtime, progression, onOpen, onMenu, selection, variant = 'default'}: {
  item: ReadingItem; width: number; height: number; comic: boolean; theme: ReturnType<typeof useTheme>;
  runtime: NativeShellRuntime; progression?: number; onOpen: () => void; onMenu?: (anchor: React.RefObject<View | null>) => void; selection?: CardSelection; variant?: 'default' | 'shelf';
}) {
  // N5-4: a book the server can't serve now is drawn but takes no open, menu or selection (as web).
  const anchor = useRef<View>(null), unavailable = item.inventoryAvailable !== true;
  const pick = unavailable ? undefined : selection, menu = unavailable ? undefined : onMenu;
  const number = item.series?.number, shelf = variant === 'shelf', phoneDefault = !shelf && theme.platform === 'phone';
  const numberLabel = number === undefined ? undefined : t(comic ? 'next.reader.volumeNumber' : 'next.reader.bookNumber', {number: defaultI18n.number(number, {maximumFractionDigits: 20})});
  const numberText = numberLabel === undefined ? null : <Text style={[theme.type.caption, {color: theme.colors.textTertiary, marginTop: theme.space.xs}]}>{numberLabel}</Text>;
  const titleText = <View style={{flexDirection: 'row', marginTop: theme.space.xs, minHeight: 2 * textLine(theme.type.cardTitle)}}>
    <GuideProgrammeTitle title={item.title} lines={2} style={[theme.type.cardTitle, {color: theme.colors.text}]}/>
  </View>;
  return <Focusable accessibilityRole="button" accessibilityLabel={item.title} focusKind="row" focusRadius={10} ref={anchor} disabled={unavailable} onPress={pick?.active ? pick.onToggle : onOpen}
    onLongPressAction={menu ? () => menu(anchor) : undefined} accessibilityState={pick?.active ? {selected: pick.selected} : unavailable ? {disabled: true} : undefined} style={{width}}>
    <View style={{width, height, borderRadius: 10, overflow: 'hidden', backgroundColor: theme.colors.raised, borderWidth: pick?.selected ? 2 : 0, borderColor: theme.colors.accentStrong}}>
      {runtime.artwork(item.coverUrl) ? <Artwork source={runtime.artwork(item.coverUrl)} width={width} height={height}/>
        : <GeneratedCover title={item.title} author={item.author?.value} tint={generatedCoverTint(item.title)} glyph={initial(item.title)} width={width} height={height}/>}
      {progression !== undefined ? <View style={{position: 'absolute', left: 0, right: 0, bottom: 0, height: 3, backgroundColor: alpha(theme.colors.text, .2)}}>
        <View style={{width: `${progression * 100}%`, height: 3, backgroundColor: theme.colors.accentStrong}}/>
      </View> : null}
      {pick?.active ? <SelectionCircle selected={pick.selected} selecting interactive={false} accessibilityLabel={item.title}
        style={{position: 'absolute', right: 7, bottom: 7}}/> : null}
    </View>
    {shelf || phoneDefault ? <>{titleText}{numberText}</> : <>{numberText}{titleText}</>}
    {item.author && !shelf && !phoneDefault ? <Text style={[theme.type.caption, {color: theme.colors.textTertiary, marginTop: theme.space.xs}]}>{item.author.value}</Text> : null}
  </Focusable>;
}
