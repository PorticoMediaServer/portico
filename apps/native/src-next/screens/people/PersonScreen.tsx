import React, {useCallback, useEffect, useMemo, useRef, useState, useSyncExternalStore} from 'react';
import {ScrollView, Text, View, VirtualizedList, useWindowDimensions, type ViewToken} from 'react-native';
import {useSafeAreaInsets} from 'react-native-safe-area-context';
import type {WindowedCollection} from '../../../../../packages/client-core/src/collections/windowed';
import type {PersonCredit, PersonPage, PersonRole} from '../../../../../packages/client-core/src/people';
import {defaultI18n} from '../../../../../packages/i18n/src';
import {AmbientArt, Artwork, PersonCircle, StandardBackdrop} from '../../kit/artwork';
import type {NativeSelection} from '../selection/native-selection';
import {PosterCard, type PosterCardProps} from '../../kit/cards';
import {Button, Chip, Focusable} from '../../kit/controls';
import {shareLinkActions} from '../share/share-actions';
import {Menu, type MenuEntry} from '../../kit/overlays';
import {TVShelf} from '../../tv/layout/TVShelf';
import {Icon} from '../../kit/Icon';
import {StateView} from '../../kit/states';
import {browseType} from '../../kit/browse/presentation';
import {useTheme} from '../../kit/theme';
import type {NativeShellRuntime} from '../../shell/runtime';
import type {createNativeEntryActions} from '../destinations/entry-actions';
import type {ContentEntry} from '../../../../../packages/client-core/src/library-content';

export type NativePersonScreenProps = Readonly<{
  focusScope: string; page: PersonPage; role: PersonRole; collection: WindowedCollection<PersonCredit>;
  runtime: NativeShellRuntime; actions: ReturnType<typeof createNativeEntryActions>; selection?: NativeSelection;
  onRole: (role: PersonRole) => void; onBack: () => void;
}>;
const t = defaultI18n.t;
const knownShelfKey = (entry: {kind: string; id: string}) => `${entry.kind}:${entry.id}`;
const creditSlotKey = (index: number) => String(index);

/** phone-detail frame7: one sparse credits scroller, with the genuine person,
 * biography and bounded known-for shelf as its header. */
