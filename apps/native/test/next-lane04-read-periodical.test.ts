// @fabric-react
import test from 'node:test';
import assert from 'node:assert/strict';
import * as React from 'react';
import {createFabricHookHost} from './helpers/fabric-hooks.mjs';
import {screenModule} from './helpers/screen-harness.mjs';
import {periodicalDatePreview, periodicalDayRead, periodicalHeroIssue, readingItemIsRead} from '../src-next/screens/read/periodical-presentation';

const deferred = <T,>() => {let resolve!: (value: T) => void, reject!: (error: unknown) => void;
  const promise = new Promise<T>((yes, no) => {resolve = yes; reject = no;}); return {promise, resolve, reject};};
const issue = (id: string, date: string, read?: boolean) => ({issueDate: date, item: {id, libraryId: 'library', sourceId: `${id}-source`, title: id,
  inventoryAvailable: true, series: {id: 'group', name: 'Publication'}, ...(read === undefined ? {} : {readingMark: {itemId: id, read, revision: 2}})}});
const newest = issue('newest', '2026-10-28'), older = issue('older', '2026-10-01');
const years = [{value: '2026', count: 61}, {value: '2025', count: 4}, {value: '2024', count: 1}];
const days = [{value: '2026-10-01', count: 1}, {value: '2026-10-28', count: 1}];
const page = (items: readonly unknown[], extra: object = {}) => ({items, yearChoices: years, dayChoices: days, ...extra});
type Query = {year?: string; month?: string; issueDate?: string; cursor?: string; limit?: number; order?: string};
async function fixture(kind = 'newspaper') {
  const authority = {serverId: 'server', accountId: 'account', profileId: 'profile', epoch: 'one', viewerFence: 'viewer'};
  const route = {kind: 'read-periodical', libraryId: 'library', groupId: 'group', publicationType: kind};
  let revision = 1, respond = async (_query: Query): Promise<any> => page([newest], {nextCursor: 'continuation'});
  const calls: Query[] = [], opens: unknown[] = [];
  const {NativeReadPeriodical} = await screenModule(new URL('../src-next/screens/read/read-periodical-model.ts', import.meta.url), {
    react: {default: {}},
    '../../../../../packages/client-core/src': {ReadingError: class extends Error {code: string; constructor(code: string) {super(code); this.code = code;}}},
    '../../shell/model': {sameAuthority: (a: any, b: any) => JSON.stringify(a) === JSON.stringify(b)},
    './eligibility': {nativeReadOrigin: () => ({route, revision}), openNativeReadBook: async (_context: unknown, offer: any) => {
      if (offer.stillCurrent && !offer.stillCurrent()) return false; opens.push(offer.item); return true;
    }},
  });
  const model = new NativeReadPeriodical({api: {reading: () => ({periodicalIssues: async (_library: string, _group: string, _type: string, query: Query) => {
    calls.push(query); return respond(query);
  }, dispose() {}})}, session: () => ({sessionFamilyId: 'family', viewer: {authority: 'local', role: 'member', ...authority}}),
  authority: () => authority, current: () => true, navigation: {getSnapshot: () => ({route, revision})}}, route);
  assert.equal(model.arm(), true); await model.load();
  return {model, calls, opens, respond: (next: typeof respond) => {respond = next;}, retire: () => {revision++; model.release();}};
}

test('newspaper defaults to latest date but its main Read follows the chosen loaded issue', async () => {
  const f = await fixture();
  try {
    assert.equal(f.model.getSnapshot().selectedDate, newest.issueDate);
    assert.equal(periodicalHeroIssue(f.model.getSnapshot(), false), newest);
    f.respond(async () => page([newest, older])); await f.model.selectMonth(f.model.getSnapshot(), '2026-10');
    await f.model.selectDate(f.model.getSnapshot(), older.issueDate);
    const selected = f.model.getSnapshot(); assert.equal(periodicalHeroIssue(selected, false), older);
    assert.equal(await f.model.open(selected, periodicalHeroIssue(selected, false)), true);
    assert.deepEqual(f.opens, [older.item]);
    assert.equal(periodicalHeroIssue(selected, true), newest, 'magazine hero remains latest-first');
  } finally {f.model.dispose();}
});

