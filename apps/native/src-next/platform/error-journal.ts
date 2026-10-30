/**
 * What this app remembers of its own trouble, for a report a person may send to the owner of
 * their server: the last lines it logged as warnings or errors, and the error that ended the
 * previous run. Kept in memory (the last error of a run is also kept on the device, so it is
 * still there at the next launch) and sent only when the person asks.
 *
 * Nothing here knows about React Native: the store and the clock are handed in, so it is tested
 * under Node.
 */
export type JournalLevel = 'warn' | 'error' | 'fatal';
export type JournalEntry = Readonly<{at: number; level: JournalLevel; text: string}>;
export type FatalRecord = Readonly<{at: number; appVersion: string; message: string; stack: string}>;

export const MAX_ENTRIES = 200;
export const MAX_ENTRY_TEXT = 2000;
/** The server takes 256 KiB a report; this leaves room for the heading and for UTF-8. */
export const MAX_REPORT_CHARS = 120_000;
const MAX_STACK = 8000;

/**
 * Takes what must never travel out of a line: bearer tokens, anything that looks like a token or
 * key (a long unbroken run of base64 or hex), and the secret part of an address (its query and
 * any user name and password). What happened stays readable; who was signed in does not ride along.
 */
export function redact(text: string): string {
  return text
    .replace(/(["']?(?:access[_-]?token|refresh[_-]?token|password|secret|api[_-]?key|proof|authorization)["']?\s*[:=]\s*)(?!Bearer\b|Basic\b)(?:"[^"]*"|'[^']*'|[^\s,;}]+)/gi, '$1[removed]')
    .replace(/(\/v1\/(?:media\/subtitles|media|downloads\/artifacts)\/)[^/\s?#"']+/gi, '$1[removed]')
    .replace(/\b(Bearer|Basic)\s+[A-Za-z0-9._~+/=-]+/gi, '$1 [removed]')
    .replace(/(https?:\/\/)[^/\s:@]+:[^/\s@]*@/gi, '$1')
    .replace(/(https?:\/\/[^\s?#"']+)\?[^\s"']*/gi, '$1?[removed]')
    .replace(/\b[A-Za-z0-9_-]{40,}\b/g, '[removed]')
    .replace(/\beyJ[A-Za-z0-9_-]{8,}(?:\.[A-Za-z0-9_-]+){1,2}/g, '[removed]');
}

export const diagnosticText = (value: unknown): string => redact(oneLine(value));

const oneLine = (value: unknown): string => {
  if (typeof value === 'string') return value;
  if (value instanceof Error) return value.name + ': ' + value.message;
  try { return JSON.stringify(value) ?? String(value); } catch { return String(value); }
};

export class ErrorJournal {
  private entries: JournalEntry[] = [];
  private readonly now: () => number;
  constructor(now: () => number = Date.now) { this.now = now; }

  /** Remembers one line. Each line is redacted and bounded as it comes in, so nothing sensitive is ever held. */
  note(level: JournalLevel, ...parts: unknown[]): void {
    const text = redact(parts.map(oneLine).join(' ')).replace(/\s+/g, ' ').trim().slice(0, MAX_ENTRY_TEXT);
    if (!text) return;
    const last = this.entries[this.entries.length - 1];
    // The same line said again and again (a retry loop) is one line, not two hundred.
    if (last && last.level === level && last.text === text) return;
    this.entries.push({at: this.now(), level, text});
    if (this.entries.length > MAX_ENTRIES) this.entries.splice(0, this.entries.length - MAX_ENTRIES);
  }

  list(): readonly JournalEntry[] { return this.entries; }
  clear(): void { this.entries = []; }
}

/** The error that ended a run, as it is kept for the next one. */
export function fatalRecord(error: unknown, appVersion: string, now: number): FatalRecord {
  const e = (error ?? {}) as {name?: unknown; message?: unknown; stack?: unknown};
  const name = typeof e.name === 'string' && e.name ? e.name + ': ' : '';
  const message = redact(name + (typeof e.message === 'string' ? e.message : oneLine(error))).slice(0, MAX_ENTRY_TEXT);
  const stack = redact(typeof e.stack === 'string' ? e.stack : '').slice(0, MAX_STACK);
  return {at: now, appVersion: appVersion.slice(0, 60), message, stack};
}

/** What was kept, when it is a record this app could have written; anything else reads as nothing. */
export function readFatalRecord(raw: unknown): FatalRecord | undefined {
  if (!raw || typeof raw !== 'object' || Array.isArray(raw)) return undefined;
  const v = raw as Record<string, unknown>;
  if (typeof v.at !== 'number' || !Number.isFinite(v.at) || typeof v.appVersion !== 'string' || v.appVersion.length > 60 || typeof v.message !== 'string' || v.message.length > MAX_ENTRY_TEXT || typeof v.stack !== 'string' || v.stack.length > MAX_STACK) return undefined;
  return {at: v.at, appVersion: v.appVersion, message: redact(v.message), stack: redact(v.stack)};
}

const stamp = (ms: number) => { const d = new Date(ms); return Number.isNaN(d.getTime()) ? '' : d.toISOString(); };

/**
 * The report as the owner reads it: plain lines, newest log lines kept when it has to be cut.
 * These are a diagnostic record, not interface text, so they are not translated.
 */
export function reportBody(o: {appVersion: string; platform: string; deviceClass: string; deviceName?: string; now: number; fatal?: FatalRecord; entries: readonly JournalEntry[]}): string {
  const head = [
    'Portico app report',
    `Sent: ${stamp(o.now)}`,
    `App: ${o.appVersion} on ${o.platform} (${o.deviceClass})`,
    ...(o.deviceName ? [`Device: ${redact(o.deviceName).slice(0, 80)}`] : []),
  ];
  const fatal = o.fatal ? ['', `Closed unexpectedly: ${stamp(o.fatal.at)} (app ${o.fatal.appVersion})`, o.fatal.message, ...(o.fatal.stack ? o.fatal.stack.split('\n').slice(0, 60) : [])] : [];
  const lines = o.entries.map(entry => `${stamp(entry.at)} ${entry.level}: ${entry.text}`);
  const fixed = [...head, ...fatal, '', lines.length ? 'Recent warnings and errors:' : 'No warnings or errors were logged in this run.'].join('\n');
  let budget = MAX_REPORT_CHARS - fixed.length - 1;
  const kept: string[] = [];
  for (let i = lines.length - 1; i >= 0 && budget > 0; i--) {
    const line = lines[i]!;
    if (line.length + 1 > budget) break;
    kept.unshift(line);
    budget -= line.length + 1;
  }
  return (kept.length ? fixed + '\n' + kept.join('\n') : fixed).slice(0, MAX_REPORT_CHARS);
}
