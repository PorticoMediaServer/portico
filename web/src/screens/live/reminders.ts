import {serverNow} from '@core/server-clock.ts';
/** In-app programme reminders for this browser, scoped per (serverId, viewerId).
 * A reminder arrives two minutes before the airing while Portico is open. */
export type LiveReminder = Readonly<{programId: string; channelId: string; title: string; channelName: string; start: number}>;
export type ReminderScope = Readonly<{serverId: string; viewerId: string}>;

const KEY_PREFIX = 'portico.next.live.reminders.v2:';
const listenersByScope = new Map<string, Set<() => void>>();
const MINUTE = 60_000;
/** Reminders fire two minutes before the airing begins. */
export const REMINDER_LEAD_MS = 2 * MINUTE;
/** Cap each chained setTimeout below JS's 32-bit signed int limit (2^31 - 1 ms) so long-horizon timers never overflow. */
export const MAX_REMINDER_DELAY_MS = 60 * MINUTE;
const EXPIRE_GRACE_MS = 5 * MINUTE;

function storageKey(scope: ReminderScope): string {
  return `${KEY_PREFIX}${encodeURIComponent(scope.serverId)}:${encodeURIComponent(scope.viewerId)}`;
}

function notify(scope: ReminderScope): void {
  const listeners = listenersByScope.get(storageKey(scope));
  if (!listeners) return;
  for (const listener of [...listeners]) listener();
}

export function readReminders(scope: ReminderScope, now = serverNow()): readonly LiveReminder[] {
  try {
    const list: unknown = JSON.parse(globalThis.localStorage?.getItem(storageKey(scope)) ?? '[]');
    if (!Array.isArray(list)) return [];
    return list.filter((value): value is LiveReminder => {
      if (value === null || typeof value !== 'object') return false;
      const item = value as Partial<LiveReminder>;
      return typeof item.programId === 'string' && typeof item.channelId === 'string' && typeof item.title === 'string'
        && typeof item.channelName === 'string' && typeof item.start === 'number' && Number.isFinite(item.start)
        && item.start > now - EXPIRE_GRACE_MS;
    });
  } catch { return []; }
}

function save(scope: ReminderScope, list: readonly LiveReminder[]): void {
  try { globalThis.localStorage?.setItem(storageKey(scope), JSON.stringify(list)); } catch { /* A browser with storage disabled simply keeps no reminder. */ }
  notify(scope);
}

export function hasReminder(scope: ReminderScope, programId: string): boolean {
  return readReminders(scope).some(item => item.programId === programId);
}

/** The returned boolean is the new on/off state. */
export function toggleReminder(scope: ReminderScope, reminder: LiveReminder): boolean {
  const current = readReminders(scope);
  const on = current.some(item => item.programId === reminder.programId);
  save(scope, on ? current.filter(item => item.programId !== reminder.programId) : [...current, reminder]);
  return !on;
}

export function removeReminder(scope: ReminderScope, programId: string): void {
  const current = readReminders(scope);
  if (!current.some(item => item.programId === programId)) return;
  save(scope, current.filter(item => item.programId !== programId));
}

export function subscribeReminders(scope: ReminderScope, listener: () => void): () => void {
  const key = storageKey(scope);
  let set = listenersByScope.get(key);
  if (!set) { set = new Set(); listenersByScope.set(key, set); }
  set.add(listener);
  return () => {
    set.delete(listener);
    if (set.size === 0) listenersByScope.delete(key);
  };
}

/** The reminders the server keeps for this profile, as read with a programme: they replace what this browser held. */
export function replaceReminders(scope: ReminderScope, list: readonly LiveReminder[]): void {
  save(scope, list.filter(item => Number.isFinite(item.start)));
}

export type ReminderSchedulerOptions = Readonly<{
  now?: () => number;
  leadMs?: number;
  maxDelayMs?: number;
  setTimer?: (callback: () => void, ms: number) => ReturnType<typeof setTimeout>;
  clearTimer?: (handle: ReturnType<typeof setTimeout>) => void;
}>;

/** Schedules in-app reminder notifications for one (serverId, viewerId) scope using chained
 * setTimeout calls (capped below 32-bit timeout overflow). Returns a stop/cleanup function. */
export function startReminderScheduler(scope: ReminderScope, onDue: (reminder: LiveReminder) => void, options: ReminderSchedulerOptions = {}): () => void {
  const now = options.now ?? serverNow;
  const leadMs = options.leadMs ?? REMINDER_LEAD_MS;
  const maxDelayMs = Math.max(1, Math.min(options.maxDelayMs ?? MAX_REMINDER_DELAY_MS, 2_147_483_647));
  const setTimer = options.setTimer ?? ((cb, ms) => globalThis.setTimeout(cb, ms));
  const clearTimer = options.clearTimer ?? (handle => globalThis.clearTimeout(handle));
  const fired = new Set<string>();
  let timer: ReturnType<typeof setTimeout> | undefined;
  let stopped = false;
  let savingSelf = false;

  const clearPending = () => {
    if (timer !== undefined) { clearTimer(timer); timer = undefined; }
  };

  const evaluate = () => {
    clearPending();
    if (stopped) return;
    const currentNow = now();
    const list = readReminders(scope, currentNow);
    const due: LiveReminder[] = [];
    for (const item of list) {
      const key = `${item.programId}:${item.start}`;
      if (fired.has(key)) continue;
      if (item.start - leadMs <= currentNow) {
        fired.add(key);
        due.push(item);
      }
    }
    if (due.length > 0) {
      const dueIds = new Set(due.map(item => item.programId));
      savingSelf = true;
      try {
        save(scope, list.filter(item => !dueIds.has(item.programId)));
      } finally {
        savingSelf = false;
      }
      for (const item of due) {
        if (stopped) return;
        onDue(item);
      }
      if (stopped) return;
    }
    clearPending();
    const remaining = readReminders(scope, now());
    let nextFireAt: number | undefined;
    for (const item of remaining) {
      const key = `${item.programId}:${item.start}`;
      if (fired.has(key)) continue;
      const fireAt = item.start - leadMs;
      if (nextFireAt === undefined || fireAt < nextFireAt) nextFireAt = fireAt;
    }
    if (nextFireAt !== undefined) {
      const delay = Math.max(0, Math.min(nextFireAt - now(), maxDelayMs));
      timer = setTimer(() => {
        timer = undefined;
        evaluate();
      }, delay);
    }
  };

  const unsubscribe = subscribeReminders(scope, () => {
    if (!stopped && !savingSelf) evaluate();
  });
  evaluate();

  return () => {
    if (stopped) return;
    stopped = true;
    clearPending();
    unsubscribe();
  };
}