test('an unloaded real date resolves by exact issueDate and never paints/opens latest while pending', async () => {
  const f = await fixture(), next = deferred<any>();
  try {
    f.respond(async () => next.promise);
    const request = f.model.selectDate(f.model.getSnapshot(), older.issueDate);
    const pending = f.model.getSnapshot();
    assert.equal(pending.selectedDate, older.issueDate); assert.equal(pending.datePending, true);
    assert.equal(periodicalHeroIssue(pending, false), undefined);
    assert.deepEqual(f.calls.at(-1), {month: '2026-10', issueDate: '2026-10-01', limit: 60});
    next.resolve(page([older])); await request;
    assert.equal(f.model.getSnapshot().datePending, false); assert.equal(periodicalHeroIssue(f.model.getSnapshot(), false), older);
    assert.equal(periodicalDatePreview(f.model.getSnapshot(), older.issueDate), older, 'the selected calendar thumbnail uses its resolved date query too');
    assert.equal(await f.model.open(pending, newest), false, 'retained pending render cannot borrow the completed state');
  } finally {next.resolve(page([])); f.model.dispose();}
});

test('unknown dates and retained month callbacks perform no IO', async () => {
  const f = await fixture();
  try {
    const before = f.model.getSnapshot(), count = f.calls.length;
    await f.model.selectDate(before, '2026-10-09'); assert.equal(f.calls.length, count);
    await f.model.selectDate(before, newest.issueDate);
    await f.model.selectMonth(before, '2026-10'); assert.equal(f.calls.length, count);
  } finally {f.model.dispose();}
});

test('choosing a different day supersedes an in-flight lookup and ignores its late response', async () => {
  const f = await fixture(), old = deferred<any>();
  try {
    f.respond(async () => old.promise); const request = f.model.selectDate(f.model.getSnapshot(), older.issueDate);
    await f.model.selectDate(f.model.getSnapshot(), newest.issueDate);
    const selected = f.model.getSnapshot(); assert.equal(selected.datePending, false); assert.equal(periodicalHeroIssue(selected, false), newest);
    old.resolve(page([older])); await request; assert.equal(f.model.getSnapshot(), selected);
  } finally {old.resolve(page([])); f.model.dispose();}
});

test('a day continuation retains existing editions and exposes failure/retry separately from month paging', async () => {
  const f = await fixture(), tail = issue('second-edition', older.issueDate), pending = deferred<any>();
  try {
    f.respond(async () => page([older], {nextCursor: 'day-two'}));
    await f.model.selectDate(f.model.getSnapshot(), older.issueDate);
    f.respond(async () => pending.promise);
    const request = f.model.nextDate(f.model.getSnapshot());
    assert.equal(f.model.getSnapshot().datePending, true); assert.equal(periodicalHeroIssue(f.model.getSnapshot(), false), older);
    const error = new Error('day unavailable'); pending.reject(error); await request;
    assert.equal(f.model.getSnapshot().dateError, error); assert.equal(f.model.getSnapshot().error, undefined);
    assert.equal(f.model.getSnapshot().datePending, false);
    f.respond(async () => page([tail])); await f.model.nextDate(f.model.getSnapshot());
    assert.deepEqual(f.model.getSnapshot().selectedDateItems, [older, tail]); assert.equal(f.model.getSnapshot().dateCursor, undefined);
  } finally {pending.resolve(page([])); f.model.dispose();}
});

test('month continuation publishes a tail state and appends only once for the rendered cursor', async () => {
  const f = await fixture(), tail = deferred<any>();
  try {
    const rendered = f.model.getSnapshot(), count = f.calls.length;
    f.respond(async () => tail.promise); const request = f.model.next(rendered);
    assert.equal(f.model.getSnapshot().pending, true); assert.deepEqual(f.model.getSnapshot().items, [newest]);
    await f.model.next(rendered); assert.equal(f.calls.length, count + 1);
    tail.resolve(page([older])); await request;
    assert.equal(f.model.getSnapshot().pending, false); assert.deepEqual(f.model.getSnapshot().items, [newest, older]);
  } finally {tail.resolve(page([])); f.model.dispose();}
});

