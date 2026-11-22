import React, {useCallback, useEffect, useMemo, useRef, useState} from 'react';
import {FlatList, ScrollView, Text, View, type ViewStyle} from 'react-native';
import {generatedCoverTint} from '@portico/design';
import {defaultI18n, calendarDate, calendarMonth} from '../../../../../packages/i18n/src';
import type {ReadingPeriodicalIssue, ReadingYearChoice} from '../../../../../packages/client-core/src';
import {Artwork, AmbientArt, GeneratedCover} from '../artwork';
import {Button, Focusable} from '../controls';
import {Icon} from '../Icon';
import {BottomSheet, Menu} from '../overlays';
import {SkeletonBlock, StateView} from '../states';
import {SelectionCircle} from '../selection';
import {Scrim} from '../cards/primitives';
import {alpha} from '../browse/presentation';
import {textLine, useTheme, type Theme} from '../theme';

import type {NativeShellRuntime} from '../../shell/runtime';
import type {NativeReadPeriodicalState} from '../../screens/read/read-periodical-model';
import {issueCardTitle, periodicalDatePreview, periodicalDayRead, periodicalHeroIssue, readingItemIsRead} from '../../screens/read/periodical-presentation';
import type {IssueOffer, PeriodicalArchiveActions} from '../../pages/shared/read/useReadPeriodical';
import type {ReadingActionsPort} from '../../pages/shared/read/useReadingActions';
import {usePeriodicalActions} from '../../pages/shared/read/usePeriodicalActions';
import type {TitleCollapse} from '../frames/TitleFrame';
import {ReadingShelf} from './ReadingShelf';
import {ReadingSelectionToolbar} from './ReadingActions';
import {GuideProgrammeTitle} from '../live/GuideProgrammeTitle';
import {TVRow} from '../layout/TVRow';

const {t} = defaultI18n;
const captionHeight = (theme: Theme) => (2 * textLine(theme.type.cardTitle) + textLine(theme.type.caption)) * theme.fontScale + theme.space.sm;
const initial = (title: string): string => title.trim().slice(0, 1).toUpperCase() || '·';

export function PeriodicalHero({state, issue, magazine, tablet, runtime, theme, onOpen, offer, actions}: {
  state: NativeReadPeriodicalState; issue?: ReadingPeriodicalIssue; magazine: boolean; tablet: boolean; runtime: NativeShellRuntime; theme: Theme;
  onOpen: (issue: ReadingPeriodicalIssue) => void; offer: IssueOffer; actions: ReadingActionsPort;
}) {
  const coverWidth = tablet ? 190 : 120, coverHeight = coverWidth * 4 / 3;
  const years = state.years.map(value => value.value), range = years.length > 1 ? `${years[0]}–${years[years.length - 1]}` : years[0];
  return <View style={{paddingHorizontal: tablet ? 28 : 20}}>
    <View style={{flexDirection: 'row', alignItems: 'flex-end', gap: tablet ? 28 : 18}}>
      {magazine ? <IssueArtwork issue={issue} title={state.groupName ?? ''} width={coverWidth} height={coverHeight} runtime={runtime} theme={theme}/> : null}
      <View style={{flex: 1, minWidth: 0}}>
        <Text style={[theme.type.eyebrow, {color: theme.colors.textSecondary}]}>{t(magazine ? 'library.tab.magazines' : 'library.tab.newspapers')}</Text>
        <Text accessibilityRole="header" style={[tablet ? theme.type.lockup : theme.type.display, {color: theme.colors.text, marginTop: 4}]}>{state.groupName ?? ''}</Text>
        <View style={{flexDirection: 'row', flexWrap: 'wrap', columnGap: 12, rowGap: 4, marginTop: 6}}>
          {state.issueCount !== undefined ? <Text style={[theme.type.rowHelp, {color: theme.colors.textSecondary}]}>{t('next.reader.issueCount', {count: state.issueCount})}</Text> : null}
          {range ? <Text style={[theme.type.rowHelp, {color: theme.colors.textTertiary}]}>{range}</Text> : null}
        </View>
      </View>
    </View>
    {issue && (magazine || !tablet) ? <PeriodicalActions issue={issue} showRead={magazine} onOpen={onOpen} offer={offer} actions={actions} runtime={runtime}/> : null}
  </View>;
}

