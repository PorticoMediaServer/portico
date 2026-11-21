import React, {useRef, type ReactNode, type RefObject} from 'react';
import {ScrollView, Text, View, type NativeScrollEvent, type NativeSyntheticEvent, type ScrollViewProps} from 'react-native';
import {defaultI18n} from '../../../../../packages/i18n/src';
import type {ContentEntry} from '../../../../../packages/client-core/src/library-content';
import type {NativeShellRuntime} from '../../shell/runtime';
import type {NativeSavedActions} from '../../pages/shared/saved/useSavedPage';
import {savedCardText} from '../../screens/home/model';
import {PosterCard, SquareCard, type CardSelection} from '../cards';
import {CardFrame, Parts} from '../cards/primitives';
import {Artwork} from '../artwork';
import {Chip, Focusable} from '../controls';
import {Icon, type IconName} from '../Icon';
import {TVRow} from '../layout/TVRow';
import {SelectionCircle} from '../selection';
import {alpha} from '../browse/presentation';
import {useTheme} from '../theme';
import {FiltersButton, SortButton, ViewToggle} from '../browse/Toolbar';
import {sortDirectionKind} from '../../../../../packages/client-core/src/presentation/browse-toolbar';

const t = defaultI18n.t;
export const savedShelfWidth = {poster: 118, square: 132};

export function savedGridCardWidth(width: number, columns: number) {
  return width ? Math.floor(width / columns - 12) : undefined;
}

/** The phone row opens its full view and keeps the drawn 28 shelf rhythm; the
 * first row sits 2 below the large title's 12 bottom padding. */
export function SavedSection({first = false, children}: {first?: boolean; children: React.ReactNode}) {
  const theme = useTheme();
  return <View style={{marginTop: first ? 2 : theme.spec.shelf.marginTop[theme.column]}}>{children}</View>;
}

export function SavedMyListRow({entry, runtime, actions, selection}: {entry: ContentEntry; runtime: NativeShellRuntime; actions: NativeSavedActions; selection?: CardSelection}) {
  const theme = useTheme(), text = savedCardText(entry, runtime.text), menuAnchorRef = useRef<View>(null);
  return <View style={{marginHorizontal: 16, borderBottomWidth: 1, borderBottomColor: alpha(theme.colors.lineStrong, .45)}}>
    <CardFrame title={entry.displayTitle || entry.title} menuAnchorRef={menuAnchorRef} moreExpanded={actions.isMenuOpen?.(entry)} testID={`saved-my-list-row-${encodeURIComponent(entry.id)}`}
      onNavigate={() => actions.openEntry(entry)} onMore={() => actions.menuEntry(entry, menuAnchorRef, selection?.onEnter)} selection={selection} pressableStyle={{flexDirection: 'row', alignItems: 'center', gap: 13, paddingVertical: 9}}>
      {() => <>
        {selection?.active && <SelectionCircle selected={selection.selected} selecting interactive={false} variant="row" size={24} accessibilityLabel={t('card.select', {title: entry.displayTitle || entry.title})}/>}
        <Artwork source={runtime.artwork(entry.posterUrl)} width={46} height={69} style={{borderRadius: 6}}/>
        <View style={{flex: 1, minWidth: 0}}><Text style={[theme.type.listLabel, {color: theme.colors.text}]}>{entry.displayTitle || entry.title}</Text>
          <Parts parts={text.caption} fit="wrap" lines={0} gap={8} style={[theme.type.rowHelp, {color: theme.colors.textTertiary, marginTop: 2, maxWidth: '100%'}]}/></View>
      </>}
    </CardFrame>
  </View>;
}

export function SavedMyListCard({entry, runtime, actions, selection, width}: {entry: ContentEntry; runtime: NativeShellRuntime; actions: NativeSavedActions; selection?: CardSelection; width?: number}) {
  const menuAnchorRef = useRef<View>(null), text = savedCardText(entry, runtime.text);
  const props = {testID: `saved-my-list-${encodeURIComponent(entry.id)}`, title: entry.displayTitle || entry.title, caption: text.caption, ...(width === undefined ? {} : {width}),
    artwork: runtime.artwork(entry.posterUrl), onNavigate: () => actions.openEntry(entry), onMore: () => actions.menuEntry(entry, menuAnchorRef, selection?.onEnter), selection,
    menuAnchorRef, moreExpanded: actions.isMenuOpen?.(entry)};
  return entry.kind === 'song' || entry.kind === 'album' || entry.kind === 'book' || entry.kind === 'audiobook_file' ? <SquareCard {...props}/> : <PosterCard {...props}/>;
}

