import React, {useRef} from 'react';
import {Text, View} from 'react-native';
import type {ContentEntry} from '../../../../../packages/client-core/src/library-content';
import {MediaArt} from '../../kit/artwork';
import {CardFrame, CardAction, Parts} from '../../kit/cards/primitives';
import type {CardSelection} from '../../kit/cards';
import {SelectionCircle} from '../../kit/selection';
import {browseType} from '../../kit/browse/presentation';
import {useTheme} from '../../kit/theme';
import type {NativeShellRuntime} from '../../shell/runtime';
import {homeCardText} from '../home/model';
import type {NativeLibraryActions} from './LibraryScreen';

/** The existing 70pt catalogue row: genuine title/art/facts and the same card
 * activation, selection and menu handling as the poster presentation. */
export function LibraryListRow({entry, width, runtime, actions, selected, disabled, focusId, onFocus, onBlur, onSelect}: Readonly<{
  entry: ContentEntry; width: number; runtime: NativeShellRuntime; actions: NativeLibraryActions;
  selected?: CardSelection; disabled?: boolean; focusId: string; onFocus: () => void; onBlur: () => void;
  /** The menu's Select row: this row's real selection entry. */
  onSelect?: () => void;
}>) {
  const theme = useTheme(), menuAnchorRef = useRef<View>(null), title = entry.displayTitle || entry.title;
  const text = homeCardText(entry, false, runtime.text), t = runtime.text.t;
  const square = ['album', 'song', 'book', 'audiobook_file'].includes(entry.kind);
  const height = 70 * (theme.fontScale ?? 1);
  return <CardFrame testID={`library-list-${encodeURIComponent(focusId)}`} focusId={focusId} title={title} caption={text.caption} detail={text.detail} width={width} disabled={disabled}
    selection={selected} onNavigate={() => actions.open(entry)} onPlay={entry.playback ? () => actions.play(entry) : undefined}
    onMore={() => actions.menu(entry, menuAnchorRef, onSelect)} moreExpanded={actions.isMenuOpen?.(entry)} menuAnchorRef={menuAnchorRef} onFocus={onFocus} onBlur={onBlur}
    pressableStyle={{height, borderRadius: 10, justifyContent: 'center'}}>
    {() => <View style={{height, flexDirection: 'row', alignItems: 'center', gap: 16, paddingHorizontal: 12}}>
      {selected?.active && <SelectionCircle selected={selected.selected} selecting interactive={false} variant="row" size={24} accessibilityLabel={t('card.select', {title})}/>}
      <MediaArt kind={square ? 'square' : 'poster'} source={runtime.artwork(entry.posterUrl)} width={square ? 54 : 36} height={54} style={{borderRadius: 5}}/>
      <View style={{flex: 1, minWidth: 0, gap: 3}}>
        <Text numberOfLines={1} style={[browseType(theme, 15, 600), {color: selected?.selected ? theme.colors.accentStrong : theme.colors.text}]}>{title}</Text>
        <Parts parts={text.caption} style={[browseType(theme, 12.5, 400), {color: theme.colors.textSecondary}]}/>
      </View>
      {!disabled && !selected?.active && <View style={{flexDirection: 'row', gap: 6}}>
        {entry.playback && <CardAction focusId={`${focusId}:list-play`} name="i-play" label={t('card.play', {title})} size={36} onPress={() => actions.play(entry)}/>}
        <CardAction focusId={`${focusId}:list-more`} name="i-more" label={t('card.more', {title})} size={36} color={theme.colors.textSecondary}
          style={{backgroundColor: 'transparent'}} onPress={() => actions.menu(entry, menuAnchorRef, onSelect)}/>
      </View>}
      <View pointerEvents="none" style={{position: 'absolute', left: 12, right: 12, bottom: 0, height: 1, backgroundColor: theme.colors.line}}/>
    </View>}
  </CardFrame>;
}