export function PeriodicalActions({issue, showRead = true, onOpen, offer, actions, runtime}: {issue: ReadingPeriodicalIssue; showRead?: boolean;
  onOpen: (issue: ReadingPeriodicalIssue) => void; offer: IssueOffer; actions: ReadingActionsPort; runtime: NativeShellRuntime}) {
  const theme = useTheme(), anchor = useRef<View>(null);
  const {target, listed, busy, toggleListed} = usePeriodicalActions(issue, offer(issue, anchor), actions.actions, runtime.reportError);
  return <View style={{marginTop: 16}}>
    <View style={{flexDirection: 'row', alignItems: 'center', gap: 10}}>
      {showRead ? <Button icon="i-book" disabled={issue.item.inventoryAvailable !== true} onPress={() => onOpen(issue)} style={{flex: 1}}>{t('next.reader.read')}</Button> : null}
      {listed ? <Button variant="icon" icon={listed.listed.listed ? 'i-check' : 'i-plus'} iconOnly disabled={busy}
        accessibilityState={{selected: listed.listed.listed}} accessibilityLabel={t(listed.listed.listed ? 'next.home.removeMyList' : 'next.home.addMyList')}
        onPress={toggleListed} style={{width: 46, height: 46}}/> : null}
      {target?.download ? <Button ref={anchor} variant="icon" icon="i-dl" iconOnly accessibilityLabel={t('action.download')}
        onPress={() => {if (target.lifetime.isCurrent()) target.download?.();}} style={{width: 46, height: 46}}/> : null}
    </View>
    {showRead ? <Text style={[theme.type.caption, {color: theme.colors.textTertiary, marginTop: 6}]}>{calendarDate(defaultI18n, issue.issueDate)}</Text> : null}
  </View>;
}