export function PersonScreen({focusScope, page, role, collection, runtime, actions, selection, onRole, onBack}: NativePersonScreenProps) {
  const selectionRevision = useSyncExternalStore(selection?.subscribe ?? (() => () => {}), selection?.getSnapshot ?? (() => 0));
  useSyncExternalStore(actions.menuState.subscribe, actions.menuState.getSnapshot);
  const theme = useTheme(), dimensions = useWindowDimensions(), insets = useSafeAreaInsets();
  const columns = theme.platform === 'phone' ? 3 : Math.max(3, Math.floor((dimensions.width - 40) / 150));
  const width = (dimensions.width - 32 - (columns - 1) * 10) / columns;
  const [range, setRange] = useState({first: 0, last: columns * 4 - 1});
  const [expanded, setExpanded] = useState(false), [sharing, setSharing] = useState(false), shareAnchor = useRef<View>(null);
  const firstPage = collection.pageOf(range.first), lastPage = collection.pageOf(range.last);
  const subscribePages = useCallback((listener: () => void) => {
    const stops = [collection.subscribe(listener)];
    for (let index = firstPage; index <= lastPage; index++) stops.push(collection.subscribePage(index, listener));
    return () => stops.forEach(stop => stop());
  }, [collection, firstPage, lastPage]);
  const version = useCallback(() => {
    const state = collection.getSnapshot();
    const values = [`${state.generation}:${state.total}:${state.status}:${state.refreshing}`];
    for (let index = firstPage; index <= lastPage; index++) values.push(collection.getPageVersion(index));
    return values.join('|');
  }, [collection, firstPage, lastPage]);
  useSyncExternalStore(subscribePages, version);
  const state = collection.getSnapshot();
  useEffect(() => {collection.ensureRange(range.first, range.last);}, [collection, range]);
  const columnsRef = useRef(columns); columnsRef.current = columns;
  const visible = useRef(({viewableItems}: {viewableItems: ViewToken<number>[]}) => {
    const indices = viewableItems.flatMap(item => item.index === null ? [] : [item.index]);
    if (!indices.length) return;
    setRange({first: Math.min(...indices) * columnsRef.current, last: (Math.max(...indices) + 1) * columnsRef.current - 1});
  }).current;
  const person = page.person, born = person.birthDate ? defaultI18n.date(person.birthDate, 'long') : undefined;
  const died = person.deathDate ? defaultI18n.date(person.deathDate, 'long') : undefined;
  const dates = born && died ? t('web.person.lived', {born, died}) : born ? t('web.person.born', {date: born}) : undefined;
  const initials = person.name.split(/\s+/u).filter(Boolean).slice(0, 2).map(word => [...word][0]).join('');
  const known = page.person.knownFor;
  // O-162: Share and Copy link for this person (the page had no ⋯ while no person action existed).
  const shareItems: MenuEntry[] = shareLinkActions(runtime.getSnapshot().authority.serverId, 'person', {personId: person.id},
    {notice: runtime.notice, report: runtime.reportError}).map(action => ({id: action.id, label: action.label, icon: action.icon, onSelect: action.onSelect}));
  const header = <>
    <View pointerEvents="none" style={{position: 'absolute', top: 0, left: 0, right: 0, height: 560, overflow: 'hidden'}}>
      <AmbientArt source={runtime.ambient(known.find(entry => entry.backdropUrl || entry.posterUrl)?.backdropUrl ?? known.find(entry => entry.posterUrl)?.posterUrl)} treatment="library" height={560}/>
    </View>
    {shareItems.length > 0 && <View style={{position: 'absolute', top: insets.top + 4, right: 8, zIndex: 1}}>
      <Button ref={shareAnchor} testID={`${focusScope}.share`} variant="icon" icon="i-more" iconOnly size="compact" accessibilityLabel={t('next.overlays.share')} onPress={() => setSharing(true)}/>
      <Menu testID={`${focusScope}.share-menu`} open={sharing} onOpenChange={setSharing} anchorRef={shareAnchor} title={person.name} items={shareItems}/>
    </View>}
    <View style={{paddingTop: Math.max(98, insets.top + 44), paddingHorizontal: 20, alignItems: 'center'}}>
      <PersonCircle size={96} initials={initials} source={runtime.artwork(person.portraitUrl)} covers={known.flatMap(entry => {const art = runtime.artwork(entry.posterUrl); return art ? [art] : [];})}/>
      <Text accessibilityRole="header" style={[browseType(theme, 30, 800, -.035), {color: theme.colors.text, marginTop: 12, marginBottom: 4}]}>{person.name}</Text>
      <View style={{flexDirection: 'row', flexWrap: 'wrap', justifyContent: 'center', columnGap: 12, rowGap: 6}}>
        {!!person.roles.length && <Text style={[browseType(theme, 13, 600), {color: theme.colors.textSecondary}]}>{defaultI18n.list(person.roles)}</Text>}
        {dates && <Text style={[browseType(theme, 13, 400), {color: theme.colors.textSecondary}]}>{dates}</Text>}
      </View>
    </View>
    {!!person.biography && <View style={{marginTop: 12, marginHorizontal: 20}}>
      <Text numberOfLines={expanded ? undefined : 2} style={[browseType(theme, 14, 400), {lineHeight: 21, color: theme.colors.textSecondary}]}>{person.biography}</Text>
      <Focusable testID="person-biography-expand" onPress={() => setExpanded(value => !value)} accessibilityRole="button" accessibilityState={{expanded}}>
        <Text style={[browseType(theme, 14, 700), {color: theme.colors.text}]}>{t(expanded ? 'web.person.showLess' : 'web.person.readMore')}</Text>
      </Focusable>
    </View>}
    {!!known.length && <View style={{marginTop: 16}}>
      <Text accessibilityRole="header" style={[browseType(theme, 19, 800), {color: theme.colors.text, marginHorizontal: 20, marginBottom: 14}]}>{t('web.person.knownFor')}</Text>
      <ScrollView horizontal showsHorizontalScrollIndicator={false} contentContainerStyle={{paddingHorizontal: 20, paddingBottom: 4, gap: 12}}>
        {known.map(entry => <PersonEntryCard entry={entry} group="known" actions={actions} selection={selection} key={`${entry.kind}:${entry.id}`} testID={`person-known-${entry.id}`} title={entry.displayTitle || entry.title} width={96} artwork={runtime.artwork(entry.posterUrl)}
          caption={entry.year === undefined ? [] : [{id: 'year', text: String(entry.year), role: 'context'}]} />)}
      </ScrollView>
    </View>}
    <View style={{flexDirection: 'row', alignItems: 'baseline', justifyContent: 'space-between', marginTop: 12, marginHorizontal: 20, marginBottom: 10}}>
      <Text accessibilityRole="header" style={[browseType(theme, 18, 700, -.015), {color: theme.colors.text}]}>{t('web.person.credits')}</Text>
      <Text style={[browseType(theme, 14, 600), {color: theme.colors.textTertiary}]}>{t('web.person.titles', {count: page.pageInfo.total})}</Text>
    </View>
    <View style={{flexDirection: 'row', gap: 8, marginHorizontal: 20, marginBottom: 12}}>
      {theme.platform !== 'phone' && selection?.start && <Chip testID="person-select" icon="i-check" onPress={() => selection.start?.()}>{t('next.selection.select')}</Chip>}
      {(['all', 'cast', 'crew'] as const).map(value => <Chip key={value} testID={`person-role-${value}`} selected={role === value} onPress={() => onRole(value)}>{t(value === 'all' ? 'title.creditsAll' : value === 'cast' ? 'web.person.cast' : 'web.person.crew')}</Chip>)}
    </View>
  </>;
  const rows = useMemo(() => Math.ceil((state.total ?? page.pageInfo.total) / columns), [columns, page.pageInfo.total, state.total]);
  if (theme.isTV) return <TVPerson focusScope={focusScope} page={page} role={role} collection={collection} runtime={runtime} actions={actions} selection={selection} onRole={onRole} onBack={onBack}/>;
  return <View style={{flex: 1}} testID="native-person"><StandardBackdrop/>
    <VirtualizedList data={collection} getItem={(_data, index) => index} getItemCount={() => rows} keyExtractor={index => String(index)} extraData={`${version()}:${selectionRevision}`}
      initialNumToRender={4} maxToRenderPerBatch={4} windowSize={5} ListHeaderComponent={header} onViewableItemsChanged={visible}
      contentContainerStyle={{paddingBottom: insets.bottom + 24}} renderItem={({item: row}: {item: number}) => <View style={{flexDirection: 'row', gap: 10, paddingHorizontal: 16, paddingBottom: 16}}>
        {Array.from({length: columns}, (_, column) => {
          const index = row * columns + column;
          if (index >= (state.total ?? page.pageInfo.total)) return null;
          const credit = collection.itemAt(index), entry = credit?.media;
          return entry && credit ? <PersonEntryCard entry={entry} group="credits" actions={actions} selection={selection} key={index} testID={`person-credit-${index}`} density="grid" title={entry.displayTitle || entry.title} width={width} artwork={runtime.artwork(entry.posterUrl)}
            caption={credit.character ? [{id: 'character', text: t('web.person.as', {character: credit.character}), role: 'context'}] : credit.role ? [{id: 'role', text: credit.role, role: 'context'}] : []}
            /> : <View key={index} testID={`person-credit-placeholder-${index}`} style={{width, height: width * 1.5 + 44, borderRadius: 10, backgroundColor: theme.colors.raised}}/>;
        })}
      </View>}
      ListEmptyComponent={<StateView title={t('web.person.noCredits')} body={t(role === 'cast' ? 'web.person.noCast' : role === 'crew' ? 'web.person.noCrew' : 'web.person.noCreditsBody')}/>}
      ListFooterComponent={state.status === 'error' ? <StateView {...runtime.error(state.error, () => collection.retry())}/> : null}/>
    {selection?.render(path => <Artwork source={runtime.artwork(path)} width={40} height={40}/>)}
    <Focusable testID="person-back" accessibilityLabel={t('action.back')} onPress={onBack} focusRadius={20}
      style={{position: 'absolute', left: 16, top: insets.top + 12, width: 40, height: 40, alignItems: 'center', justifyContent: 'center'}}><Icon name="i-left" size={22} color={theme.colors.text}/></Focusable>
  </View>;
}

