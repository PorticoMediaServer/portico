/**
 * Live TV in words for the owner pages: what a guide or playlist import left out and why, and
 * what is using a tuner. The reasons are the server's catalogue keys; one this app has no words
 * for is left out, never shown as a key.
 */
import type {MessageId, MessageValues} from '../../../i18n/src/index.ts';
import type {LiveSourceAcquisition} from '../administration.ts';

type Translate = (id: MessageId, values?: MessageValues) => string;
type Has = (id: string) => boolean;

/**
 * The saved-settings line for a Live source: an upload that has no refresh loop defers to the
 * next upload, a remote source keeps the guide-refresh sentence, and an unknown mode claims no
 * deferral at all.
 */
export function liveSavedSettingsId(acquisition: LiveSourceAcquisition | undefined): MessageId {
  return acquisition === 'upload' ? 'server.live.changesAfterUpload' : acquisition === 'remote' ? 'server.live.settingsSaved' : 'settings.saved';
}

export type ImportSkipped = Readonly<{/** "12 channels and 340 programs were left out. 5 program times were adjusted." */ summary: string; /** Why, in sentences, each once. */ reasons: readonly string[]}>;

const count = (v: unknown): number => (typeof v === 'number' && Number.isSafeInteger(v) && v > 0 ? v : 0);

/** What an import left out, or nothing when it left nothing out. The document is read part by part: an unreadable part counts as nothing. */
export function importSkipped(t: Translate, has: Has, diagnostics: unknown): ImportSkipped | undefined {
  const d = diagnostics && typeof diagnostics === 'object' ? (diagnostics as Record<string, unknown>) : {};
  const channels = count(d.skippedChannels), programmes = count(d.skippedProgrammes), adjusted = count(d.adjustedProgrammes);
  if (!channels && !programmes && !adjusted) return undefined;
  const parts = [
    channels && programmes ? t('server.live.skippedBoth', {channels, programs: programmes}) : channels ? t('server.live.skippedChannels', {count: channels}) : programmes ? t('server.live.skippedProgrammes', {count: programmes}) : '',
    adjusted ? t('server.live.adjusted', {count: adjusted}) : '',
  ].filter(Boolean);
  const reasons: string[] = [];
  for (const key of Array.isArray(d.examples) ? d.examples.slice(0, 10) : []) {
    if (typeof key !== 'string' || !key.startsWith('live.import.') || !has(key)) continue;
    const words = t(key as MessageId);
    if (!reasons.includes(words)) reasons.push(words);
  }
  return {summary: parts.join(' '), reasons};
}

/** One thing using a tuner: "Being watched · BBC One", "Recording · Match of the Day", or when a recording starts. */
export function tunerUseLine(t: Translate, use: Readonly<{kind: string; state: string; title: string; startsAt: string; accountName?: string; profileName?: string}>, when: (iso: string) => string): string {
  const what = use.kind === 'live' ? t('server.live.beingWatched') : use.state === 'recording' || use.kind === 'recording' && !use.startsAt ? t('server.live.recordingNow') : use.startsAt ? when(use.startsAt) : t('server.live.recordingNow');
  return [what, use.title, tunerWhose(t, use), use.state === 'retiring' ? t('server.live.finishing') : ''].filter(Boolean).join(' · ');
}

/** Whose a tuner's use is: the profile, with its account when they differ; empty when the server could not name them. */
export function tunerWhose(t: Translate, use: Readonly<{accountName?: string; profileName?: string}>): string {
  const account = use.accountName ?? '', profile = use.profileName ?? '';
  if (profile && account && profile !== account) return t('server.tuners.whoseProfile', {profile, account});
  return profile || account;
}

/** How busy a source's tuners are, as its status says it. */
export function tunerStatus(t: Translate, source: Readonly<{conflicts: number; inUse: number; tunerCount: number}>): {tone: 'healthy' | 'warning' | 'danger'; text: string} {
  if (source.conflicts) return {tone: 'danger', text: t('server.live.clashes', {count: source.conflicts})};
  return {tone: source.tunerCount > 0 && source.inUse >= source.tunerCount ? 'warning' : 'healthy', text: t('server.live.tunersInUse', {used: source.inUse, total: source.tunerCount})};
}

/** What a remote Live TV source is doing, for the badge on its row, from the admin source document: the source is on or paused by the owner, and its refresh row is healthy, refreshing, degraded or waiting for sign-in. A state this client has no word for says Not checked instead of claiming health. */
export function liveSourceState(source: Readonly<{state: string}>, refresh: Readonly<{state: string}> | undefined): Readonly<{id: MessageId; tone: 'healthy' | 'warning' | 'danger' | 'neutral'; live: boolean}> {
  if (source.state === 'disabled') return {id: 'web.liveTv.pausedBadge', tone: 'neutral', live: false};
  if (refresh?.state === 'refreshing') return {id: 'server.live.state.refreshing', tone: 'warning', live: true};
  if (refresh?.state === 'credentials-required') return {id: 'server.live.state.credentials', tone: 'danger', live: false};
  if (source.state === 'failed') return {id: 'server.live.state.failed', tone: 'danger', live: false};
  if (refresh?.state === 'degraded') return {id: 'server.live.state.degraded', tone: 'danger', live: false};
  if (source.state === 'pending' || source.state === 'new') return {id: 'server.live.state.waiting', tone: 'neutral', live: false};
  if ((source.state === 'active' || source.state === 'ready') && (refresh === undefined || refresh.state === 'healthy')) return {id: 'server.live.state.ready', tone: 'healthy', live: false};
  return {id: 'web.capabilities.unknown', tone: 'neutral', live: false};
}