type MagazineRow = Readonly<{key: string; year: string; kind: 'heading' | 'issues'; index: number; items: readonly ReadingPeriodicalIssue[]}>;
export function MagazineIssues({state, archive, theme, runtime, tablet, width, onOpen, offer, actions, hero, bottom, scroll}: {
  state: NativeReadPeriodicalState; archive: PeriodicalArchiveActions; theme: Theme; runtime: NativeShellRuntime; tablet: boolean; width: number;
  onOpen: (issue: ReadingPeriodicalIssue) => void; offer: IssueOffer; actions: ReadingActionsPort; hero: React.ReactNode; bottom?: number;
  /** As a title page's scroller (phone and tablet, phone-read 24): the frame's padding, and its compact bar past `collapse.at`. */
  scroll?: Readonly<{inset: ViewStyle; collapse: TitleCollapse}>;
}) {
  const list = useRef<FlatList<MagazineRow>>(null), [headerHeight, setHeaderHeight] = useState(0), [visibleRows, setVisibleRows] = useState<readonly MagazineRow[]>([]);
  const shelfOffsets = useRef(new Map<string, {offset: number}>()).current;
  const cardWidth = tablet ? 150 : Math.max(1, Math.min(150, (width - 40 - 20) / 3)), artHeight = cardWidth * 4 / 3, rowHeight = artHeight + captionHeight(theme);
  const rows = useMemo(() => state.years.flatMap(year => {
    const section = state.yearSections?.find(value => value.year === year.value), items = section?.items ?? [];
    const rows: MagazineRow[] = [{key: `${year.value}:heading`, year: year.value, kind: 'heading', index: -1, items: []}];
    if (tablet) rows.push({key: `${year.value}:shelf`, year: year.value, kind: 'issues', index: 0, items});
    else {
      for (let index = 0; index < Math.max(1, items.length + (section?.pending && items.length ? 1 : 0)); index += 3)
        rows.push({key: `${year.value}:${index}`, year: year.value, kind: 'issues', index, items: items.slice(index, index + 3)});
    }
    return rows;
  }), [state.years, state.yearSections, tablet]);
  const positions = useMemo(() => {let offset = headerHeight; return rows.map(row => {const length = row.kind === 'heading' ? 64 : rowHeight;
    const position = {length, offset}; offset += length; return position;});}, [rows, rowHeight, headerHeight]);
  const onVisible = useCallback(({viewableItems}: {viewableItems: readonly {item: MagazineRow; isViewable: boolean}[]}) => {
    setVisibleRows(viewableItems.filter(value => value.isViewable && value.item.kind === 'issues').map(value => value.item));
  }, []);
  useEffect(() => {
    // A state publication may occur while two years are visible. Ask from this
    // committed state one at a time; the next render considers the other year.
    for (const row of visibleRows) {
      const choice = state.years.find(year => year.value === row.year), section = state.yearSections?.find(value => value.year === row.year);
      if (!choice || section?.pending || section?.error) continue;
      if (!section || !tablet && section.nextCursor && row.index + row.items.length >= section.items.length - 10) {
        archive.ensureYear(choice, !!section); break;
      }
    }
  }, [archive, state, tablet, visibleRows]);
  const chooseYear = (choice: ReadingYearChoice) => {
    const index = rows.findIndex(row => row.year === choice.value && row.kind === 'heading');
    if (index >= 0) list.current?.scrollToIndex({index, viewOffset: 56});
    archive.selectYear(choice);
  };
  const orderAnchor = useRef<View>(null), [orderMenu, setOrderMenu] = useState(false);
  const chooseOrder = (order: 'newest' | 'oldest') => {setOrderMenu(false); archive.setOrder(order);};
  const header = <View onLayout={event => setHeaderHeight(event.nativeEvent.layout.height)}>{hero}
    <View style={{marginTop: 24, flexDirection: 'row', alignItems: 'center'}}><TVRow><FlatList horizontal style={{flex: 1}} data={state.years} keyExtractor={year => year.value} showsHorizontalScrollIndicator={false}
      contentContainerStyle={{paddingHorizontal: tablet ? 28 : 20, gap: 8}} initialNumToRender={10} windowSize={5}
      getItemLayout={(_data, index) => ({index, length: 72, offset: (tablet ? 28 : 20) + 80 * index})}
      renderItem={({item: year}) => <Focusable accessibilityRole="button" accessibilityLabel={year.value} accessibilityState={{selected: year.value === state.selectedYear}}
        onPress={() => chooseYear(year)} focusKind="chip" focusRadius={9}
        style={{height: tablet ? 44 : 32, width: 72, borderRadius: 9, alignItems: 'center', justifyContent: 'center',
          backgroundColor: year.value === state.selectedYear ? alpha(theme.colors.accent, tablet ? .2 : .28) : alpha(theme.colors.text, .08),
          borderWidth: year.value === state.selectedYear ? 1 : 0, borderColor: tablet ? alpha(theme.colors.accentStrong, .62) : theme.colors.accentStrong}}>
        <Text style={[theme.type.chip, {color: theme.colors.textSecondary}]}>{year.value}</Text>
      </Focusable>}/></TVRow>
      {/* tablet-read 10, phone-read 24: the archive's Order, beside the year chips. */}
      {(tablet || scroll) && state.order ? <Focusable ref={orderAnchor} accessibilityRole="button" accessibilityLabel={`${t('next.overlays.order')}, ${t('next.reader.issueOrder', {order: state.order})}`}
        onPress={() => setOrderMenu(true)} focusKind="chip" focusRadius={9}
        style={{height: 34, marginRight: 28, paddingLeft: 12, paddingRight: 9, borderRadius: 9, flexDirection: 'row', alignItems: 'center', gap: 6, backgroundColor: alpha(theme.colors.text, .08)}}>
        <Text style={[theme.type.rowHelp, {color: theme.colors.textTertiary, marginRight: 4}]}>{t('next.overlays.order')}</Text>
        <Text style={[theme.type.chip, {color: theme.colors.text}]}>{t('next.reader.issueOrder', {order: state.order})}</Text>
        <Icon name="i-down" size={18} color={theme.colors.textTertiary}/>
      </Focusable> : null}
      <Menu open={orderMenu} onOpenChange={setOrderMenu} anchorRef={orderAnchor} title={t('next.overlays.order')} items={(['newest', 'oldest'] as const).map(order => ({
        id: order, label: t('next.reader.issueOrder', {order}), selected: state.order === order, onSelect: () => chooseOrder(order)}))}/></View>
    <ReadingSelectionToolbar port={actions} runtime={runtime}/>
  </View>;
  return <FlatList ref={list} testID="native-read-magazine-years" data={rows} keyExtractor={row => row.key} ListHeaderComponent={header}
    showsVerticalScrollIndicator={false} style={{width: '100%', maxWidth: 1180, alignSelf: 'center'}} contentContainerStyle={scroll ? scroll.inset : {paddingBottom: bottom ?? 0}}
    scrollEventThrottle={scroll ? 32 : undefined} onScroll={scroll ? event => scroll.collapse.onChange(event.nativeEvent.contentOffset.y > scroll.collapse.at) : undefined}
    initialNumToRender={4} maxToRenderPerBatch={6} windowSize={5} removeClippedSubviews={false} onViewableItemsChanged={onVisible}
    maintainVisibleContentPosition={{minIndexForVisible: 0}}
    getItemLayout={(_data, index) => ({index, length: positions[index]?.length ?? rowHeight, offset: positions[index]?.offset ?? headerHeight})}
    renderItem={({item: row}) => {
      const choice = state.years.find(year => year.value === row.year), section = state.yearSections?.find(value => value.year === row.year);
      if (row.kind === 'heading') return <View style={{height: 64, flexDirection: 'row', alignItems: 'center', gap: 10, paddingHorizontal: tablet ? 28 : 20}}>
        <Text accessibilityRole="header" style={[theme.type.rowHeading, {color: theme.colors.text}]}>{row.year}</Text>
        {choice ? <Text style={[theme.type.rowHelp, {color: theme.colors.textTertiary}]}>{t('next.reader.issueCount', {count: choice.count})}</Text> : null}
        {section?.error && choice ? <Button variant="text" size="small" onPress={() => {archive.ensureYear(choice, !!section.items.length);}}>{t('action.tryAgain')}</Button> : null}
      </View>;
      if (!section?.items.length && section?.error && choice) return <View style={{height: rowHeight, paddingHorizontal: tablet ? 28 : 20}}>
        <StateView variant="inline" {...runtime.error(section.error, () => {archive.ensureYear(choice);}, 'library')}/>
      </View>;
      if (section?.phase === 'ready' && !section.items.length && !section.pending) return <View style={{height: rowHeight, paddingHorizontal: tablet ? 28 : 20}}>
        <StateView variant="inline" title={t('library.emptyLibrary')} body={t('library.emptyLibraryBody')}/>
      </View>;
      if (tablet && section?.items.length) {
        let scrollMemory = shelfOffsets.get(row.year);
        if (!scrollMemory) {scrollMemory = {offset: 0}; shelfOffsets.set(row.year, scrollMemory);}
        return <ReadingShelf items={section.items} getKey={issue => issue.item.id} width={cardWidth} height={rowHeight} artHeight={artHeight} scrollMemory={scrollMemory}
        gutter={28} pending={section.pending} onNext={section.nextCursor && !section.error && choice ? () => {archive.ensureYear(choice, true);} : undefined}
        renderItem={issue => <IssueCard issue={issue} width={cardWidth} height={artHeight} theme={theme} runtime={runtime} onOpen={() => onOpen(issue)} offer={offer} actions={actions} byMonth={!!scroll}/>}/>;
      }
      return <View style={{height: rowHeight, flexDirection: 'row', gap: 10, paddingHorizontal: tablet ? 28 : 20}}>
        {row.items.map(issue => <IssueCard key={issue.item.id} issue={issue} width={cardWidth} height={artHeight} theme={theme} runtime={runtime} onOpen={() => onOpen(issue)} offer={offer} actions={actions} byMonth={!!scroll}/>)}
        {!row.items.length || section?.pending && row.index + row.items.length === section.items.length && row.items.length < 3 ? <SkeletonBlock width={cardWidth} height={artHeight} radius={10}/> : null}
      </View>;
    }}/>;
}