/** tv-detail frame 10: the ten-foot person page. One blur mosaic circle,
 * the person's own facts and biography, then the real windowed credits as a
 * horizontal row; Sort is not produced by the person read, so only the real
 * All/Cast/Crew filter is drawn. */
export function TVPerson({focusScope, page, role, collection, runtime, actions, selection, onRole, onBack}: {
  focusScope: string; page: PersonPage; role: PersonRole; collection: WindowedCollection<PersonCredit>;
  runtime: NativeShellRuntime; actions: ReturnType<typeof createNativeEntryActions>; selection?: NativeSelection;
  onRole: (role: PersonRole) => void; onBack: () => void;
}) {
  const theme = useTheme(), t = defaultI18n.t;
  useSyncExternalStore(selection?.subscribe ?? (() => () => {}), selection?.getSnapshot ?? (() => 0));
  useSyncExternalStore(actions.menuState?.subscribe ?? (() => () => {}), actions.menuState?.getSnapshot ?? (() => 0));
  const [expanded, setExpanded] = useState(false), [filters, setFilters] = useState(false), [range, setRange] = useState({first: 0, last: 19});
  const firstPage = collection.pageOf(range.first), lastPage = collection.pageOf(range.last);
  const subscribePages = useCallback((listener: () => void) => {
    const stops = [collection.subscribe(listener)];
    for (let index = firstPage; index <= lastPage; index++) stops.push(collection.subscribePage(index, listener));
    return () => stops.forEach(stop => stop());
  }, [collection, firstPage, lastPage]);
  const version = useCallback(() => {
    const state = collection.getSnapshot(), values = [`${state.generation}:${state.total}:${state.status}:${state.refreshing}`];
    for (let index = firstPage; index <= lastPage; index++) values.push(collection.getPageVersion(index));
    return values.join('|');
  }, [collection, firstPage, lastPage]);
  useSyncExternalStore(subscribePages, version);
  useEffect(() => {collection.ensureRange(range.first, range.last);}, [collection, range]);
  useEffect(() => runtime.registerTVPageBack?.(() => {onBack(); return true;}), [runtime, onBack]);
  const state = collection.getSnapshot(), total = state.total ?? page.pageInfo.total;
  const person = page.person, born = person.birthDate ? defaultI18n.date(person.birthDate, 'long') : undefined;
  const died = person.deathDate ? defaultI18n.date(person.deathDate, 'long') : undefined;
  const dates = born && died ? t('web.person.lived', {born, died}) : born ? t('web.person.born', {date: born}) : undefined;
  const initials = person.name.split(/\s+/u).filter(Boolean).slice(0, 2).map(word => [...word][0]).join('');
  const known = page.person.knownFor;
  const slots = useMemo(() => Array.from({length: total}, (_, index) => index), [total]);
  const filterItems: MenuEntry[] = ([['all', 'title.creditsAll'], ['cast', 'web.person.cast'], ['crew', 'web.person.crew']] as const).map(([value, label]) =>
    ({id: value, label: t(label), selected: role === value, onSelect: () => onRole(value)}));
  return <View testID="native-person" style={{flex: 1}}><StandardBackdrop/>
    <View pointerEvents="none" style={{position: 'absolute', inset: 0, overflow: 'hidden'}}><AmbientArt source={runtime.ambient(known[0]?.backdropUrl || person.portraitUrl || known[0]?.posterUrl)} treatment="person"/></View>
    <ScrollView contentContainerStyle={{paddingLeft: 200, paddingRight: 96, paddingTop: 92, paddingBottom: 80}}>
      <View style={{flexDirection: 'row', alignItems: 'flex-start', gap: 60, minHeight: 284}}>
        <View style={{marginTop: 4}}><PersonCircle size={280} initials={initials} source={runtime.artwork(person.portraitUrl)}
          covers={known.flatMap(entry => {const art = runtime.artwork(entry.posterUrl); return art ? [art] : [];})}/></View>
        <View style={{width: 1180, minWidth: 0}}>
          {person.roles.length > 0 && <Text style={[browseType(theme, 24, 600), {color: theme.colors.accentStrong, marginBottom: 16}]}>{defaultI18n.list(person.roles)}</Text>}
          <Text accessibilityRole="header" numberOfLines={2} style={[browseType(theme, 96, 800, -.035, 94.08), {color: theme.colors.text, marginBottom: 16}]}>{person.name}</Text>
          <View style={{flexDirection: 'row', flexWrap: 'wrap', alignItems: 'baseline', columnGap: 28, rowGap: 10, marginBottom: 20}}>
            {dates && <Text style={[browseType(theme, 24, 700), {color: theme.colors.text}]}>{dates}</Text>}
            {person.birthPlace && <Text style={[browseType(theme, 24), {color: theme.colors.textSecondary}]}>{person.birthPlace}</Text>}
            <Text style={[browseType(theme, 24, 500), {color: theme.colors.textSecondary}]}>{t('web.person.titles', {count: total})}</Text>
          </View>
          {person.biography && <Focusable testID="person-biography" onPress={() => setExpanded(value => !value)} accessibilityRole="button"
            accessibilityState={{expanded}} focusKind="row" focusRadius={10} style={{maxWidth: 1060}}>
            <Text numberOfLines={expanded ? undefined : 3} style={[browseType(theme, 24, 400, 0, 34.8), {color: theme.colors.textSecondary}]}>{person.biography}</Text>
          </Focusable>}
        </View>
      </View>
      {!!known.length && <View style={{marginTop: 48}}>
        <Text accessibilityRole="header" style={[browseType(theme, 38, 800, -.02), {color: theme.colors.text, marginBottom: 22}]}>{t('web.person.knownFor')}</Text>
        <TVShelf items={known} getKey={knownShelfKey} itemWidth={214} itemHeight={420}
          renderItem={(entry, slot) => <PersonEntryCard entry={entry} group="known" actions={actions} selection={selection} testID={`person-known-${entry.id}`} title={entry.displayTitle || entry.title} width={214}
            onFocus={slot.onFocus} onBlur={slot.onBlur} hasTVPreferredFocus={slot.index === 0} artwork={runtime.artwork(entry.posterUrl)} caption={entry.year === undefined ? [] : [{id: 'year', text: String(entry.year), role: 'context'}]}/>}/>
      </View>}
      <View style={{flexDirection: 'row', alignItems: 'center', gap: 18, marginTop: 52, marginBottom: 24}}>
        <Text accessibilityRole="header" style={[browseType(theme, 38, 800, -.02), {color: theme.colors.text}]}>{t('web.person.credits')}</Text>
        <Text style={[browseType(theme, 24, 600), {color: theme.colors.textTertiary}]}>{t('web.person.titles', {count: total})}</Text>
        <View style={{flex: 1}}/>
        <Chip testID="person-role-filter" selected={role !== 'all'} onPress={() => setFilters(true)}>{t('next.browse.native.filters')}</Chip>
      </View>
      <TVShelf items={slots} getKey={creditSlotKey} itemWidth={214} itemHeight={440}
        onVisibleRange={(first, last) => setRange(current => current.first === first && current.last === last ? current : {first, last})}
        renderItem={(index, slot) => {
          const credit = collection.itemAt(index), entry = credit?.media;
          if (!entry) return <View testID={`person-credit-placeholder-${index}`} style={{width: 214, height: 321, borderRadius: 10, backgroundColor: theme.colors.raised}}/>;
          return <PersonEntryCard entry={entry} group="credits" actions={actions} selection={selection} testID={`person-credit-${index}`} title={entry.displayTitle || entry.title} width={214}
            onFocus={slot.onFocus} onBlur={slot.onBlur} hasTVPreferredFocus={index === 0} artwork={runtime.artwork(entry.posterUrl)} watched={entry.watched} progress={entry.duration && entry.progressSeconds ? entry.progressSeconds / entry.duration : undefined}
            caption={[...(entry.year === undefined ? [] : [{id: 'year', text: String(entry.year), role: 'context' as const}]),
              ...(credit.character ? [{id: 'character', text: t('web.person.as', {character: credit.character}), role: 'context' as const}] : [])]}/>;
        }}/>
      {state.status === 'error' && <View style={{marginTop: 30, width: 900}}><StateView {...runtime.error(state.error, () => collection.retry())}/></View>}
    </ScrollView>
    {selection?.render(path => <Artwork source={runtime.artwork(path)} width={40} height={40}/>)}
    <Menu testID={`${focusScope}.role-filter`} open={filters} onOpenChange={setFilters} title={t('next.browse.native.filters')} items={filterItems}/>
  </View>;
}

function PersonEntryCard({entry, group, actions, selection, ...props}: Omit<PosterCardProps, 'primaryAction' | 'onPlay' | 'onNavigate' | 'onMore' | 'selection'> & {
  entry: ContentEntry; group: 'known' | 'credits'; actions: ReturnType<typeof createNativeEntryActions>; selection?: NativeSelection;
}) {
  const anchor = useRef<View>(null), pick = selection?.item(entry, group);
  return <PosterCard {...props} menuAnchorRef={anchor} moreExpanded={actions.isMenuOpen?.(entry)} selection={pick}
    onNavigate={() => actions.open(entry)} onMore={() => actions.menu(entry, anchor, pick?.onEnter)}/>;
}
