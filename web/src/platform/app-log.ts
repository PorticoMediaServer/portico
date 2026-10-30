/** Root §23: only these reviewed diagnostic codes may enter the explicit journal.
 * Never project exception text, identifiers, paths or arbitrary metadata here. */
export const APP_LOG_CODES = Object.freeze([
  'audio_transition_unavailable', 'audio_edge_late', 'audio_processing_unavailable',
  'equalizer_filter_unavailable', 'audio_autoplay_blocked',
  // O-159: an accepted start that never became drawable, by the read it was still missing.
  'playback_not_drawn_session', 'playback_not_drawn_offers', 'playback_not_drawn_presentation',
] as const);
export type AppLogCode = (typeof APP_LOG_CODES)[number];
export type AppLogLevel = 'info' | 'warn' | 'error';
export type AppLogEntry = Readonly<{atMs: number; level: AppLogLevel; code: AppLogCode}>;
export type AppLogRecorder = (code: AppLogCode, level?: AppLogLevel) => void;
type Journal = {isCurrent(): boolean; entries: readonly AppLogEntry[]; closed: boolean};
let active: Journal | undefined;
const day = 24 * 60 * 60 * 1000;
function recent(entry: AppLogEntry, now: number): boolean {
  return Number.isSafeInteger(entry.atMs) && entry.atMs >= now - day && entry.atMs <= now + 5 * 60 * 1000;
}
function current(journal: Journal | undefined): journal is Journal {
  return !!journal && active === journal && !journal.closed && journal.isCurrent();
}

/** One shell assembly owns the journal. Even the same profile's next session
 * gets a fresh journal; an old callback can never adopt its replacement. */
export function openAppLogContext(isCurrent: () => boolean): () => void {
  if (active) {active.closed = true; active.entries = [];}
  const journal: Journal = {isCurrent, entries: [], closed: false};
  active = journal;
  return () => {journal.closed = true; journal.entries = []; if (active === journal) active = undefined;};
}

export function captureAppLogRecorder(): AppLogRecorder {
  const journal = active;
  return (code, level = 'warn') => {
    if (!current(journal) || !APP_LOG_CODES.includes(code) || !['info', 'warn', 'error'].includes(level)) return;
    const now = Date.now();
    journal.entries = [...journal.entries.filter(entry => recent(entry, now)).slice(-199), Object.freeze({atMs: now, level, code})];
  };
}

/** Captured once per explicit opening. Sending or acknowledging it requires
 * the exact original assembly; entries recorded later remain local. */
export function captureAppLog() {
  const journal = active;
  const bounded = current(journal) ? journal.entries.filter(entry => recent(entry, Date.now())) : [];
  // The opaque recipient revision is always 64 ASCII characters. Reserve its
  // exact encoded envelope now and retain the newest entries that fit it.
  const envelopeBytes = new TextEncoder().encode(JSON.stringify({recipientRevision: '0'.repeat(64), entries: []})).byteLength;
  while (bounded.length && envelopeBytes + new TextEncoder().encode(JSON.stringify(bounded)).byteLength - 2 > 16384) bounded.shift();
  const entries = Object.freeze(bounded);
  return Object.freeze({entries,
    isCurrent: () => current(journal),
    sendable: () => current(journal) && entries.length > 0 && entries.every(entry => recent(entry, Date.now())),
    acknowledge: () => {if (current(journal)) journal.entries = journal.entries.filter(entry => !entries.includes(entry));},
  });
}