export function NewspaperEditions({state, archive, theme, runtime, tablet, width, onOpen, offer, actions}: {
  state: NativeReadPeriodicalState; archive: PeriodicalArchiveActions; theme: Theme; runtime: NativeShellRuntime; tablet: boolean; width: number;
  onOpen: (issue: ReadingPeriodicalIssue) => void; offer: IssueOffer; actions: ReadingActionsPort;
}) {
  const [dateOpen, setDateOpen] = useState(false), previewAnchor = useRef<View>(null);
  const dayStrip = useRef<FlatList<{value: string; count: number}>>(null), dayIndex = state.days.findIndex(day => day.value === state.selectedDate);
  useEffect(() => {if (!tablet && dayIndex >= 0) dayStrip.current?.scrollToIndex({index: dayIndex, animated: false, viewPosition: .5});}, [dayIndex, state.days, tablet]);
  const geometry = state.selectedMonth ? calendarMonth(defaultI18n, state.selectedMonth) : undefined;
  if (!geometry) return null;
  const selectDate = (date: string) => {archive.selectDate(date);};
  const index = state.months.indexOf(state.selectedMonth!), older = state.months[index + 1], newer = index > 0 ? state.months[index - 1] : undefined;
  const selected = periodicalHeroIssue(state, false), selectedItems = state.selectedDateItems ?? state.items.filter(issue => issue.issueDate === state.selectedDate);
  const previewOffer = selected ? offer(selected, previewAnchor) : undefined, previewSelection = previewOffer ? actions.selection(previewOffer) : undefined;
  const expanded = tablet && width >= 900, previewWidth = expanded ? Math.min(400, width * .42) : tablet ? 250 : Math.max(1, width - 32);
  const monthControls = <View style={{flexDirection: 'row', alignItems: 'center', flexWrap: 'wrap', gap: theme.space.xs, paddingHorizontal: theme.gutter, marginBottom: theme.space.sm}}>
    {older ? <Button variant="icon" icon="i-left" iconOnly accessibilityLabel={calendarMonth(defaultI18n, older)?.label ?? older} onPress={() => {archive.selectMonth(older);}}/> : null}
    <Text style={[theme.type.rowHeading, {color: theme.colors.text}]}>{geometry.label}</Text>
    {newer ? <Button variant="icon" icon="i-right" iconOnly accessibilityLabel={calendarMonth(defaultI18n, newer)?.label ?? newer} onPress={() => {archive.selectMonth(newer);}}/> : null}
    <Button variant="text" size="small" icon="i-cal" onPress={() => setDateOpen(true)} style={{marginLeft: 'auto'}}>{t('next.reader.goToDate')}</Button>
  </View>;
  const preview = <View style={{width: expanded ? previewWidth : undefined, alignItems: tablet ? 'center' : 'stretch'}}>
    {selected ? <View style={{width: previewWidth, alignSelf: 'center'}}>
      <Focusable ref={previewAnchor} accessibilityRole="button" accessibilityLabel={selected.item.title}
        accessibilityState={previewSelection?.active ? {selected: previewSelection.selected} : undefined}
        disabled={selected.item.inventoryAvailable !== true} onPress={previewSelection?.active ? previewSelection.onToggle : () => onOpen(selected)}
        onLongPressAction={previewOffer ? () => actions.actions.show(previewOffer) : undefined} focusKind="row"
        style={{height: tablet ? previewWidth * 4 / 3 : 446, borderRadius: 10, overflow: 'hidden', backgroundColor: theme.colors.raised,
          borderWidth: previewSelection?.selected ? 2 : 0, borderColor: theme.colors.accentStrong}}>
        <IssueArtwork issue={selected} title={selected.item.title} width={previewWidth} height={tablet ? previewWidth * 4 / 3 : 446} runtime={runtime} theme={theme} newspaper/>
        {!tablet ? <><View pointerEvents="none" style={{position: 'absolute', left: 0, right: 0, bottom: 0, height: 110}}><Scrim/></View><View style={{position: 'absolute', left: 14, right: 14, bottom: 12, flexDirection: 'row', alignItems: 'flex-end', gap: 10}}>
          <View style={{flex: 1}}><Text style={[theme.type.rowLabel, {color: theme.colors.text}]}>{calendarDate(defaultI18n, selected.issueDate)}</Text></View>
          <Button icon="i-book" size="small" disabled={selected.item.inventoryAvailable !== true} onPress={() => onOpen(selected)}>{t('next.reader.read')}</Button>
        </View></> : null}
        {previewSelection?.active ? <SelectionCircle selected={previewSelection.selected} selecting interactive={false} accessibilityLabel={selected.item.title}
          style={{position: 'absolute', top: 10, right: 10}}/> : null}
      </Focusable>
      {tablet ? <PeriodicalActions issue={selected} onOpen={onOpen} offer={offer} actions={actions} runtime={runtime}/> : null}
    </View> : state.datePending ? <SkeletonBlock width={previewWidth} height={tablet ? previewWidth * 4 / 3 : 446} style={{alignSelf: 'center'}}/> : null}
  </View>;
  return <View style={{marginTop: 24}}>
    {monthControls}
    {!tablet ? <TVRow><FlatList ref={dayStrip} horizontal data={state.days} keyExtractor={day => day.value} showsHorizontalScrollIndicator={false}
      contentContainerStyle={{paddingHorizontal: 16, gap: 6, paddingBottom: 14}}
      // No initialScrollIndex: on a strip shorter than the screen FlatList then never draws the days before it. The effect
      // above brings the chosen day into view instead.
      getItemLayout={(_data, index) => ({index, length: 46, offset: 16 + index * 52})}
      renderItem={({item: day}) => {const chosen = day.value === state.selectedDate, read = periodicalDayRead(state, day.value), weekday = geometry.weekdays[(geometry.offset + geometry.dates.indexOf(day.value)) % 7];
        return <Focusable key={day.value} accessibilityRole="button" accessibilityLabel={`${calendarDate(defaultI18n, day.value)}. ${t('next.reader.issueCount', {count: day.count})}`} accessibilityState={{selected: chosen}}
          focusKind="chip" onPress={() => selectDate(day.value)} style={{width: 46, height: 64, borderRadius: 12, gap: 2, alignItems: 'center', justifyContent: 'center',
            backgroundColor: chosen ? alpha(theme.colors.accent, .28) : theme.colors.raised, borderWidth: chosen ? 1.5 : 0, borderColor: theme.colors.accentStrong}}>
          <Text style={[theme.type.caption, {color: theme.colors.textTertiary}]}>{weekday}</Text>
          <Text style={[theme.type.rowLabel, {color: read ? theme.colors.textTertiary : theme.colors.text}]}>{Number(day.value.slice(8, 10))}</Text>
          {read ? <View accessibilityLabel={t('next.reader.markRead')} style={{position: 'absolute', bottom: 7, width: 5, height: 5, borderRadius: 3, backgroundColor: theme.colors.textTertiary}}/> : null}
        </Focusable>;}}/></TVRow> : null}
    <View style={expanded ? {flexDirection: 'row', alignItems: 'flex-start', gap: 28, paddingHorizontal: 28} : undefined}>
      {preview}
      {tablet ? <View style={{flex: expanded ? 1 : undefined, marginTop: expanded ? 0 : 24, paddingHorizontal: expanded ? 0 : 28}}>
        <PeriodicalCalendar state={state} runtime={runtime} theme={theme} thumbnails onDate={selectDate}/>
      </View> : null}
    </View>
    {selectedItems.length > 1 ? <View style={{marginTop: 20}}><ReadingShelf items={selectedItems} getKey={issue => issue.item.id} width={120} height={160 + captionHeight(theme)} artHeight={160}
      pending={state.datePending} onNext={state.dateCursor && !state.dateError ? () => {archive.nextDate();} : undefined}
      renderItem={issue => <IssueCard issue={issue} width={120} height={160} theme={theme} runtime={runtime} onOpen={() => onOpen(issue)} offer={offer} actions={actions}/>}/></View> : null}
    {state.datePending && !selectedItems.length ? <SkeletonBlock width={120} height={160} style={{marginHorizontal: 20, marginTop: 12}}/> : null}
    {state.dateError ? <StateView {...runtime.error(state.dateError, () => {if (state.selectedDate) (state.dateCursor ? archive.nextDate() : archive.selectDate(state.selectedDate));}, 'library')}/> : null}
    <ReadingSelectionToolbar port={actions} runtime={runtime}/>
    <BottomSheet open={dateOpen} onOpenChange={setDateOpen} title={t('next.reader.goToDate')}>
      {monthControls}
      <View style={{paddingHorizontal: 20}}><PeriodicalCalendar state={state} runtime={runtime} theme={theme} onDate={date => {setDateOpen(false); selectDate(date);}}/></View>
      {state.archivePending ? <SkeletonBlock width={120} height={32} style={{marginHorizontal: 20, marginVertical: 12}}/> : null}
      {state.archiveError ? <StateView {...runtime.error(state.archiveError, () => {archive.moreMonths();}, 'library')}/> : null}
      {state.archiveCursor ? <Button variant="text" disabled={state.archivePending} onPress={() => {archive.moreMonths();}}>{t('action.showMore')}</Button> : null}
    </BottomSheet>
  </View>;
}

