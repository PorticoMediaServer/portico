import {ReadingError, type LocalSession, type ReadingClient, type ReadingDayChoice, type ReadingPeriodicalIssue, type ReadingPeriodicalOrder, type ReadingPeriodicalType, type ReadingYearChoice} from '../../../../../packages/client-core/src';
import type {NativeShellBootstrapContext} from '../../shell/bootstrap';
import type {NativeShellRoute} from '../../shell/routes';
import {sameAuthority, type ShellAuthority} from '../../shell/model';
import {nativeReadOrigin, openNativeReadBook, type NativeReadOrigin} from './eligibility';

export type NativeReadPeriodicalYear = Readonly<{
  year: string;
  phase: 'loading' | 'ready' | 'error';
  items: readonly ReadingPeriodicalIssue[];
  nextCursor?: string;
  pending?: boolean;
  error?: unknown;
}>;

export type NativeReadPeriodicalState = Readonly<{
  phase: 'idle' | 'loading' | 'ready' | 'error';
  /** The server's ordered issues for the selected year or month. */
  items: readonly ReadingPeriodicalIssue[];
  /** The group's latest issue from the first, unfiltered page. */
  latest?: ReadingPeriodicalIssue;
  years: readonly ReadingYearChoice[];
  days: readonly ReadingDayChoice[];
  /** Months that actually hold issues in the unfiltered first page, newest first. */
  months: readonly string[];
  groupName?: string;
  /** Only a complete bucket sum or the group's own count; otherwise absent. */
  issueCount?: number;
  publicationType?: ReadingPeriodicalType;
  selectedYear?: string;
  /** Magazine issue order across the whole archive (SUP-017): years and the
   * issues within each year run newest first or oldest first. */
  order?: ReadingPeriodicalOrder;
  selectedMonth?: string;
  selectedDate?: string;
  /** Each magazine year owns its own cursor; only visible sections are loaded. */
  yearSections?: readonly NativeReadPeriodicalYear[];
  /** An exact day query, independent of the month's thumbnail window. */
  selectedDateItems?: readonly ReadingPeriodicalIssue[];
  dateCursor?: string;
  datePending?: boolean;
  dateError?: unknown;
  pending?: boolean;
  archivePending?: boolean;
  archiveError?: unknown;
  /** The selected query's own next page, if the server published one. */
  nextCursor?: string;
  /** The unfiltered archive's own next page for discovering older months. */
  archiveCursor?: string;
  error?: unknown;
}>;
type PeriodicalRoute = Extract<NativeShellRoute, {kind: 'read-periodical'}>;

const refused = () => Object.assign(new Error('native_read_periodical_refused'), {name: 'AbortError', code: 'cancelled'});
const cancelled = (error: unknown): boolean => error instanceof ReadingError && error.code === 'cancelled' && error.status === undefined;
const monthsOf = (items: readonly ReadingPeriodicalIssue[]): readonly string[] => [...new Set(items.map(entry => entry.issueDate.slice(0, 7)))];

/** A magazine or newspaper group from its signed periodical producer. Year and
 * month choices, days and issues are the server's own projections; the page
 * never builds an empty calendar or a year range of its own. */
export class NativeReadPeriodical {
  private readonly context: NativeShellBootstrapContext;
  private readonly libraryId: string;
  private readonly groupId: string;
  private readonly publicationType: ReadingPeriodicalType;
  private readonly route: PeriodicalRoute;
  private origin?: NativeReadOrigin;
  private captured?: Readonly<{sessionFamilyId: string; viewer: LocalSession['viewer']}>;
  private authority?: ShellAuthority;
  private life?: AbortController;
  private client?: ReadingClient;
  private state: NativeReadPeriodicalState = Object.freeze({phase: 'idle', items: [], years: [], days: [], months: []});
  private readonly listeners = new Set<() => void>();
  private flight = 0;
  private command?: object;
  private readonly yearTickets = new Map<string, object>();
  private disposed = false;
  private order: ReadingPeriodicalOrder = 'newest';
  /** A magazine's year queries carry the chosen order; newspapers keep their calendar. */
  private ordered = () => this.publicationType === 'magazine' ? {order: this.order} : {};
  private orderedYears = (years: readonly ReadingYearChoice[]) => this.publicationType === 'magazine' && this.order === 'oldest' ? [...years].reverse() : years;

