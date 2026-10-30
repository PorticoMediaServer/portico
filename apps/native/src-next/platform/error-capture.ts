import {NativeModules} from 'react-native';
import {NativeQueueStore} from './queue-storage';
import {diagnosticText, ErrorJournal, fatalRecord, readFatalRecord, type FatalRecord} from './error-journal';

/**
 * What this app keeps of its own trouble (`error-journal`), wired to the runtime: warnings and
 * errors it logs are remembered for this run, and an error that ends the run is kept on the
 * device so the next launch still has it. Nothing is sent from here: a report goes to the owner
 * of the server only when the person asks (Settings › About › Send a report).
 *
 * Installed once, as the app starts. It depends on nothing but the runtime, so it is in place
 * before the rest of the app loads.
 */
export const journal = new ErrorJournal();

const store = new NativeQueueStore();
const KEY = 'report.fatal:v1';
/** How long the end of a run is held back so what ended it can be kept. */
const KEEP_MS = 800;

type Handler = (error: unknown, fatal?: boolean) => void;
let appVersion = '';
let installed = false;

export function installErrorCapture(): void {
  if (installed) return;
  installed = true;
  try { void Promise.resolve(NativeModules.PorticoCapabilities?.facts?.()).then((facts: {appVersion?: unknown} | null | undefined) => { if (typeof facts?.appVersion === 'string') appVersion = facts.appVersion; }, () => {}); } catch { /* the report says the version is unknown */ }
  for (const level of ['warn', 'error'] as const) {
    const original = console[level];
    console[level] = (...args: unknown[]) => {
      try { journal.note(level, ...args); } catch { /* remembering never breaks logging */ }
      original.apply(console, args.map(diagnosticText));
    };
  }
  const errors = (globalThis as {ErrorUtils?: {getGlobalHandler?: () => Handler | undefined; setGlobalHandler: (handler: Handler) => void}}).ErrorUtils;
  if (!errors) return;
  const previous = errors.getGlobalHandler?.();
  errors.setGlobalHandler((error, fatal) => {
    try { journal.note(fatal ? 'fatal' : 'error', error instanceof Error ? error.stack ?? error.message : error); } catch { /* as above */ }
    if (!fatal) { previous?.(error, fatal); return; }
    // The run is about to end. What ended it is kept first, then the system's own handling goes
    // ahead, whether or not the device answered in time.
    let handed = false;
    const hand = () => { if (handed) return; handed = true; previous?.(error, fatal); };
    const timer = setTimeout(hand, KEEP_MS);
    let record: FatalRecord;
    try { record = fatalRecord(error, appVersion, Date.now()); } catch { clearTimeout(timer); hand(); return; }
    void store.change(KEY, () => ({record, value: undefined})).catch(() => {}).finally(() => { clearTimeout(timer); hand(); });
  });
}

/** The error that ended an earlier run, if one is still kept. */
export function lastCrash(): Promise<FatalRecord | undefined> {
  return store.change<FatalRecord | undefined>(KEY, raw => ({record: raw, value: readFatalRecord(raw)})).catch(() => undefined);
}

/** Forgets the kept error once it has been sent (only that one: a newer crash is not lost). */
export function forgetCrash(sent: FatalRecord): Promise<void> {
  return store.change<void>(KEY, raw => ({record: readFatalRecord(raw)?.at === sent.at ? null : raw, value: undefined})).catch(() => {});
}

installErrorCapture();