function PeriodicalCalendar({state, runtime, theme, thumbnails = false, onDate}: {state: NativeReadPeriodicalState; runtime: NativeShellRuntime; theme: Theme; thumbnails?: boolean; onDate: (date: string) => void}) {
  const [width, setWidth] = useState(0), geometry = state.selectedMonth ? calendarMonth(defaultI18n, state.selectedMonth) : undefined;
  if (!geometry) return null;
  const gap = thumbnails ? 8 : 0, cell = Math.max(1, (width - gap * 6) / 7), artHeight = cell * 4 / 3, height = thumbnails ? artHeight + 28 : 54;
  return <View onLayout={event => setWidth(event.nativeEvent.layout.width)}>
    <View style={{flexDirection: 'row', gap, marginBottom: 6}}>{geometry.weekdays.map((label, index) => <Text key={index} style={[theme.type.caption, {flex: 1, textAlign: 'center', color: theme.colors.textTertiary}]}>{label}</Text>)}</View>
    <View style={{flexDirection: 'row', flexWrap: 'wrap', columnGap: gap, rowGap: thumbnails ? 10 : 0}}>
      {Array.from({length: geometry.offset}, (_, index) => <View key={`pad-${index}`} style={{width: cell, height}}/>)}
      {geometry.dates.map(date => {const count = state.days.find(day => day.value === date)?.count, issue = periodicalDatePreview(state, date);
        if (count === undefined) return <View key={date} style={{width: cell, height}}/>;
        const chosen = date === state.selectedDate, read = periodicalDayRead(state, date);
        return <Focusable key={date} accessibilityRole="button" accessibilityLabel={`${calendarDate(defaultI18n, date)}. ${t('next.reader.issueCount', {count})}`} accessibilityState={{selected: chosen}}
          focusKind="row" focusRadius={8} onPress={() => onDate(date)} style={{width: cell, height}}>
          {thumbnails ? <View style={{borderWidth: chosen ? 3 : 0, borderColor: theme.colors.accentStrong, borderRadius: 5, overflow: 'hidden'}}>
            <View style={{opacity: read ? .5 : .9}}>{issue ? <IssueArtwork issue={issue} title={issue.item.title} width={cell} height={artHeight} runtime={runtime} theme={theme} newspaper/>
              : <SkeletonBlock width={cell} height={artHeight} radius={5}/>}</View>
          </View> : null}
          <View style={{flexDirection: 'row', alignItems: 'center', justifyContent: 'center', gap: 3, flex: thumbnails ? undefined : 1,
            borderRadius: 8, backgroundColor: !thumbnails && chosen ? alpha(theme.colors.accent, .28) : 'transparent'}}>
            <Text style={[theme.type.caption, {color: chosen ? theme.colors.accentStrong : read ? theme.colors.textTertiary : theme.colors.text}]}>{Number(date.slice(8, 10))}</Text>
            {read ? <Icon name="i-check" size={12} color={theme.colors.textTertiary}/> : null}
            {count > 1 ? <Text style={[theme.type.caption, {color: theme.colors.textTertiary}]}>{defaultI18n.number(count)}</Text> : null}
          </View>
        </Focusable>;})}
    </View>
  </View>;
}