  constructor(context: NativeShellBootstrapContext, route: PeriodicalRoute) {
    this.context = context; this.libraryId = route.libraryId; this.groupId = route.groupId;
    this.publicationType = route.publicationType; this.route = route;
  }
  arm = (): boolean => {
    if (this.disposed) return false;
    const selected = this.context.session(), origin = nativeReadOrigin(this.context, this.route);
    if (!selected || !origin) return false;
    this.retire();
    this.origin = origin;
    this.authority = this.context.authority();
    this.captured = Object.freeze({sessionFamilyId: selected.sessionFamilyId, viewer: Object.freeze({...selected.viewer})});
    this.state = Object.freeze({phase: 'idle', items: [], years: [], days: [], months: []});
    this.emit();
    return true;
  };
  release = (): void => {
    this.retire();
    this.origin = undefined; this.captured = undefined; this.authority = undefined;
  };
  private retire(): void {
    this.flight++;
    this.yearTickets.clear();
    this.life?.abort();
    this.client?.dispose();
    this.client = undefined; this.life = undefined; this.command = undefined;
  }
  private current = (): boolean => {
    const origin = this.origin, captured = this.captured, selected = this.context.session();
    if (this.disposed || !origin || !captured || !this.authority || !selected || !this.context.current()) return false;
    if (selected.sessionFamilyId !== captured.sessionFamilyId || !sameAuthority(this.authority, this.context.authority())) return false;
    const snapshot = this.context.navigation.getSnapshot();
    return snapshot.route === origin.route && snapshot.revision === origin.revision;
  };
  private reading(): ReadingClient {
    const captured = this.captured;
    if (!captured || !this.current()) throw refused();
    this.life ??= new AbortController();
    return this.client ??= this.context.api.reading({viewer: captured.viewer, isCurrent: this.current, signal: this.life.signal});
  }
  private publish(next: NativeReadPeriodicalState): void {
    if (this.disposed || !this.current()) return;
    this.state = Object.freeze(next);
    this.emit();
  }
  private emit(): void {for (const listener of [...this.listeners]) listener();}
  subscribe = (listener: () => void): (() => void) => {
    if (this.disposed) return () => {};
    this.listeners.add(listener);
    return () => {this.listeners.delete(listener);};
  };
  getSnapshot = (): NativeReadPeriodicalState => this.state;
  load = async (): Promise<void> => {
    if (!this.current()) return;
    const flight = ++this.flight;
    this.yearTickets.clear();
    // H14: a reload of the same periodical keeps its painted issues until the new reading lands.
    this.publish({...this.state, phase: 'loading', error: undefined});
    try {
      const client = this.reading(), signal = this.life!.signal;
      // The unfiltered first page gives the real latest issue, the year buckets
      // and the months that actually hold issues. The displayed issues always
      // come from a query on the chosen year or month, never from this page.
      const unfiltered = await client.periodicalIssues(this.libraryId, this.groupId, this.publicationType, {limit: 60}, signal);
      if (flight !== this.flight || !this.current()) return;
      const latest = unfiltered.items[0];
      const months = monthsOf(unfiltered.items);
      const groupName = latest?.item.series?.name;
      // The raw wire years are {year, issueCount}; only the client's canonical
      // yearChoices are the validated {value, count} projection.
      const buckets = unfiltered.yearChoices;
      const issueCount = buckets.length ? buckets.reduce((total, year) => total + year.count, 0) : undefined;
      const selectedYear = this.publicationType === 'magazine' ? this.orderedYears(buckets)[0]?.value : undefined;
      const selectedMonth = this.publicationType === 'newspaper' ? months[0] : undefined;
      const page = selectedYear ? await client.periodicalIssues(this.libraryId, this.groupId, this.publicationType, {year: selectedYear, ...this.ordered(), limit: 60}, signal)
        : selectedMonth ? await client.periodicalIssues(this.libraryId, this.groupId, this.publicationType, {month: selectedMonth, limit: 60}, signal)
          : unfiltered;
      if (flight !== this.flight || !this.current()) return;
      this.publish({phase: 'ready', items: page.items, ...(latest ? {latest} : {}), years: this.orderedYears(page.yearChoices), days: page.dayChoices,
        months, ...(groupName ? {groupName} : {}), ...(issueCount === undefined ? {} : {issueCount}), publicationType: this.publicationType, ...(this.publicationType === 'magazine' ? {order: this.order} : {}),
        ...(selectedYear ? {selectedYear, yearSections: [{year: selectedYear, phase: 'ready' as const, items: page.items, nextCursor: page.nextCursor}]} : {}),
        ...(selectedMonth ? {selectedMonth, selectedDate: latest?.issueDate ?? page.items[0]?.issueDate} : {}),
        ...(page.nextCursor ? {nextCursor: page.nextCursor} : {}),
        ...(unfiltered.nextCursor ? {archiveCursor: unfiltered.nextCursor} : {})});
    } catch (error) {
      if (flight !== this.flight || !this.current() || cancelled(error)) return;
      this.publish({phase: 'error', items: [], years: [], days: [], months: [], error});
    }
  };
  retry = (): Promise<void> => this.load();
  /** A new order is a new archive walk: every year section reloads in that order. */
  setOrder = async (rendered: NativeReadPeriodicalState, order: ReadingPeriodicalOrder): Promise<void> => {
    if (!this.current() || this.state !== rendered || rendered.phase !== 'ready' || this.publicationType !== 'magazine' || order === this.order
      || order !== 'newest' && order !== 'oldest') return;
    this.order = order;
    await this.load();
  };
  /** Selecting a chip moves to its year section; it does not replace the other
   * sections. Each section is read lazily from its own server-issued cursor. */
  selectYear = async (rendered: NativeReadPeriodicalState, choice: ReadingYearChoice): Promise<void> => {
    if (!this.current() || this.state !== rendered || rendered.phase !== 'ready' || this.publicationType !== 'magazine'
      || !rendered.years.includes(choice)) return;
    const section = rendered.yearSections?.find(value => value.year === choice.value);
    this.publish({...rendered, selectedYear: choice.value, ...(section ? {items: section.items, nextCursor: section.nextCursor} : {})});
    await this.ensureYear(this.state, choice);
  };
  ensureYear = async (rendered: NativeReadPeriodicalState, choice: ReadingYearChoice, more = false): Promise<void> => {
    if (!this.current() || this.state !== rendered || rendered.phase !== 'ready' || this.publicationType !== 'magazine'
      || !rendered.years.includes(choice) || this.yearTickets.has(choice.value)) return;
    const existing = rendered.yearSections?.find(section => section.year === choice.value);
    if (existing?.phase === 'ready' && !more || more && !existing?.nextCursor) return;
    const life = this.life, flight = this.flight, ticket = {}, cursor = more ? existing?.nextCursor : undefined;
    if (!life) return;
    const pending: NativeReadPeriodicalYear = Object.freeze({year: choice.value, phase: existing?.items.length ? 'ready' : 'loading',
      items: existing?.items ?? [], nextCursor: cursor, pending: true});
    this.yearTickets.set(choice.value, ticket);
    this.publish({...rendered, yearSections: [...(rendered.yearSections ?? []).filter(section => section.year !== choice.value), pending]});
    const current = () => this.life === life && this.flight === flight && this.yearTickets.get(choice.value) === ticket && this.current()
      && this.state.yearSections?.find(section => section.year === choice.value) === pending;
    try {
      const page = await this.reading().periodicalIssues(this.libraryId, this.groupId, this.publicationType, {year: choice.value, ...this.ordered(), ...(cursor ? {cursor} : {}), limit: 60}, life.signal);
      if (!current()) return;
      const seen = new Set(pending.items.map(issue => issue.item.id));
      if (page.items.some(issue => seen.has(issue.item.id)) || cursor && page.nextCursor === cursor) throw new ReadingError('invalid_document');
      const items = Object.freeze([...pending.items, ...page.items]);
      const section: NativeReadPeriodicalYear = Object.freeze({year: choice.value, phase: 'ready', items, nextCursor: page.nextCursor});
      this.publish({...this.state, yearSections: this.state.yearSections!.map(value => value === pending ? section : value),
        ...(this.state.selectedYear === choice.value ? {items, nextCursor: page.nextCursor} : {})});
    } catch (error) {
      if (!current() || cancelled(error)) return;
      const section: NativeReadPeriodicalYear = Object.freeze({...pending, pending: false, phase: pending.items.length ? 'ready' : 'error', error});
      this.publish({...this.state, yearSections: this.state.yearSections!.map(value => value === pending ? section : value)});
    } finally {if (this.yearTickets.get(choice.value) === ticket) this.yearTickets.delete(choice.value);}
  };
  /** Load one real month of a newspaper's editions and its actual day counts. */
  selectMonth = async (rendered: NativeReadPeriodicalState, month: string): Promise<void> => {
    if (this.command || this.publicationType !== 'newspaper' || !this.current() || this.state !== rendered || rendered.phase !== 'ready'
      || !rendered.months.includes(month)) return;
    const life = this.life, flight = ++this.flight, ticket = {};
    if (!life) return;
    this.command = ticket;
    try {
      const page = await this.reading().periodicalIssues(this.libraryId, this.groupId, this.publicationType, {month, limit: 60}, life.signal);
      if (this.life !== life || this.command !== ticket || flight !== this.flight || this.state !== rendered || !this.current()) return;
      this.publish({...rendered, phase: 'ready', items: page.items, years: page.yearChoices, days: page.dayChoices,
        selectedMonth: month, selectedDate: page.items[0]?.issueDate, selectedDateItems: undefined, dateCursor: undefined, datePending: false, dateError: undefined, error: undefined,
        ...(page.nextCursor ? {nextCursor: page.nextCursor} : {nextCursor: undefined})});
    } catch (error) {
      if (this.life === life && this.command === ticket && flight === this.flight && this.state === rendered && this.current() && !cancelled(error))
        this.publish({...rendered, phase: 'error', error});
    } finally {if (this.command === ticket) this.command = undefined;}
  };
  /** A date remains a real server choice even when its issue is beyond the
   * month's first page. Resolve that day directly, never fall back to latest. */
  selectDate = async (rendered: NativeReadPeriodicalState, date: string): Promise<void> => {
    if (!this.current() || this.state !== rendered || rendered.phase !== 'ready' || this.publicationType !== 'newspaper') return;
    const choice = rendered.days.find(day => day.value === date);
    if (!choice) return;
    if (this.command) {
      if (!rendered.datePending || rendered.selectedDate === date) return;
      // A newer chosen day supersedes only the older day lookup. Its late
      // settlement cannot overwrite the current selection or release its claim.
      this.command = undefined; this.flight++;
    }
    const items = rendered.items.filter(issue => issue.issueDate === date);
    if (items.length === choice.count) {this.publish({...rendered, selectedDate: date, selectedDateItems: items, dateCursor: undefined, datePending: false, dateError: undefined}); return;}
    await this.requestDate(rendered, date);
  };
  nextDate = async (rendered: NativeReadPeriodicalState): Promise<void> => {
    if (rendered.selectedDate && rendered.dateCursor) await this.requestDate(rendered, rendered.selectedDate, rendered.dateCursor);
  };
  private requestDate = async (rendered: NativeReadPeriodicalState, date: string, cursor?: string): Promise<void> => {
    const life = this.life, ticket = {}, flight = this.flight;
    if (this.command || !life || !this.current() || this.state !== rendered || rendered.phase !== 'ready') return;
    this.command = ticket;
    const pending = Object.freeze({...rendered, selectedDate: date, selectedDateItems: cursor ? rendered.selectedDateItems : [], dateCursor: cursor, datePending: true, dateError: undefined});
    this.publish(pending);
    const current = () => this.life === life && this.flight === flight && this.command === ticket && this.state === pending && this.current();
    try {
      const page = await this.reading().periodicalIssues(this.libraryId, this.groupId, this.publicationType,
        {month: rendered.selectedMonth, issueDate: date, ...(cursor ? {cursor} : {}), limit: 60}, life.signal);
      if (!current()) return;
      const seen = new Set(pending.selectedDateItems?.map(issue => issue.item.id));
      if (page.items.some(issue => seen.has(issue.item.id)) || cursor && page.nextCursor === cursor) throw new ReadingError('invalid_document');
      this.publish({...pending, selectedDateItems: [...(pending.selectedDateItems ?? []), ...page.items], dateCursor: page.nextCursor, datePending: false});
    } catch (error) {
      if (current() && !cancelled(error)) this.publish({...pending, datePending: false, dateError: error});
    } finally {if (this.command === ticket) this.command = undefined;}
  };
  /** The selected query's real next page; only the exact rendered state and its
   * own cursor append, and a newer flight retires this one. */
  next = async (rendered: NativeReadPeriodicalState): Promise<void> => {
    const cursor = rendered.nextCursor;
    if (this.command || !cursor || !this.current() || this.state !== rendered || rendered.phase !== 'ready') return;
    const life = this.life, flight = ++this.flight, ticket = {};
    if (!life) return;
    this.command = ticket;
    const pending = Object.freeze({...rendered, pending: true, error: undefined});
    this.publish(pending);
    try {
      const query = rendered.selectedYear ? {year: rendered.selectedYear, ...this.ordered()} : rendered.selectedMonth ? {month: rendered.selectedMonth} : {};
      const page = await this.reading().periodicalIssues(this.libraryId, this.groupId, this.publicationType, {...query, cursor, limit: 60}, life.signal);
      if (this.life !== life || this.command !== ticket || flight !== this.flight || this.state !== pending || !this.current() || this.state.nextCursor !== cursor) return;
      const seen = new Set(rendered.items.map(issue => issue.item.id));
      if (page.nextCursor === cursor || page.items.some(issue => seen.has(issue.item.id))) throw new ReadingError('invalid_document');
      this.publish({...rendered, pending: false, items: [...rendered.items, ...page.items], years: this.orderedYears(page.yearChoices), days: page.dayChoices,
        ...(page.nextCursor ? {nextCursor: page.nextCursor} : {nextCursor: undefined})});
    } catch (error) {
      if (this.life === life && this.command === ticket && flight === this.flight && this.state === pending && this.current() && !cancelled(error))
        this.publish({...rendered, pending: false, error});
    } finally {if (this.command === ticket) this.command = undefined;}
  };
  /** Discover older newspaper months from the unfiltered archive's own cursor;
   * one real page per call, appending only the months that page actually holds. */
  moreMonths = async (rendered: NativeReadPeriodicalState): Promise<void> => {
    const cursor = rendered.archiveCursor;
    if (this.command || this.publicationType !== 'newspaper' || !cursor || !this.current() || this.state !== rendered || rendered.phase !== 'ready') return;
    const life = this.life, flight = ++this.flight, ticket = {};
    if (!life) return;
    this.command = ticket;
    const pending = Object.freeze({...rendered, archivePending: true, archiveError: undefined});
    this.publish(pending);
    try {
      const page = await this.reading().periodicalIssues(this.libraryId, this.groupId, this.publicationType, {cursor, limit: 60}, life.signal);
      if (this.life !== life || this.command !== ticket || flight !== this.flight || this.state !== pending || !this.current()) return;
      if (page.nextCursor === cursor) throw new ReadingError('invalid_document');
      const known = new Set(rendered.months);
      const discovered = monthsOf(page.items).filter(month => !known.has(month));
      this.publish({...rendered, archivePending: false, archiveError: undefined, months: [...rendered.months, ...discovered], ...(page.nextCursor ? {archiveCursor: page.nextCursor} : {archiveCursor: undefined})});
    } catch (error) {
      if (this.life === life && this.command === ticket && flight === this.flight && this.state === pending && this.current() && !cancelled(error))
        this.publish({...rendered, archivePending: false, archiveError: error});
    } finally {if (this.command === ticket) this.command = undefined;}
  };
  itemLifetime = (issue: ReadingPeriodicalIssue): Readonly<{signal: AbortSignal; isCurrent: () => boolean}> | undefined => {
    const life = this.life;
    const current = () => this.life === life && this.current() && this.state.phase === 'ready'
      && (this.publicationType === 'magazine' && this.state.latest === issue || this.state.items.includes(issue) || !!this.state.selectedDateItems?.includes(issue)
        || !!this.state.yearSections?.some(section => section.items.includes(issue)));
    return life && current() ? Object.freeze({signal: life.signal, isCurrent: current}) : undefined;
  };
  /** Only an issue this page actually listed, from the exact rendered state. */
  open = async (rendered: NativeReadPeriodicalState, issue: ReadingPeriodicalIssue): Promise<boolean> => {
    const origin = this.origin, life = this.life;
    // Every issue is one this page actually listed, including its own lazy year
    // sections or exact selected-day response. Magazine keeps its latest hero.
    if (this.command || !origin || !life || !this.current() || this.state !== rendered || rendered.phase !== 'ready'
      || issue.item.inventoryAvailable !== true || !rendered.items.includes(issue) && rendered.latest !== issue
        && !rendered.selectedDateItems?.includes(issue) && !rendered.yearSections?.some(section => section.items.includes(issue))) return false;
    const ticket = {};
    this.command = ticket;
    try {
      return await openNativeReadBook(this.context, {libraryId: this.libraryId, item: issue.item,
        stillCurrent: () => this.life === life && this.command === ticket && this.state === rendered && this.current()}, origin, life.signal);
    }
    finally {if (this.command === ticket) this.command = undefined;}
  };
  dispose = (): void => {
    if (this.disposed) return;
    this.disposed = true;
    this.release();
    this.listeners.clear();
  };
}
