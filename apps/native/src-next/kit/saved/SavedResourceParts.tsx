import React, {useRef, useState, type ReactNode, type RefObject} from 'react';
import {Text, View} from 'react-native';
import {defaultI18n} from '../../../../../packages/i18n/src';
import type {ContentEntry} from '../../../../../packages/client-core/src/library-content';
import type {PersonalResource} from '../../../../../packages/client-core/src/personal-saved';
import type {NativeShellRuntime} from '../../shell/runtime';
import type {NativeSavedEntryActions, SavedResourceRowValue} from '../../pages/shared/saved/useSavedResource';
import type {NativeSavedSelectionPort} from '../../pages/shared/saved/useSavedPage';
import {homeCardText} from '../../screens/home/model';
import {savedDuration} from '../../screens/saved/presentation';
import {CoverMosaic, MediaArt} from '../artwork';
import {CardFrame, Parts} from '../cards/primitives';
import {PosterCard, SquareCard, type CardSelection} from '../cards';
import {cardCaptionHeight} from '../cards/caption-metrics';
import {SelectionCircle} from '../selection';
import {Button, Focusable, Switch} from '../controls';
import {useDockClearance} from '../dock/DockClearance';
import {usePageWidth} from '../layout/page-inset';
import {Banner, ToastHost, type ToastMessage} from '../overlays';
import {SavedTail} from './SavedLoading';
import {savedVisibilityHelp, type SavedVisibilityLine} from '../../screens/saved/presentation';
import {Icon} from '../Icon';
import {alpha} from '../browse/presentation';
import {useTheme} from '../theme';

const t = defaultI18n.t;

function savedResourceColumns(tv: boolean, width: number) {return tv ? 7 : width >= 900 ? 5 : 3;}

export function useSavedResourceLayout(grid: boolean) {
  const theme = useTheme(), viewport = usePageWidth();
  const windowWidth = theme.isTV ? 1920 : viewport;
  const width = Math.max(1, windowWidth - theme.gutter * 2);
  const columns = grid ? savedResourceColumns(theme.isTV, viewport) : 1;
  return {width, columns, windowWidth, gutter: theme.gutter, rowStride: grid ? width / columns * 1.7 : theme.isTV ? 104 : 80};
}

export function SavedResourceHero({artwork, name, eyebrow, summary, condition, visible, runtime, count, duration, visibility}: {
  artwork: readonly string[]; name: string; eyebrow?: string; summary?: string; condition?: string;
  visible: boolean; runtime: NativeShellRuntime; count?: string; duration?: number; visibility?: SavedVisibilityLine;
}) {
  const theme = useTheme(), size = theme.isTV ? 280 : 152;
  const covers = artwork.slice(0, 4).map(path => runtime.artwork(path)).filter((cover): cover is NonNullable<typeof cover> => !!cover);
  return <View style={theme.isTV
    ? {flexDirection: 'row', alignItems: 'flex-end', gap: theme.space.xl, paddingHorizontal: theme.gutter, paddingTop: theme.space.sm}
    : {alignItems: 'center', paddingHorizontal: theme.gutter, paddingTop: theme.space.xl}}>
    <View style={{width: size, height: size, borderRadius: theme.isTV ? 16 : 14, overflow: 'hidden', backgroundColor: theme.colors.raised}}>
      <CoverMosaic covers={covers} width={size} height={size} shape="square" visible={visible}/>
    </View>
    <View style={theme.isTV ? {flex: 1, minWidth: 0} : {alignItems: 'center', alignSelf: 'stretch', marginTop: theme.space.sm}}>
      {!!eyebrow && <Text style={[theme.type.caption, {color: theme.colors.accentStrong}]}>{eyebrow}</Text>}
      <Text accessibilityRole="header" style={[theme.type.pageTitle, {color: theme.colors.text, textAlign: theme.isTV ? 'left' : 'center'}]}>{name}</Text>
      <View style={{flexDirection: 'row', flexWrap: 'wrap', alignItems: 'center', gap: theme.space.sm, marginTop: theme.space.xs,
        justifyContent: theme.isTV ? 'flex-start' : 'center'}}>
        {!!count && <Text style={[theme.type.caption, {color: theme.colors.textSecondary}]}>{count}</Text>}
        {!!duration && <Text style={[theme.type.caption, {color: theme.colors.textSecondary}]}>{savedDuration(duration)}</Text>}
        {visibility && <View style={{flexDirection: 'row', alignItems: 'center', gap: theme.space.xs}}>
          <Icon name={visibility.icon} size={theme.isTV ? 22 : 13} color={visibility.strong ? theme.colors.accentStrong : theme.colors.textTertiary}/>
          <Text style={[theme.type.caption, {color: visibility.strong ? theme.colors.accentStrong : theme.colors.textTertiary}]}>{visibility.text}</Text>
        </View>}
      </View>
      {!!summary && <Text style={[theme.type.body, {color: theme.colors.textSecondary, marginTop: theme.space.xs,
        textAlign: theme.isTV ? 'left' : 'center'}]}>{summary}</Text>}
      {!!condition && <Text style={[theme.type.caption, {color: theme.colors.textSecondary, borderRadius: 14,
        backgroundColor: theme.colors.raised, marginTop: theme.space.sm, paddingHorizontal: theme.space.sm,
        paddingVertical: theme.space.xs}]}>{condition}</Text>}
    </View>
  </View>;
}