/** A magazine's cover for a title page's head (phone-read 24): the selected issue's, or one drawn from its name. */
export function PeriodicalCover({issue, title, width, height, runtime}: {issue?: ReadingPeriodicalIssue; title: string; width: number; height: number; runtime: NativeShellRuntime}) {
  return <IssueArtwork issue={issue} title={title} width={width} height={height} runtime={runtime} theme={useTheme()}/>;
}

function IssueArtwork({issue, title, width, height, runtime, theme, read = false, newspaper = false}: {issue?: ReadingPeriodicalIssue; title: string; width: number; height: number; runtime: NativeShellRuntime; theme: Theme; read?: boolean; newspaper?: boolean}) {
  const art = runtime.artwork(issue?.item.coverUrl);
  return <View style={{width, height, borderRadius: 8, overflow: 'hidden', backgroundColor: theme.colors.raised, opacity: !art && read ? .66 : 1}}>
    {art ? <Artwork source={art} width={width} height={height} saturation={newspaper ? 0 : read ? .6 : 1} brightness={read ? .66 : 1} position={newspaper ? {x: .5, y: 0} : undefined}/>
      : <GeneratedCover title={title} tint={generatedCoverTint(title)} glyph={initial(title)} width={width} height={height}/>}
  </View>;
}

/** One issue: its cover, then its title and date; `byMonth` (a magazine's archive under its year, phone-read 24) titles it
 * by its date alone ("December", "August 27"), the year being the heading above it. */