export function SavedRowHeading({title, detail, onPress, testID}: {title: string; detail?: string; onPress: () => void; testID: string}) {
  const theme = useTheme();
  return <View style={{flexDirection: 'row', alignItems: 'baseline', gap: theme.spec.shelf.headingGap[theme.column], marginHorizontal: theme.gutter,
    marginBottom: theme.spec.shelf.headingMargin[theme.column]}}>
    <Focusable testID={testID} accessibilityRole="button" accessibilityLabel={title} onPress={onPress} focusRadius={8}
      style={{flexDirection: 'row', alignItems: 'center', gap: 1, flexShrink: 1}}>
      <Text accessibilityRole="header" style={[theme.type.rowHeading, {color: theme.colors.text}]}>{title}</Text>
      <Icon name="i-right" size={18} color={theme.colors.textTertiary}/>
    </Focusable>
    {detail && <Text style={[theme.type.caption, {color: theme.colors.textTertiary}]}>{detail}</Text>}
  </View>;
}

export function SavedLink({label, caption, icon, value, separator, onPress, testID}: {label: string; caption?: string; icon: IconName; value?: string; separator: boolean; onPress: () => void; testID: string}) {
  const theme = useTheme();
  return <Focusable testID={testID} accessibilityLabel={label} onPress={onPress} style={{position: 'relative', flexDirection: 'row', alignItems: 'center', minHeight: 48, paddingVertical: 8, paddingHorizontal: 14, gap: 12}}>
    {separator && <View style={{position: 'absolute', left: 56, right: 0, top: 0, height: 1, backgroundColor: alpha(theme.colors.lineStrong, .45)}}/>}
    <View style={{width: 30, height: 30, borderRadius: 8, alignItems: 'center', justifyContent: 'center', backgroundColor: theme.colors.raisedBright}}><Icon name={icon} size={18} color={theme.colors.textSecondary}/></View>
    <View style={{flex: 1, minWidth: 0}}>
      <Text style={[theme.type.rowLabel, {color: theme.colors.text}]}>{label}</Text>
      {caption && <Text style={[theme.type.rowHelp, {color: theme.colors.textTertiary, marginTop: 2}]}>{caption}</Text>}
    </View>
    {value !== undefined && <Text style={[theme.type.intro, {color: theme.colors.textTertiary}]}>{value}</Text>}
    <Icon name="i-right" size={18} color={theme.colors.lineStrong}/>
  </Focusable>;
}

export function SavedSurface({visible, children}: {visible: boolean; children: ReactNode}) {
  return <View testID="native-saved-screen" pointerEvents={visible ? 'auto' : 'none'} accessibilityElementsHidden={!visible}
    importantForAccessibility={visible ? 'auto' : 'no-hide-descendants'} style={{flex: 1}}>{children}</View>;
}

export function SavedScroll({node, refresh, children, onHeight, onExtent, onOffset}: {
  node: RefObject<ScrollView | null>; refresh?: ScrollViewProps['refreshControl']; children: ReactNode;
  onHeight: (height: number) => void; onExtent: (height: number) => void; onOffset: (offset: number) => void;
}) {
  const theme = useTheme();
  return <ScrollView ref={node} refreshControl={refresh}
    onLayout={event => onHeight(event.nativeEvent.layout.height)} onContentSizeChange={(_width, height) => onExtent(height)}
    onScroll={event => onOffset(event.nativeEvent.contentOffset.y)} scrollEventThrottle={64}
    contentContainerStyle={{paddingBottom: theme.space.lg}}>{children}</ScrollView>;
}