export function SavedResourceBand({children}: {children: ReactNode}) {
  const theme = useTheme();
  return <View style={{paddingHorizontal: theme.gutter, paddingTop: theme.space.sm, flexDirection: 'row',
    flexWrap: 'wrap', alignItems: 'center', gap: theme.space.sm}}>{children}</View>;
}

export function viewSortLabel(resource: PersonalResource): string {
  const sort = resource.definition?.sort?.[0];
  if (!sort) return t('web.saved.sort.other');
  return t(sortFieldId(sort.field));
}

function sortFieldId(field: string): 'web.saved.sort.title' | 'web.saved.sort.added' | 'web.saved.sort.recent' | 'web.saved.sort.year' | 'web.saved.sort.duration' | 'web.saved.sort.progress' | 'web.saved.sort.other' {
  if (field === 'title' || field === 'sort_title') return 'web.saved.sort.title';
  if (field === 'added' || field === 'added_at') return 'web.saved.sort.added';
  if (field === 'last_played' || field === 'recent') return 'web.saved.sort.recent';
  if (field === 'year') return 'web.saved.sort.year';
  if (field === 'duration') return 'web.saved.sort.duration';
  if (field === 'progress') return 'web.saved.sort.progress';
  return 'web.saved.sort.other';
}

/** A saved view is a browse result, so its body is the library grid rather than
 * the playlist row list (collections.html 13a and 21). */
export function SavedResourceViewGrid({entries, runtime, actions, selection, columns, windowWidth, gutter}: {
  entries: readonly ContentEntry[]; runtime: NativeShellRuntime; actions: NativeSavedEntryActions; selection?: NativeSavedSelectionPort; columns: number; windowWidth: number; gutter: number;
}) {
  const theme = useTheme(), [caption, setCaption] = useState(0);
  const gap = theme.space.sm, width = Math.max(1, (windowWidth - gutter * 2 - gap * (columns - 1)) / columns);
  const rowHeight = width * 1.5 + Math.max(caption, cardCaptionHeight(theme, 2));
  return <View style={{flexDirection: 'row', flexWrap: 'wrap', gap, paddingHorizontal: gutter}}>{entries.map(entry =>
    <View key={entry.id} style={{minHeight: rowHeight}}>
      <SavedResourceCard entry={entry} runtime={runtime} actions={actions} selection={selection?.item(entry)} width={width}
        onCaptionLayout={height => setCaption(current => Math.max(current, height))}/>
    </View>)}</View>;
}
function SavedResourceCard({entry, runtime, actions, selection, width, onCaptionLayout}: {
  entry: ContentEntry; runtime: NativeShellRuntime; actions: NativeSavedEntryActions; selection?: CardSelection; width: number; onCaptionLayout: (height: number) => void;
}) {
  const anchor = useRef<View>(null), text = homeCardText(entry, false, runtime.text), square = ['album', 'song', 'book', 'audiobook_file'].includes(entry.kind);
  const progress = entry.duration && entry.progressSeconds ? Math.min(1, entry.progressSeconds / entry.duration) : undefined;
  const props = {testID: `saved-view-entry-${encodeURIComponent(entry.id)}`, focusId: `saved-view-entry-${entry.id}`, title: entry.displayTitle || entry.title,
    caption: text.caption, titleLines: 2 as const, onCaptionLayout, density: 'grid' as const, artwork: runtime.artwork(entry.posterUrl), progress, watched: entry.watched, width, menuAnchorRef: anchor, selection,
    onNavigate: () => actions.open(entry), onPlay: entry.playback ? () => actions.play(entry) : undefined, onMore: () => actions.menu(entry, anchor, selection?.onEnter),
    moreExpanded: actions.isMenuOpen?.(entry)};
  return square ? <SquareCard {...props}/> : <PosterCard {...props}/>;
}