function IssueCard({issue, width, height, theme, runtime, onOpen, offer, actions, byMonth = false}: {issue: ReadingPeriodicalIssue; width: number; height: number; theme: Theme;
  runtime: NativeShellRuntime; onOpen: () => void; offer: IssueOffer; actions: ReadingActionsPort; byMonth?: boolean}) {
  // N5-4: an issue the server can't serve now is drawn but takes no open, menu or selection (as web).
  const unavailable = issue.item.inventoryAvailable !== true;
  const anchor = useRef<View>(null), target = unavailable ? undefined : offer(issue, anchor), selection = target ? actions.selection(target) : undefined;
  const date = calendarDate(defaultI18n, issue.issueDate), read = readingItemIsRead(issue.item), month = byMonth ? issueCardTitle(issue.issueDate) : undefined;
  // phone-read 24: under its date, the issue's volume and number when its name gives them ("Vol. 4, No. 9").
  const {volume, number} = issue as ReadingPeriodicalIssue & {volume?: number; number?: number};
  const numbered = month && volume && number ? t('next.reader.volumeIssue', {volume, number}) : undefined;
  return <Focusable ref={anchor} accessibilityRole="button" accessibilityLabel={`${issue.item.title}${date ? `. ${date}` : ''}`} focusKind="row" focusRadius={10} disabled={unavailable}
    accessibilityState={selection?.active ? {selected: selection.selected} : unavailable ? {disabled: true} : undefined} onPress={selection?.active ? selection.onToggle : onOpen}
    onLongPressAction={target ? () => actions.actions.show(target) : undefined} style={{width}}>
    <View style={{borderRadius: 10, borderWidth: selection?.selected ? 2 : 0, borderColor: theme.colors.accentStrong}}>
      <IssueArtwork issue={issue} title={issue.item.title} width={width} height={height} runtime={runtime} theme={theme} read={read}/>
      {selection?.active ? <SelectionCircle selected={selection.selected} selecting interactive={false} accessibilityLabel={issue.item.title} style={{position: 'absolute', bottom: 7, right: 7}}/>
        : read ? <View style={{position: 'absolute', bottom: 7, right: 7}}><Icon name="i-check" size={18} color={theme.colors.text}/></View> : null}
    </View>
    <View style={{flexDirection: 'row', marginTop: theme.space.xs, minHeight: 2 * textLine(theme.type.cardTitle) * theme.fontScale}}>
      <GuideProgrammeTitle title={month ?? issue.item.title} lines={2} style={[theme.type.cardTitle, {color: theme.colors.text}]}/>
    </View>
    {date && !month ? <Text style={[theme.type.caption, {color: theme.colors.textTertiary, marginTop: 2}]}>{date}</Text>
      : numbered ? <Text style={[theme.type.caption, {color: theme.colors.textTertiary, marginTop: 2}]}>{numbered}</Text> : null}
  </Focusable>;
}