test('magazines lazily load independent year sections and retain both out-of-order completions', async () => {
  const f = await fixture('magazine'), a = deferred<any>(), b = deferred<any>();
  try {
    assert.equal(f.calls.length, 2, 'initial page and selected year only, never every year');
    const year2025 = years[1]!, year2024 = years[2]!;
    f.respond(async query => query.year === '2025' ? a.promise : b.promise);
    const first = f.model.ensureYear(f.model.getSnapshot(), year2025);
    const second = f.model.ensureYear(f.model.getSnapshot(), year2024);
    assert.equal(f.model.getSnapshot().yearSections.filter((section: any) => section.pending).length, 2);
    const i2024 = issue('2024-issue', '2024-01-01'), i2025 = issue('2025-issue', '2025-01-01');
    b.resolve(page([i2024])); await second; a.resolve(page([i2025])); await first;
    const sections = f.model.getSnapshot().yearSections;
    assert.equal(sections.find((section: any) => section.year === '2024').items[0], i2024);
    assert.equal(sections.find((section: any) => section.year === '2025').items[0], i2025);
    assert.equal(sections.find((section: any) => section.year === '2026').items[0], newest);
    const count = f.calls.length; await f.model.selectYear(f.model.getSnapshot(), year2025);
    assert.equal(f.calls.length, count, 'a cached year chip scroll does not reload its first page');
    assert.deepEqual(f.model.getSnapshot().items, [i2025]);
    assert.equal(periodicalHeroIssue(f.model.getSnapshot(), true), newest);
  } finally {a.resolve(page([])); b.resolve(page([])); f.model.dispose();}
});

test('year continuation has one pending tail and a failure leaves the already drawn issues', async () => {
  const f = await fixture('magazine'), next = deferred<any>();
  try {
    const rendered = f.model.getSnapshot(), count = f.calls.length;
    f.respond(async () => next.promise); const request = f.model.ensureYear(rendered, years[0], true);
    assert.equal(f.model.getSnapshot().yearSections[0].pending, true);
    await f.model.ensureYear(f.model.getSnapshot(), years[0], true); assert.equal(f.calls.length, count + 1);
    const failure = new Error('offline'); next.reject(failure); await request;
    const section = f.model.getSnapshot().yearSections[0];
    assert.equal(section.pending, false); assert.equal(section.error, failure); assert.deepEqual(section.items, [newest]);
    f.respond(async () => page([older])); await f.model.ensureYear(f.model.getSnapshot(), years[0], true);
    assert.deepEqual(f.model.getSnapshot().yearSections[0].items, [newest, older]);
  } finally {next.resolve(page([])); f.model.dispose();}
});

test('route retirement rejects late date data and retained issue menu lifetimes', async () => {
  const f = await fixture(), next = deferred<any>();
  try {
    const lifetime = f.model.itemLifetime(newest); assert.equal(lifetime.isCurrent(), true);
    f.respond(async () => next.promise); const request = f.model.selectDate(f.model.getSnapshot(), older.issueDate);
    const pending = f.model.getSnapshot(); f.retire();
    assert.equal(lifetime.signal.aborted, true); assert.equal(lifetime.isCurrent(), false);
    next.resolve(page([older])); await request; assert.equal(f.model.getSnapshot(), pending);
    assert.equal(await f.model.open(pending, newest), false);
  } finally {next.resolve(page([])); f.model.dispose();}
});

test('read markers accept only actual matching marks, and a partly loaded date is not marked read', () => {
  const read = issue('one', '2026-10-01', true), second = issue('two', '2026-10-01', true);
  assert.equal(readingItemIsRead(read.item), true);
  assert.equal(readingItemIsRead({...read.item, readingMark: {...read.item.readingMark, itemId: 'other'}}), false);
  assert.equal(readingItemIsRead({...read.item, readingMark: {...read.item.readingMark, revision: -1}}), false);
  assert.equal(readingItemIsRead(newest.item), false);
  const state: any = {items: [read], days: [{value: '2026-10-01', count: 2}], selectedDate: '2026-10-01'};
  assert.equal(periodicalDayRead(state, '2026-10-01'), false);
  assert.equal(periodicalDayRead({...state, selectedDateItems: [read, second]}, '2026-10-01'), true);
  assert.equal(periodicalDayRead({...state, selectedDateItems: [read, issue('two', '2026-10-01', false)]}, '2026-10-01'), false);
});


