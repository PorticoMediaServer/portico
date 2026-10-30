import type {MessageId, MessageValues} from '../../../i18n/src/index.ts';
import type {LibrarySonicDocument} from './library-sonic.ts';

type Translate = (id: MessageId, values?: MessageValues) => string;

/** One drawn Sonic status line. This is presentation only: no retry permission, edit eligibility or capability fact. */
export type LibrarySonicStatusLine = Readonly<{text: string; tone: 'quiet' | 'ok' | 'warn'}>;

const measured = (value: number): boolean => Number.isSafeInteger(value) && value >= 0;
const count = (value: number): string => new Intl.NumberFormat().format(value);

/**
 * The library's Sonic analysis status from the current document, or nothing when the owner turned
 * it off. Counts must be actual non-negative safe integers; the paused time must be a real Date the
 * caller can format. An unusable count is invalid and yields no line rather than a repaired zero.
 */
export function librarySonicStatus(doc: LibrarySonicDocument, t: Translate, formatTime: (at: number) => string): LibrarySonicStatusLine | undefined {
  if (doc.state === 'disabled') return undefined;
  if (!doc.available || doc.state === 'unavailable') return Object.freeze({text: t('next.settings.librarySonic.unavailable'), tone: 'warn'});
  const counts = measured(doc.done) && measured(doc.total);
  if (doc.state === 'complete') return counts ? Object.freeze({text: t('next.settings.librarySonic.complete', {total: count(doc.total)}), tone: 'ok'}) : undefined;
  if (doc.state === 'analyzing') return counts ? Object.freeze({text: t('next.settings.librarySonic.analyzing', {done: count(doc.done), total: count(doc.total)}), tone: 'quiet'}) : undefined;
  if (doc.state === 'paused') {
    const at = doc.pausedUntilMs;
    if (counts && at !== undefined && Number.isSafeInteger(at) && at > 0 && Number.isFinite(new Date(at).getTime())) {
      try {
        const time = formatTime(at);
        if (typeof time === 'string' && time) return Object.freeze({text: t('next.settings.librarySonic.pausedUntil', {time, done: count(doc.done), total: count(doc.total)}), tone: 'quiet'});
      } catch {}
    }
    return Object.freeze({text: t('next.ownerLibraries.paused'), tone: 'quiet'});
  }
  return undefined;
}