export function PeriodicalSkeleton({magazine, tablet, width, top}: {magazine: boolean; tablet: boolean; width: number; top: number}) {
  const cover = tablet ? 190 : 120, cell = tablet ? 150 : Math.max(1, Math.min(150, (width - 60) / 3));
  const preview = tablet ? width >= 900 ? Math.min(400, width * .42) : 250 : Math.max(1, width - 32);
  return <View style={{paddingTop: top, paddingHorizontal: tablet ? 28 : 20}}>
    <View style={{flexDirection: 'row', alignItems: 'flex-end', gap: tablet ? 28 : 18}}>
      {magazine ? <SkeletonBlock width={cover} height={cover * 4 / 3}/> : null}
      <View style={{flex: 1, gap: 12}}><SkeletonBlock width="35%" height={13}/><SkeletonBlock width="92%" height={tablet ? 44 : 30}/><SkeletonBlock width="60%" height={14}/></View>
    </View>
    <View style={{flexDirection: 'row', gap: 10, marginTop: 16}}><SkeletonBlock width={180} height={46}/><SkeletonBlock width={46} height={46} radius={23}/><SkeletonBlock width={46} height={46} radius={23}/></View>
    <View style={{flexDirection: 'row', gap: 8, marginTop: 24}}>{Array.from({length: magazine ? 3 : 6}, (_, index) => <SkeletonBlock key={index} width={magazine ? 72 : 46} height={magazine ? tablet ? 44 : 32 : 64} radius={10}/>)}</View>
    {magazine ? <><SkeletonBlock width={80} height={24} style={{marginTop: 24, marginBottom: 12}}/><View style={{flexDirection: 'row', gap: 10}}>{Array.from({length: 3}, (_, index) => <View key={index} style={{gap: 7}}><SkeletonBlock width={cell} height={cell * 4 / 3}/><SkeletonBlock width={cell * .8} height={14}/></View>)}</View></>
      : <SkeletonBlock width={preview} height={tablet ? preview * 4 / 3 : 446} style={{marginTop: 14, alignSelf: 'center'}}/>}
  </View>;
}