/** The frame's list row: art with its progress line, the title, what it is, the
 * library and its time left, and the row's own menu. */
export function SavedResourceRow({row, width, runtime, actions, selection, library, onMore}: {
  row: SavedResourceRowValue; width: number; runtime: NativeShellRuntime; actions: NativeSavedEntryActions; selection?: CardSelection; library?: string; onMore: (anchor?: RefObject<View | null>) => void;
}) {
  const theme = useTheme(), anchor = useRef<View>(null), entry = row.media, square = ['album', 'song', 'book', 'audiobook_file'].includes(entry.kind);
  const text = homeCardText(entry, false, runtime.text);
  const watched = entry.watched === true, progress = entry.duration && entry.progressSeconds ? Math.min(1, entry.progressSeconds / entry.duration) : undefined;
  const remaining = entry.duration && entry.progressSeconds && !watched ? savedDuration(Math.max(0, entry.duration - entry.progressSeconds)) : undefined;
  return <CardFrame testID={`saved-row-${encodeURIComponent(row.id)}`} focusId={`saved-row-${row.id}`} title={entry.title} width={width} menuAnchorRef={anchor} selection={selection} onNavigate={() => actions.open(entry)}
    onPlay={entry.playback ? () => actions.play(entry) : undefined} onMore={() => onMore(anchor)} moreExpanded={actions.isMenuOpen?.(entry)}
    pressableStyle={{minHeight: theme.isTV ? 104 : 80, borderRadius: theme.isTV ? 16 : 10, justifyContent: 'center'}}>
    {() => <View style={{minHeight: theme.isTV ? 104 : 80, flexDirection: 'row', alignItems: 'center', gap: theme.isTV ? 28 : 12, paddingHorizontal: theme.isTV ? 20 : 4, paddingVertical: theme.space.xs}}>
      {selection?.active && <SelectionCircle selected={selection.selected} selecting accessibilityLabel={t('next.selection.select')} interactive={false} variant="row"/>}
      <View style={{width: theme.isTV ? 76 : 48, height: theme.isTV ? 76 : square ? 56 : 72, alignItems: 'center', justifyContent: 'center'}}>
        <MediaArt kind={square ? 'square' : 'poster'} source={runtime.artwork(entry.posterUrl)} width={theme.isTV ? (square ? 76 : 51) : square ? 56 : 48}
          height={theme.isTV ? 76 : square ? 56 : 72} style={{borderRadius: theme.isTV ? 7 : 6}}/>
        {progress !== undefined && <View style={{position: 'absolute', left: theme.isTV ? 15 : 4, right: theme.isTV ? 15 : 4, bottom: theme.isTV ? 0 : 4, height: theme.isTV ? 5 : 3,
          borderRadius: 5, backgroundColor: alpha(theme.colors.ink, .7), overflow: 'hidden'}}><View style={{height: '100%', width: `${progress * 100}%`, backgroundColor: theme.colors.accentStrong}}/></View>}
      </View>
      <View style={{flex: 1, minWidth: 0, gap: 3}}>
        <Text style={[theme.type.rowLabel, {color: theme.colors.text}]}>{entry.displayTitle || entry.title}</Text>
        <Parts fit="wrap" parts={text.caption} style={[theme.type.rowHelp, {color: theme.colors.textTertiary}]}/>
        {(watched || remaining) && <Text style={[theme.type.rowHelp, {color: watched ? theme.colors.textTertiary : theme.colors.accentStrong}]}>
          {watched ? t('saved.watched') : remaining}
        </Text>}
      </View>
      {theme.isTV && <Text style={[theme.type.rowHelp, {color: theme.colors.textTertiary, width: 360}]}>{library ?? ''}</Text>}
      {!theme.isTV && library && <Text style={[theme.type.rowHelp, {color: theme.colors.textTertiary, maxWidth: 96, textAlign: 'right'}]}>{library}</Text>}
      <Focusable testID={`saved-row-more-${encodeURIComponent(row.id)}`} focusId={`saved-row-more-${row.id}`} accessibilityRole="button" accessibilityLabel={t('card.more', {title: entry.title})}
        onPress={() => onMore(anchor)} style={{width: theme.isTV ? 56 : 40, height: theme.isTV ? 56 : 40, alignItems: 'center', justifyContent: 'center'}}>
        <Icon name="i-more" size={theme.isTV ? 30 : 20} color={theme.colors.textTertiary}/>
      </Focusable>
      <View pointerEvents="none" style={{position: 'absolute', left: theme.isTV ? 20 : 20, right: theme.isTV ? 20 : 4, bottom: 0, height: 1, backgroundColor: alpha(theme.colors.line, theme.isTV ? .55 : .3)}}/>
    </View>}
  </CardFrame>;
}

