import test from 'node:test';
import assert from 'node:assert/strict';
import {createElement} from 'react';
import {renderToStaticMarkup} from 'react-dom/server';
import {registerHooks} from 'node:module';
import {transformTsx} from './transform-tsx.mjs';
import {
  MAX_REMINDER_DELAY_MS,
  REMINDER_LEAD_MS,
  hasReminder,
  readReminders,
  replaceReminders,
  startReminderScheduler,
  subscribeReminders,
  toggleReminder,
  type LiveReminder,
} from '../src/screens/live/reminders.ts';

const loader = registerHooks({load(url, context, next) {
  if (url.endsWith('.module.css')) return {format: 'module' as const, source: 'export default new Proxy({}, {get: (_, key) => String(key)});', shortCircuit: true};
  if (url.endsWith('.tsx')) return {format: 'module' as const, source: transformTsx(url), shortCircuit: true};
  return next(url, context);
}});
const {ProgrammePanelView, isSyntheticPreviewAiring} = await import('../src/screens/live/ProgrammePanel.tsx');
test.after(() => loader.deregister());

function installMemoryStorage() {
  const store = new Map<string, string>();
  const previous = Object.getOwnPropertyDescriptor(globalThis, 'localStorage');
  Object.defineProperty(globalThis, 'localStorage', {
    configurable: true,
    value: {
      getItem: (key: string) => store.get(key) ?? null,
      setItem: (key: string, value: string) => { store.set(key, String(value)); },
      removeItem: (key: string) => { store.delete(key); },
      clear: () => { store.clear(); },
    },
  });
  return () => {
    if (previous) Object.defineProperty(globalThis, 'localStorage', previous);
    else delete (globalThis as Record<string, unknown>).localStorage;
  };
}

test('reminders storage and subscriptions are strictly scoped by (serverId, viewerId)', () => {
  const restore = installMemoryStorage();
  try {
    const scopeA = {serverId: 'srv-1', viewerId: 'alice'};
    const scopeB = {serverId: 'srv-1', viewerId: 'bob'};
    const scopeC = {serverId: 'srv-2', viewerId: 'alice'};
    const start = Date.now() + 30 * 60_000;
    const item: LiveReminder = {programId: 'pg-1', channelId: 'ch-1', title: 'Evening News', channelName: 'News 24', start};

    let hitsA = 0, hitsB = 0;
    const offA = subscribeReminders(scopeA, () => { hitsA++; });
    const offB = subscribeReminders(scopeB, () => { hitsB++; });
    try {
      assert.equal(toggleReminder(scopeA, item), true);
      assert.equal(hasReminder(scopeA, 'pg-1'), true);
      assert.equal(hasReminder(scopeB, 'pg-1'), false);
      assert.equal(hasReminder(scopeC, 'pg-1'), false);
      assert.equal(hitsA, 1);
      assert.equal(hitsB, 0);

      replaceReminders(scopeB, [{...item, programId: 'pg-2'}]);
      assert.deepEqual(readReminders(scopeA).map(r => r.programId), ['pg-1']);
      assert.deepEqual(readReminders(scopeB).map(r => r.programId), ['pg-2']);
      assert.equal(hitsA, 1);
      assert.equal(hitsB, 1);
    } finally { offA(); offB(); }
  } finally { restore(); }
});