export function SavedShelf({children, onScroll}: {children: ReactNode; onScroll?: (offset: number, width: number, extent: number) => void}) {
  const theme = useTheme();
  const scroll = onScroll && ((event: NativeSyntheticEvent<NativeScrollEvent>) => {
    const {contentOffset, layoutMeasurement, contentSize} = event.nativeEvent;
    onScroll(contentOffset.x, layoutMeasurement.width, contentSize.width);
  });
  return <TVRow><ScrollView horizontal showsHorizontalScrollIndicator={false} scrollEventThrottle={64} onScroll={scroll}
    contentContainerStyle={{paddingHorizontal: theme.gutter, paddingTop: theme.spec.shelf.phonePaddingTop,
      paddingBottom: theme.spec.shelf.phonePaddingBottom, gap: theme.spec.shelf.gap[theme.column]}}>{children}</ScrollView></TVRow>;
}

export function SavedLinkGroup({children}: {children: ReactNode}) {
  const theme = useTheme();
  return <View style={{marginHorizontal: theme.gutter, marginTop: theme.space.md, borderRadius: theme.radii.artwork,
    backgroundColor: theme.colors.raised, overflow: 'hidden'}}>{children}</View>;
}

export function SavedPinnedHeading({onEdit}: {onEdit: () => void}) {
  const theme = useTheme();
  return <View style={{flexDirection: 'row', alignItems: 'baseline', justifyContent: 'space-between', marginTop: theme.space.md,
    marginHorizontal: theme.gutter}}>
    <Text accessibilityRole="header" style={[theme.type.rowHeading, {color: theme.colors.text}]}>{t('pins.heading')}</Text>
    <Focusable testID="saved-edit-pins" accessibilityLabel={t('pins.edit')} onPress={onEdit}>
      <Text style={[theme.type.seeAll, {color: theme.colors.textTertiary}]}>{t('pins.edit')}</Text>
    </Focusable>
  </View>;
}

export function SavedGrid({columns, onWidth, children}: {columns: number; onWidth: (width: number) => void; children: ReactNode}) {
  const theme = useTheme();
  return <View onLayout={event => onWidth(event.nativeEvent.layout.width)}
    style={{flexDirection: 'row', flexWrap: 'wrap', marginHorizontal: theme.gutter - theme.space.tight, marginTop: theme.space.xs}}>
    {React.Children.map(children, child => <View style={{width: `${100 / columns}%`, padding: theme.space.tight}}>{child}</View>)}
  </View>;
}

export function SavedGridControls({filter, sort, select, list, onList, count}: {
  filter?: Readonly<{anchor: RefObject<View | null>; open: () => void; isOpen: boolean}>;
  sort?: Readonly<{anchor: RefObject<View | null>; open: () => void; isOpen: boolean; label?: string;
    /** The order's field and direction, said as the library says it ('Newest first'). */ detail?: Readonly<{field: string; direction: 'asc' | 'desc'}>}>;
  select?: () => void; list: boolean; onList: (value: boolean) => void; count?: string;
}) {
  // tablet 11: the library toolbar's own parts. Filters and Sort (reading the current order) at the start; the count, grid or
  // list, and Select last (owner, 18 Nov) at the end.
  const theme = useTheme();
  return <View style={{flexDirection: 'row', alignItems: 'center', flexWrap: 'wrap', gap: theme.space.xs,
    marginHorizontal: theme.gutter, marginTop: theme.space.sm}}>
    {filter && <FiltersButton anchorRef={filter.anchor} testID="saved-my-list-filters" open={filter.isOpen} onPress={filter.open}/>}
    {sort && <SortButton anchorRef={sort.anchor} testID="saved-my-list-sort" open={sort.isOpen} onPress={sort.open} label={sort.label}
      detail={sort.detail && t('web.filters.direction', {kind: sortDirectionKind(sort.detail.field), dir: sort.detail.direction})}/>}
    <View style={{flexDirection: 'row', alignItems: 'center', gap: theme.space.xs, marginLeft: 'auto'}}>
      {count && <Text style={[theme.type.captionStrong, {color: theme.colors.textTertiary}]}>{count}</Text>}
      <ViewToggle value={list ? 'list' : 'grid'} onValueChange={value => onList(value === 'list')}
        itemProps={value => ({testID: value === 'list' ? 'saved-my-list-list' : 'saved-my-list-grid'})}/>
      {select && <Chip testID="saved-my-list-select" icon="i-check" onPress={select}>{t('next.selection.select')}</Chip>}
    </View>
  </View>;
}
