import React, {useRef} from 'react';
import {View} from 'react-native';
import {cardText} from '../../../../../packages/client-core/src/presentation/kit-model';
import type {ContentEntry} from '../../../../../packages/client-core/src/library-content';
import {MediaArt} from '../artwork';
import {CardFrame, CardAction, Caption} from '../cards/primitives';
import type {CardSelection} from '../cards';
import {SelectionCircle} from '../selection';
import {frameSpec} from '@portico/design';
import {useTheme} from '../theme';
import type {NativeShellRuntime} from '../../shell/runtime';
import {homeCardText} from '../../screens/home/model';
import type {NativeLibraryActions} from '../../screens/library/LibraryScreen';

/** The catalogue row: genuine title/art/facts and the same card
 * activation, selection and menu handling as the poster presentation. */
export function LibraryListRow({entry, width, runtime, actions, selected, disabled, focusId, onFocus, onBlur, onSelect}: Readonly<{
  entry: ContentEntry; width: number; runtime: NativeShellRuntime; actions: NativeLibraryActions;
  selected?: CardSelection; disabled?: boolean; focusId: string; onFocus: () => void; onBlur: () => void;
  /** The menu's Select row: this row's real selection entry. */
  onSelect?: () => void;
}>) {
  const theme = useTheme(), menuAnchorRef = useRef<View>(null), title = entry.displayTitle || entry.title;
  const text = entry.kind === 'song'
    ? cardText({id: entry.id, kind: 'song', title, artist: entry.artist?.name, album: entry.album?.name, year: entry.year, durationSeconds: entry.duration}, 'song-row', runtime.text)
    : homeCardText(entry, false, runtime.text);
  const t = runtime.text.t;
  const square = ['album', 'song', 'book', 'audiobook_file'].includes(entry.kind);
  const height = frameSpec.browse.listRow[theme.form] * theme.fontScale;
  return <CardFrame testID={`library-list-${encodeURIComponent(focusId)}`} focusId={focusId} title={title} caption={text.caption} detail={text.detail} width={width} disabled={disabled}
    selection={selected} onNavigate={() => actions.open(entry)} onPlay={entry.playback ? () => actions.play(entry) : undefined}
    onMore={() => actions.menu(entry, menuAnchorRef, onSelect)} moreExpanded={actions.isMenuOpen?.(entry)} menuAnchorRef={menuAnchorRef} onFocus={onFocus} onBlur={onBlur}
    pressableStyle={{height, borderRadius: 10, justifyContent: 'center'}}>
    {() => <View style={{height, flexDirection: 'row', alignItems: 'center', gap: 16, paddingHorizontal: 12}}>
      {selected?.active && <SelectionCircle selected={selected.selected} selecting interactive={false} variant="row" size={24} accessibilityLabel={t('card.select', {title})}/>}
      <MediaArt kind={square ? 'square' : 'poster'} source={runtime.artwork(entry.posterUrl)} width={square ? 54 : 36} height={54} style={{borderRadius: 5}}/>
      <View style={{flex: 1, minWidth: 0, gap: 3}}>
        <Caption title={title} titleLines={2} titleMargin={0} selected={selected?.selected}
          titleTextStyle={theme.type.rowLabel} caption={text.caption} captionTextStyle={theme.type.caption}/>
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
