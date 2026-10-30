/** In-app programme reminders for this browser. The drawn note is precise: a reminder
 * arrives two minutes before the airing while Portico is open, and nothing leaves the
 * client. Keys and ids come from the guide; no server fact is invented. */
export type LiveReminder = Readonly<{programId: string; channelId: string; title: string; channelName: string; start: number}>;

const KEY = 'portico.next.live.reminders.v1';
const listeners = new Set<() => void>();
const MINUTE = 60_000;

export function readReminders(): readonly LiveReminder[] {
  try {
    const list: unknown = JSON.parse(globalThis.localStorage?.getItem(KEY) ?? '[]');
    if (!Array.isArray(list)) return [];
    return list.filter((value): value is LiveReminder => {
      if (value === null || typeof value !== 'object') return false;
      const item = value as Partial<LiveReminder>;
      return typeof item.programId === 'string' && typeof item.channelId === 'string' && typeof item.title === 'string'
        && typeof item.channelName === 'string' && typeof item.start === 'number' && Number.isFinite(item.start)
        && item.start > Date.now() - 5 * MINUTE;
    });
  } catch { return []; }
}

function save(list: readonly LiveReminder[]): void {
  try { globalThis.localStorage?.setItem(KEY, JSON.stringify(list)); } catch { /* A browser with storage disabled simply keeps no reminder. */ }
  for (const listener of [...listeners]) listener();
}

export function hasReminder(programId: string): boolean {
  return readReminders().some(item => item.programId === programId);
}

/** The returned boolean is the new on/off state. */
export function toggleReminder(reminder: LiveReminder): boolean {
  const current = readReminders();
  const on = current.some(item => item.programId === reminder.programId);
  save(on ? current.filter(item => item.programId !== reminder.programId) : [...current, reminder]);
  return !on;
}

export function subscribeReminders(listener: () => void): () => void {
  listeners.add(listener);
  return () => { listeners.delete(listener); };
}