test('reminder scheduler chains capped setTimeouts for long timers, fires once at lead time, reschedules on updates, and cleans up on stop', () => {
  const restore = installMemoryStorage();
  try {
    const scope = {serverId: 'srv-1', viewerId: 'alice'};
    let now = Date.parse('2026-11-01T12:00:00Z');
    const scheduled: {id: number; delay: number; cb: () => void; cleared: boolean}[] = [];
    let nextId = 1;
    const setTimer = (cb: () => void, ms: number) => {
      const entry = {id: nextId++, delay: ms, cb, cleared: false};
      scheduled.push(entry);
      return entry.id as unknown as ReturnType<typeof setTimeout>;
    };
    const clearTimer = (handle: ReturnType<typeof setTimeout>) => {
      const entry = scheduled.find(item => item.id === (handle as unknown as number));
      if (entry) entry.cleared = true;
    };

    // 40 days out exceeds 32-bit signed int ms (~24.85 days): timer must be capped cleanly.
    const fortyDaysMs = 40 * 24 * 60 * 60_000;
    replaceReminders(scope, [{programId: 'far', channelId: 'ch-1', title: 'Far Show', channelName: 'Channel 1', start: now + fortyDaysMs}]);
    const fired: LiveReminder[] = [];
    const stop = startReminderScheduler(scope, item => { fired.push(item); }, {now: () => now, setTimer, clearTimer});

    assert.equal(scheduled.length, 1);
    assert.equal(scheduled[0]!.delay, MAX_REMINDER_DELAY_MS, 'long timer must be capped below 32-bit overflow');
    assert.equal(fired.length, 0);

    // Adding a closer reminder clears the old timer and schedules for start - REMINDER_LEAD_MS.
    const soonStart = now + 10 * 60_000;
    toggleReminder(scope, {programId: 'soon', channelId: 'ch-2', title: 'Soon Show', channelName: 'Channel 2', start: soonStart});
    assert.equal(scheduled[0]!.cleared, true);
    assert.equal(scheduled.length, 2);
    assert.equal(scheduled[1]!.delay, 10 * 60_000 - REMINDER_LEAD_MS);

    // Advance clock to fire time and invoke callback: reminder fires once and is removed.
    now = soonStart - REMINDER_LEAD_MS;
    scheduled[1]!.cb();
    assert.deepEqual(fired.map(item => item.programId), ['soon']);
    assert.deepEqual(readReminders(scope, now).map(item => item.programId), ['far']);
    // Next chained timer is scheduled for 'far'.
    assert.equal(scheduled.length, 3);
    assert.equal(scheduled[2]!.delay, MAX_REMINDER_DELAY_MS);

    // Stopping clears active timer and unsubscribes from future reminder additions.
    stop();
    assert.equal(scheduled[2]!.cleared, true);
    toggleReminder(scope, {programId: 'after-stop', channelId: 'ch-3', title: 'Late', channelName: 'Channel 3', start: now + 60_000});
    assert.equal(scheduled.length, 3);
    assert.equal(fired.length, 1);
  } finally { restore(); }
});

test('ProgrammePanelView hides Remind buttons on synthetic :preview: airings and only shows reminderSet copy when a reminder is active', () => {
  assert.equal(isSyntheticPreviewAiring('pg_1:preview:0'), true);
  assert.equal(isSyntheticPreviewAiring('pg_real'), false);

  const now = Date.parse('2026-11-12T20:00:00Z');
  const channel = {id: 'ch_1', sourceId: 'src_1', kind: 'live' as const, number: '101', name: 'Harbour News', group: '', favorite: false, tuneAvailable: true, recordAvailable: true, guide: 'full' as const};
  const futureProgramme = {id: 'pg_future', channelId: 'ch_1', title: 'Late Show', start: now + 3_600_000, end: now + 7_200_000};
  const baseProps = {
    channel,
    programme: futureProgramme,
    now,
    timezone: 'UTC',
    choice: {episodes: 'all' as const, keep: 'all', startEarly: 0, endLate: 0, anyChannel: false},
    seriesOpen: false,
    onClose: () => {},
    onSeriesOpenChange: () => {},
    onChoice: () => {},
    onReminder: () => {},
    onProgrammeReminder: () => {},
  };

  // Synthetic :preview: airing + no active reminders: no Remind button on synthetic row and no reminderSet copy.
  const noReminderHtml = renderToStaticMarkup(createElement(ProgrammePanelView, {
    ...baseProps,
    reminders: [],
    snapshot: {
      phase: 'ready',
      favorite: false,
      previewLoading: false,
      preview: {
        matches: 1,
        airings: [{id: 'pg_future:preview:0', start: now + 86_400_000, time: '8:00 PM', range: '8:00 to 9:00 PM', day: 'Friday', title: 'Preview Ep', channelId: 'ch_1'}],
      },
    },
  }));
  assert.equal(noReminderHtml.includes('You’ll get a reminder'), false, 'reminderSet copy must not appear when no reminder is set');
  assert.equal(noReminderHtml.includes('airingRemind'), false, 'synthetic :preview: airing must not render a Remind button');

  // Real airing + active reminder on both programme and real airing: Remind button and reminderSet copy appear.
  const withReminderHtml = renderToStaticMarkup(createElement(ProgrammePanelView, {
    ...baseProps,
    reminders: ['pg_future', 'pg_real_2'],
    snapshot: {
      phase: 'ready',
      favorite: false,
      previewLoading: false,
      preview: {
        airings: [{id: 'pg_real_2', start: now + 86_400_000, time: '8:00 PM', range: '8:00 to 9:00 PM', day: 'Friday', title: 'Real Ep', channelId: 'ch_1'}],
      },
    },
  }));
  assert.equal(withReminderHtml.includes('You’ll get a reminder'), true);
  assert.equal(withReminderHtml.includes('airingRemind'), true);
});