export function SavedResourceVisibility({resource, disabled, onChange}: {resource: PersonalResource; disabled?: boolean; onChange: (visible: boolean) => void}) {
  const theme = useTheme();
  return <View style={{paddingHorizontal: theme.gutter, paddingTop: theme.space.sm, gap: theme.space.xs}}>
    <Switch focusId="saved-visibility" checked={resource.visibility === 'server'} disabled={disabled} onCheckedChange={onChange}>{t('web.saved.visibleToAll')}</Switch>
    <Text style={[theme.type.caption, {color: theme.colors.textTertiary}]}>{savedVisibilityHelp(resource)}</Text>
  </View>;
}

export function SavedResourceAction({shape, ...props}: React.ComponentProps<typeof Button> & {shape: 'primary' | 'round' | 'more'}) {
  const theme = useTheme();
  const style = theme.isTV
    ? shape === 'more' ? {width: 72, height: 72, borderRadius: 14} : undefined
    : shape === 'primary' ? {height: 48, minWidth: 92, flex: 1} : {width: 48, height: 48, borderRadius: 24};
  return <Button {...props} style={style}/>;
}

export function SavedResourceCaption({children, trailing = false}: {children: ReactNode; trailing?: boolean}) {
  const theme = useTheme();
  return <Text style={[trailing ? theme.type.caption : theme.type.rowLabel, {color: trailing ? theme.colors.textTertiary : theme.colors.text,
    ...(trailing ? {marginLeft: 'auto'} : {})}]}>{children}</Text>;
}

export function SavedResourceMenuFacts({values}: {values: readonly (string | undefined)[]}) {
  const theme = useTheme();
  return <View style={{flexDirection: 'row', flexWrap: 'wrap', gap: theme.space.xs}}>
    {values.filter((part): part is string => !!part).map((part, index) =>
      <Text key={index} style={[theme.type.caption, {color: theme.colors.textTertiary}]}>{part}</Text>)}
  </View>;
}

export function SavedResourceViewInfo({resource}: {resource?: PersonalResource | null}) {
  const theme = useTheme();
  return <View style={{gap: theme.space.sm}}>
    {resource && <Text style={[theme.type.rowLabel, {color: theme.colors.text}]}>{viewSortLabel(resource)}</Text>}
    {!!resource?.summary && <Text style={[theme.type.body, {color: theme.colors.textSecondary}]}>{resource.summary}</Text>}
    <Text style={[theme.type.caption, {color: theme.colors.textTertiary}]}>{t('next.saved.viewsHelp')}</Text>
  </View>;
}

export function SavedResourceInventory({children, tail}: {children: ReactNode; tail?: React.ComponentProps<typeof SavedTail>}) {
  const theme = useTheme();
  return <View style={{marginTop: theme.isTV ? theme.space.md : theme.space.xs}}>
    {children}
    {tail && <View style={{paddingHorizontal: theme.gutter, paddingVertical: theme.space.sm}}><SavedTail {...tail}/></View>}
  </View>;
}

export function SavedResourceHoldHint() {
  const theme = useTheme();
  return <View style={{flexDirection: 'row', alignItems: 'center', gap: theme.space.sm, paddingHorizontal: theme.gutter, paddingTop: theme.space.lg}}>
    <Icon name="i-hold" size={22} color={theme.colors.textTertiary}/>
    <Text style={[theme.type.rowHelp, {color: theme.colors.textTertiary, flexShrink: 1}]}>{t('next.saved.native.holdRowActions')}</Text>
  </View>;
}

export function SavedResourceUndo({toast, onDismiss}: {toast: ToastMessage | null; onDismiss: () => void}) {
  const theme = useTheme(), dockTop = useDockClearance();
  if (!theme.isTV) return <ToastHost toast={toast} onDismiss={onDismiss} bottomOffset={0} obstructionTop={dockTop}/>;
  return toast?.action && <View style={{position: 'absolute', left: theme.gutter, right: theme.gutter, bottom: 80}} pointerEvents="box-none">
    <Banner variant="notice" tone="info" placement="overlay" message={toast.message} action={{label: toast.action.label, onPress: toast.action.onPress}} visible/>
  </View>;
}