test('periodical hero inspects My List once per item and route lease, not per publication', async () => {
  const reads: string[] = [], errors: unknown[] = [];
  let selected = newest, lease = new AbortController(), tree: any;
  const actions = {actions: {inspect: async (target: any) => {reads.push(target.item.id); return {itemId: target.item.id, listed: false, revision: 1};}}};
  const runtime = {reportError: (error: unknown) => errors.push(error)};
  const {PeriodicalActions} = await screenModule(new URL('../src-next/screens/read/ReadPeriodicalScreen.tsx', import.meta.url), {
    react: {...React, default: React}, 'react-native': {View: 'View'}, 'react-native-safe-area-context': {}, '@portico/design': {},
    '../../../../../packages/i18n/src': {defaultI18n: {t: (key: string) => key}}, '../../kit/artwork': {StandardBackdrop: 'StandardBackdrop'},
    '../../kit/controls': {Button: 'Button'}, '../../kit/Icon': {}, '../../kit/overlays': {}, '../../kit/states': {}, '../../kit/selection': {},
    '../../kit/cards/primitives': {}, '../../kit/browse/presentation': {browseType: () => ({})}, '../../kit/theme': {useTheme: () => ({colors: {}})},
    '../downloads/request': {}, '../../../platform/offline-reading': {}, './ReadingLoading': {}, './ReadingShelf': {}, './ReadingActions': {},
    './periodical-presentation': {},
  });
  const driver = createFabricHookHost(() => {tree = PeriodicalActions({issue: selected, showRead: false, onOpen() {}, actions, runtime,
    offer: (issue: any) => ({item: issue.item, lifetime: {signal: lease.signal, isCurrent: () => !lease.signal.aborted}, open() {}})});});
  try {
    await driver.mount(); await new Promise(resolve => setImmediate(resolve)); await driver.flush();
    assert.deepEqual(reads, ['newest']);
    for (let index = 0; index < 4; index++) {selected = {...selected, item: {...selected.item}}; await driver.rerender(); await Promise.resolve(); await driver.flush();}
    assert.deepEqual(reads, ['newest']);
    assert.ok(tree);
    selected = older; await driver.rerender(); await new Promise(resolve => setImmediate(resolve)); await driver.flush();
    assert.deepEqual(reads, ['newest', 'older']);
    lease.abort(); lease = new AbortController(); await driver.rerender(); await new Promise(resolve => setImmediate(resolve)); await driver.flush();
    assert.deepEqual(reads, ['newest', 'older', 'older']);
    assert.deepEqual(errors, []);
  } finally {await driver.unmount(); lease.abort();}
});

test('SUP-017: magazine Order walks the whole archive oldest first, years and issues alike', async () => {
  const f = await fixture('magazine');
  try {
    const first = f.model.getSnapshot();
    assert.equal(first.order, 'newest'); assert.equal(first.selectedYear, '2026');
    assert.deepEqual(f.calls.at(-1), {year: '2026', order: 'newest', limit: 60});
    await f.model.setOrder(first, 'oldest');
    const oldest = f.model.getSnapshot();
    assert.equal(oldest.order, 'oldest');
    assert.deepEqual(oldest.years.map((year: any) => year.value), ['2024', '2025', '2026'], 'oldest year first');
    assert.equal(oldest.selectedYear, '2024');
    assert.deepEqual(f.calls.at(-1), {year: '2024', order: 'oldest', limit: 60});
    await f.model.ensureYear(oldest, oldest.years[1]);
    assert.deepEqual(f.calls.at(-1), {year: '2025', order: 'oldest', limit: 60}, 'every section keeps the order');
    const count = f.calls.length;
    await f.model.setOrder(first, 'newest');
    assert.equal(f.calls.length, count, 'a retained render cannot change the order');
  } finally {f.model.dispose();}
});

test('SUP-017: newspapers keep their calendar and refuse an order', async () => {
  const f = await fixture();
  try {
    const before = f.model.getSnapshot(), count = f.calls.length;
    assert.equal(before.order, undefined);
    await f.model.setOrder(before, 'oldest');
    assert.equal(f.calls.length, count);
    assert.ok(f.calls.every(call => call.order === undefined));
  } finally {f.model.dispose();}
});
