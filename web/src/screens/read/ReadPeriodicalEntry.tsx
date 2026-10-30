import {ReadGroupSavedAction} from './ReadGroupSavedAction';
import {useEffect, useRef, useState, useSyncExternalStore, type ReactNode, useLayoutEffect} from 'react';
import {calendarDate, defaultI18n} from '@i18n';
import type {ReadingPeriodicalType, ReadingPeriodicalIssue, ReadingYearChoice, ReadingDayChoice} from '@portico/client-core';
import {useProtectedWebScreenContext} from '../../route-registry-view';
import {router} from '../../router';
import {CoverCard} from '../../kit/cards';
import {Icon} from '../../kit/Icon';
import {Select, TextField} from '../../kit/controls';
import {WindowedGrid} from '../../kit/layout';
import {Skeleton, StateView} from '../../kit/states';
import {useShellWidth} from '../../shell/viewport';
import {createReadBindings} from './bindings';
import {createReadingPeriodicalGroupsCollection, createReadingPeriodicalIssuesCollection} from './library-collection';
import {readChosenDestination} from './navigation';
import {NewspaperCalendar} from './NewspaperCalendar';
import styles from './entry.module.css';

const {t} = defaultI18n;
type Captured = NonNullable<ReturnType<typeof useProtectedWebScreenContext>>;
type Common = Readonly<{context: Captured['context']; bindings: ReturnType<typeof createReadBindings>; libraryId: string; kind: ReadingPeriodicalType; issueDate?: string; year?: string; month?: string; reportError: (error: unknown) => void}>;
type Groups = Common & Readonly<{mode: 'groups'; pages: ReturnType<typeof createReadingPeriodicalGroupsCollection>}>;
type Issues = Common & Readonly<{mode: 'issues'; seriesId: string; pages: ReturnType<typeof createReadingPeriodicalIssuesCollection>}>;
type Resources = Groups | Issues;

/** Only an actual server-offered type/series reaches this renderer. No dates or
 * group identifiers are derived from labels, loaded cards or folder strings. */
