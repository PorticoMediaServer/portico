import React, {useRef, useState, type ReactNode, type RefObject} from 'react';
import {ScrollView, Text, View, useWindowDimensions} from 'react-native';
import {useSafeAreaInsets} from 'react-native-safe-area-context';
import {defaultI18n} from '../../../../../packages/i18n/src';
import {Artwork, type ArtworkSource} from '../artwork';
import {Focusable} from '../controls';
import {Icon, type IconName} from '../Icon';
import {GuideProgrammeTitle} from '../live/GuideProgrammeTitle';
import {textLine, useTheme} from '../theme';
import {alpha} from '../browse/presentation';
import {savedNearEnd} from '../../screens/saved/paging';
import {SavedLoading, SavedTail} from './SavedLoading';

const t = defaultI18n.t;

type DirectoryItem = Readonly<{
  id: string; title: string; caption: readonly string[]; note?: Readonly<{text: string; icon: IconName; strong: boolean}>;
  summary?: string; mosaic: readonly (ArtworkSource | undefined)[]; filterTile?: boolean; pinned?: boolean; menuOpen?: boolean;
  onPress: () => void; onMenu?: (anchor: RefObject<View | null>) => void;
}>;

export function SavedDirectoryBody({testID, visible, toolbar, help, notice, state, rows, loading, paging, onNearEnd, overlays}: {
  testID: string; visible: boolean; toolbar?: ReactNode; help?: string; notice?: ReactNode; state?: ReactNode;
  rows: readonly DirectoryItem[]; loading: boolean; paging: boolean; onNearEnd: () => void; overlays?: ReactNode;
}) {
  const theme = useTheme(), insets = useSafeAreaInsets(), window = useWindowDimensions();
  const tablet = theme.platform !== 'phone', gap = theme.space.sm;
  const [outerWidth, setOuterWidth] = useState(window.width);
  const paneWidth = Math.max(1, outerWidth - theme.gutter * 2);
  const columns = paneWidth >= 1000 ? 4 : paneWidth >= 640 ? 3 : 2;
  const width = Math.max(1, Math.floor((paneWidth - gap * (columns - 1)) / columns));
  const viewport = useRef({offset: 0, height: 0, extent: 0});
  const load = () => {
    if (savedNearEnd(viewport.current.offset, viewport.current.height, viewport.current.extent, tablet ? width + 110 : 80, tablet ? columns : 1)) onNearEnd();
  };
  return <View testID={testID} pointerEvents={visible ? 'auto' : 'none'} accessibilityElementsHidden={!visible}
    importantForAccessibility={visible ? 'auto' : 'no-hide-descendants'}
    onLayout={event => setOuterWidth(event.nativeEvent.layout.width)} style={{flex: 1}}>
    {toolbar}
    <ScrollView onLayout={event => {viewport.current.height = event.nativeEvent.layout.height; load();}}
      onContentSizeChange={(_width, height) => {viewport.current.extent = height; load();}}
      onScroll={event => {viewport.current.offset = event.nativeEvent.contentOffset.y; load();}}
      scrollEventThrottle={64} contentContainerStyle={{paddingBottom: theme.space.lg + insets.bottom}}>
      {help && <Text style={[theme.type.rowHelp, {color: theme.colors.textTertiary, marginHorizontal: theme.gutter, marginVertical: theme.space.sm}]}>{help}</Text>}
      {notice && <View style={{paddingHorizontal: theme.gutter, paddingTop: theme.space.sm}}>{notice}</View>}
      {loading ? <SavedLoading kind="directory"/> : state ?? (tablet ?
        <View style={{flexDirection: 'row', flexWrap: 'wrap', gap, marginHorizontal: theme.gutter, marginTop: theme.space.xs}}>
          {rows.map(item => <View key={item.id} style={{width}}><SavedDirectoryTile {...item}/></View>)}
        </View> : rows.map(item => <SavedDirectoryRow key={item.id} {...item}/>))}
      {paging && <View style={{marginHorizontal: theme.gutter, marginTop: theme.space.sm}}><SavedTail row/></View>}
    </ScrollView>
    {overlays}
  </View>;
}