export function ReadPeriodicalEntry({captured: provided, libraryId, kind, seriesId}: {captured?: Captured; libraryId: string; kind: ReadingPeriodicalType; seriesId?: string}) {
  const original = useProtectedWebScreenContext(), captured = provided ?? original;
  return captured ? <BoundPeriodicals key={JSON.stringify([libraryId, kind, seriesId])} captured={captured} libraryId={libraryId} kind={kind} seriesId={seriesId}/> : null;
}
function BoundPeriodicals({captured, libraryId, kind, seriesId}: {captured: Captured; libraryId: string; kind: ReadingPeriodicalType; seriesId?: string}) {
  const [issueDate, setIssueDate] = useState(''), [year, setYear] = useState(''), [month, setMonth] = useState('');
  const [dayFact, setDayFact] = useState<Readonly<{context: Captured['context']; month: string; choices: readonly ReadingDayChoice[]}>>();
  const [yearFact, setYearFact] = useState<Readonly<{context: Captured['context']; choices: readonly ReadingYearChoice[]}>>();
  const [resources, setResources] = useState<Resources>(), [retry, setRetry] = useState(0), [retired, setRetired] = useState(false);
  // The opening reports through this commit's shell binding; a later binding identity
  // must not replay the committed periodical cursor.
  const shellEffects = useRef(captured.binding.home.effects);
  useLayoutEffect(() => {shellEffects.current = captured.binding.home.effects;});
  useEffect(() => {
    let active = true;
    const effects = shellEffects.current;
    const bindings = createReadBindings(captured.context);
    const common: Common = {context: captured.context, bindings, libraryId, kind, issueDate: issueDate || undefined, year: year || undefined, month: month || undefined, reportError: error => {if (bindings.authority.isCurrent()) effects.reportError(error);}};
    const next: Resources = seriesId ? {...common, mode: 'issues', seriesId, pages: createReadingPeriodicalIssuesCollection(bindings.client, libraryId, seriesId, kind, issueDate || undefined, year || undefined, choices => {if (active && bindings.authority.isCurrent()) setYearFact({context: captured.context, choices});}, month || undefined, (actualMonth, choices, latestDate) => {
        if (!active || !bindings.authority.isCurrent()) return;
        if (actualMonth) setDayFact({context: captured.context, month: actualMonth, choices});
        else if (kind === 'newspaper' && !month && latestDate) setMonth(latestDate.slice(0, 7));
      })}
      : {...common, mode: 'groups', pages: createReadingPeriodicalGroupsCollection(bindings.client, libraryId, kind)};
    setRetired(false); setResources(next);
    const off = bindings.authority.subscribe(() => {setRetired(true); setResources(undefined);});
    return () => {active = false; off(); next.pages.dispose(); void bindings.dispose().catch(() => {});};
  }, [captured.context, libraryId, kind, seriesId, retry, issueDate, year, month]);
  const onRetry = () => setRetry(value => value + 1);
  const content = retired ? <Failure retry={onRetry}/>
    : !resources || resources.context !== captured.context || resources.issueDate !== (issueDate || undefined) || resources.year !== (year || undefined) || resources.month !== (month || undefined) ? <Skeleton quiet height={320}/>
    : resources.mode === 'groups' ? <GroupRows resources={resources} retry={onRetry}/> : <IssueRows resources={resources} retry={onRetry}/>;
  const yearChoices = yearFact?.context === captured.context ? yearFact.choices : [];
  const days = dayFact?.context === captured.context && dayFact.month === month ? dayFact.choices : undefined;
  return <>{kind === 'magazine' && seriesId && yearChoices.length > 0 && <div className={styles.dateFilter}>
    <Select label={t('next.reader.year')} value={year || 'all'} options={[{value: 'all', label: t('next.reader.allYears')}, ...yearChoices.map(choice => ({value: choice.value, label: choice.value, description: t('next.reader.issueCount', {count: choice.count})}))]}
      onValueChange={value => {if (captured.context.isCurrent() && (value === 'all' || yearChoices.some(choice => choice.value === value))) {setIssueDate(''); setYear(value === 'all' ? '' : value);}}}/>
  </div>}{kind === 'newspaper' && seriesId && <div className={styles.dateFilter}>
    <TextField label={t('next.reader.month')} type="month" value={month} onChange={event => {if (captured.context.isCurrent()) {setIssueDate(''); setMonth(event.currentTarget.value);}}}/>
    <TextField label={t('next.reader.goToDate')} type="date" value={issueDate} onChange={event => {if (captured.context.isCurrent()) {const date = event.currentTarget.value; setIssueDate(date); if (date) setMonth(date.slice(0, 7));}}}/>
  </div>}{kind === 'newspaper' && seriesId && days && !retired && <NewspaperCalendar month={month} days={days} selected={issueDate} onSelect={date => {if (captured.context.isCurrent() && days.some(day => day.value === date)) setIssueDate(date);}}/>}{content}</>;
}
function Failure({retry}: {retry: () => void}) {
  return <StateView variant="page" title={t('next.states.pageFailed')} body={t('next.states.pageFailedBody')} primary={{id: 'retry', label: t('next.states.tryAgain'), onPress: retry}}/>;
}
function GridFrame({children}: {children: (height: number, width: number) => ReactNode}) {
  const width = useShellWidth(), grid = useRef<HTMLDivElement>(null), [height, setHeight] = useState(600);
  useEffect(() => {
    const element = grid.current; if (!element) return;
    const measure = () => setHeight(Math.max(280, window.innerHeight - element.getBoundingClientRect().top));
    const observer = new ResizeObserver(measure); observer.observe(element); window.addEventListener('resize', measure); measure();
    return () => {observer.disconnect(); window.removeEventListener('resize', measure);};
  }, []);
  return <div ref={grid} className={styles.books}>{children(height, width < 600 ? 108 : 190)}</div>;
}
function GroupRows({resources, retry}: {resources: Groups; retry: () => void}) {
  const state = useSyncExternalStore(resources.pages.collection.subscribe, resources.pages.collection.getSnapshot, resources.pages.collection.getSnapshot);
  if (state.status === 'error') return <Failure retry={retry}/>;
  if (state.status === 'ready' && state.total === 0) return <StateView title={t('title.nothingHere')}/>;
  return <section className={styles.library}><GridFrame>{(height, width) => <WindowedGrid collection={resources.pages.collection} label={t(resources.kind === 'magazine' ? 'library.tab.magazines' : 'library.tab.newspapers')}
    height={height} cardWidth={width} rowHeight={width * 4 / 3 + 66} gap={20} rowGap={30} renderPlaceholder={() => <Skeleton width={width} height={width * 4 / 3}/>}
    renderItem={group => <CoverCard kind={resources.kind} context="grid" density="grid" visible
      artwork={resources.context.artwork.foreground(group.latest.item.coverUrl, 380, 3 / 4)} fallback={<Icon name="i-book" size={40}/>}
      model={{title: group.name, caption: [{id: 'date', text: calendarDate(defaultI18n, group.latest.issueDate), role: 'identity'}, {id: 'issues', text: t('next.reader.issueCount', {count: group.issueCount}), role: 'identity'}]}}
      playLabel={t('next.reader.read')} onPlay={group.latest.item.inventoryAvailable ? () => {
        if (resources.bindings.authority.isCurrent()) void router.navigate(readChosenDestination(group.latest.item.id)).catch(resources.reportError);
      } : undefined}
      onNavigate={() => {if (resources.bindings.authority.isCurrent()) void router.navigate({to: '/library/$libraryId', params: {libraryId: resources.libraryId}, search: {view: 'series', pivot: resources.kind === 'magazine' ? 'magazines' : 'newspapers', entityId: group.id}}).catch(resources.reportError);}}/>}/>}</GridFrame></section>;
}
function IssueRows({resources, retry}: {resources: Issues; retry: () => void}) {
  const state = useSyncExternalStore(resources.pages.collection.subscribe, resources.pages.collection.getSnapshot, resources.pages.collection.getSnapshot);
  const name = resources.pages.collection.residentItems()[0]?.item.series?.name;
  if (state.status === 'error') return <Failure retry={retry}/>;
  if (state.status === 'ready' && state.total === 0) return <StateView title={t('title.nothingHere')}/>;
  const open = (issue: ReadingPeriodicalIssue, read: boolean) => {
    if (!resources.bindings.authority.isCurrent()) return;
    void router.navigate(read ? readChosenDestination(issue.item.id) : {to: '/item/$itemId', params: {itemId: issue.item.id}}).catch(resources.reportError);
  };
  return <section className={styles.library}>{name && <h2>{name}</h2>}{state.status === 'ready' && <ReadGroupSavedAction client={resources.bindings.client} libraryId={resources.libraryId} groupKind={resources.kind} seriesId={resources.seriesId}
    isCurrent={() => resources.bindings.authority.isCurrent() && resources.pages.collection.getSnapshot().status === 'ready'} reportError={resources.reportError}/>}<GridFrame>{(height, width) => <WindowedGrid collection={resources.pages.collection} label={name ?? t(resources.kind === 'magazine' ? 'library.tab.magazines' : 'library.tab.newspapers')}
    height={height} cardWidth={width} rowHeight={width * 4 / 3 + 66} gap={20} rowGap={30} renderPlaceholder={() => <Skeleton width={width} height={width * 4 / 3}/>}
    renderItem={issue => <CoverCard kind={resources.kind} context="grid" density="grid" visible
      artwork={resources.context.artwork.foreground(issue.item.coverUrl, 380, 3 / 4)} fallback={<Icon name="i-book" size={40}/>}
      model={{title: issue.item.title, caption: [{id: 'date', text: calendarDate(defaultI18n, issue.issueDate), role: 'identity'}]}}
      disabled={!issue.item.inventoryAvailable} playLabel={t('next.reader.read')} onNavigate={() => open(issue, false)} onPlay={issue.item.inventoryAvailable ? () => open(issue, true) : undefined}/>}/>}</GridFrame></section>;
}