function SavedDirectoryTile({id, title, caption, note, summary, mosaic, filterTile, pinned, menuOpen, onPress, onMenu}: DirectoryItem) {
  const theme = useTheme(), [artWidth, setArtWidth] = useState(0), anchor = useRef<View>(null), paths = mosaic.filter((source): source is NonNullable<typeof source> => !!source).slice(0, 4);
  return <View style={{position: 'relative', borderRadius: 14, backgroundColor: theme.colors.raised, overflow: theme.isTV ? undefined : 'hidden', borderWidth: menuOpen ? 1 : 0, borderColor: theme.colors.accent}}>
    <Focusable testID={`saved-directory-${encodeURIComponent(id)}`} onPress={onPress} focusKind="row" focusRadius={14} accessibilityRole="button" accessibilityLabel={title}>
      <View onLayout={event => setArtWidth(event.nativeEvent.layout.width)} style={{width: '100%', aspectRatio: 1, flexDirection: 'row', flexWrap: 'wrap', backgroundColor: theme.colors.ink}}>
        {paths.map((source, index) => <Artwork key={`${source.cacheKey}:${index}`} source={source} width={Math.max(1, artWidth / 2)} height={Math.max(1, artWidth / 2)} visible priority={index === 0} style={{borderRadius: 0}}/>)}
        {!paths.length && filterTile && <View style={{flex: 1, alignItems: 'center', justifyContent: 'center'}}><Icon name="i-filter" size={32} color={theme.colors.text}/></View>}
      </View>
      <View style={{padding: 12, gap: 4}}>
        <View style={{flexDirection: 'row', minHeight: 2 * textLine(theme.type.cardTitle)}}>
          <GuideProgrammeTitle title={title} lines={2} style={[theme.type.cardTitle, {color: theme.colors.text}]}/>
        </View>
        {!!caption.length && <View style={{flexDirection: 'row', flexWrap: 'wrap', gap: 8}}>{caption.map(part => <Text key={part} style={[theme.type.rowHelp, {color: theme.colors.textTertiary, flexShrink: 1}]}>{part}</Text>)}</View>}
        {note && <View style={{flexDirection: 'row', alignItems: 'center', gap: 5, marginTop: 1}}><Icon name={note.icon} size={13} color={note.strong ? theme.colors.accentStrong : theme.colors.textTertiary}/>
          <Text style={[note.strong ? theme.type.captionStrong : theme.type.rowHelp, {color: note.strong ? theme.colors.accentStrong : theme.colors.textTertiary, flexShrink: 1}]}>{note.text}</Text></View>}
        {!!summary && <Text style={[theme.type.rowHelp, {color: theme.colors.textSecondary}]}>{summary}</Text>}
        {pinned && <Icon name="i-bookmark-f" size={16} color={theme.colors.accentStrong}/>}
      </View>
    </Focusable>
    {onMenu && <Focusable ref={anchor} testID={`saved-directory-more-${encodeURIComponent(id)}`} accessibilityRole="button" accessibilityLabel={t('card.more', {title})} onPress={() => onMenu(anchor)} focusRadius={16}
      style={{position: 'absolute', top: 8, right: 8, width: 32, height: 32, borderRadius: 16, alignItems: 'center', justifyContent: 'center', backgroundColor: theme.colors.ink}}>
      <Icon name="i-more" size={20} color={theme.colors.text}/>
    </Focusable>}
  </View>;
}

function SavedDirectoryRow({id, title, caption, note, summary, mosaic, filterTile, pinned, onPress}: DirectoryItem) {
  const theme = useTheme(), paths = mosaic.filter((source): source is NonNullable<typeof source> => !!source).slice(0, 4);
  return <Focusable testID={`saved-directory-${encodeURIComponent(id)}`} focusId={`saved-directory-${id}`} accessibilityRole="button" accessibilityLabel={title} onPress={onPress}
    focusKind="row" style={{flexDirection: 'row', alignItems: 'center', gap: 14, paddingVertical: 8, paddingLeft: theme.gutter, paddingRight: 18, minHeight: 80}}>
      {filterTile ? <View style={{width: 56, height: 56, borderRadius: 12, backgroundColor: theme.colors.raisedBright, alignItems: 'center', justifyContent: 'center'}}>
        <Icon name="i-filter" size={24} color={theme.colors.text}/></View>
        : <View style={{width: 64, height: 64, borderRadius: 10, overflow: 'hidden', backgroundColor: theme.colors.ink, flexDirection: 'row', flexWrap: 'wrap', gap: 1}}>
          {paths.map((source, index) => <Artwork key={`${source.cacheKey}:${index}`} source={source} width={31.5} height={31.5} visible priority={index === 0} style={{borderRadius: 0}}/>)}
        </View>}
      <View style={{flex: 1, minWidth: 0, gap: 3}}>
        <Text style={[theme.type.listLabel, {color: theme.colors.text}]}>{title}</Text>
        {!!caption.filter(Boolean).length && <View style={{flexDirection: 'row', flexWrap: 'wrap', gap: 8}}>{caption.filter((part): part is string => !!part).map(part =>
          <Text key={part} style={[theme.type.rowHelp, {color: theme.colors.textTertiary, flexShrink: 1}]}>{part}</Text>)}</View>}
        {note && <View style={{flexDirection: 'row', alignItems: 'center', gap: 5, marginTop: 1}}><Icon name={note.icon} size={13} color={note.strong ? theme.colors.accentStrong : theme.colors.textTertiary}/>
          <Text style={[note.strong ? theme.type.captionStrong : theme.type.rowHelp, {color: note.strong ? theme.colors.accentStrong : theme.colors.textTertiary, flexShrink: 1}]}>{note.text}</Text></View>}
        {!!summary && <Text style={[theme.type.rowHelp, {color: theme.colors.textSecondary}]}>{summary}</Text>}
      </View>
      {pinned && <Icon name="i-bookmark-f" size={16} color={theme.colors.accentStrong}/>}
      <Icon name="i-right" size={18} color={theme.colors.lineStrong}/>
    <View pointerEvents="none" style={{position: 'absolute', left: 98, right: 0, bottom: 0, height: 1, backgroundColor: alpha(theme.colors.lineStrong, .35)}}/>
  </Focusable>;
}
