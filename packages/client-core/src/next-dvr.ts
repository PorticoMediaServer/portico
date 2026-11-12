/**
 * Recordings on the new server, behind the same calls the recordings screens already make (`DVRClient`), so the screens
 * stay as they are. Each new-server booking is turned into the fields those screens read; facts the new server does not
 * keep (a size, a play position, a booking's revision) are left at what the screens treat as not known.
 *
 * The Recorded tab is the profile's History of recordings that were made (recorded, or partly recorded), grouped by title;
 * each one names its title in the Recorded TV library (`itemId`), which is what plays. The Storage tab shows what the server
 * says: the free space, about how many hours are left, the keep rule and whether the recordings folder can be written.
 */
import type {DvrRecording, DvrRule} from '../../contracts/src/next/api.ts';
import type {GuideProgramme} from './channel-guide.ts';
import type {DeletePreview, DVRPage, DVRSummary, DVRView, Recording, RecordingDetail, RecordingGroups, RecordingRule, RecordingState, RuleConfig, RulePreview} from './dvr.ts';
import {changeRecording, changeRule, endRecording, readDvr, readRecording, readRecordings, readRules, removeRule, type DvrOverview} from './live-tv.ts';
import {readItem} from './items.ts';
import type {ServerRequester} from './query/server-api.ts';

/** What the Storage tab shows: only what the server says. */
export type RecordingSpace = Readonly<{freeBytes: number; hoursLeft: number; keepRule: string; writeHealthy: boolean}>;

const stateOf = (recording: DvrRecording): RecordingState => (
  recording.state === 'scheduled' ? (recording.conflict ? 'conflicted' : 'scheduled')
    : recording.state === 'recording' ? 'recording' : recording.state === 'recorded' ? 'completed' : recording.state === 'partial' ? 'incomplete-playable'
      : recording.state === 'failed' ? 'failed' : recording.state === 'skipped' ? 'skipped' : 'cancelled') as RecordingState;

/** A new-server booking in the fields the recordings screens read. */
export function nextRecording(recording: DvrRecording, libraryId = ''): Recording {
  const programme: GuideProgramme = {id: recording.id, channelId: recording.channel, title: recording.title, start: recording.startsAt, end: recording.endsAt,
    lineage: 'interval-only', seriesId: '', episodeId: '', newEvidence: 'unknown', description: '',
    ...(recording.episodeTitle ? {subtitle: recording.episodeTitle} : {}),
    ...(recording.season !== undefined || recording.episode !== undefined ? {episode: {season: recording.season, number: recording.episode}} : {})} as GuideProgramme;
  return {id: recording.id, revision: 0, occurrence: {sourceId: '', channelId: recording.channel, generation: '', programmeId: recording.id}, programme,
    ruleId: recording.fromRule ? 'rule' : '', options: {beforeSeconds: Math.round(recording.padBeforeMs / 1000), afterSeconds: Math.round(recording.padAfterMs / 1000),
      priority: recording.priority, retentionDays: 0, episodeLimit: 0},
    start: recording.startsAt, end: recording.endsAt, state: stateOf(recording), reason: recording.reason ?? '', keep: recording.keep,
    itemId: recording.itemId ?? '', libraryId: recording.itemId ? libraryId : '', coverageStart: recording.startsAt, coverageEnd: recording.endsAt, bytes: 0,
    // The server names the channel and who booked it; it sends no tuner count, so none is given.
    ...(recording.channel ? {channel: {id: recording.channel, name: recording.channel, number: ''}} : {}),
    ...(recording.bookedBy ? {bookedBy: recording.bookedBy} : {}),
    conflicts: recording.conflict ? [{start: recording.startsAt, end: recording.endsAt}] : []} as Recording;
}
/** A new-server series rule in the fields the Series rules screen reads. */
export function nextRule(rule: DvrRule): RecordingRule {
  const config: RuleConfig = {matchKind: 'title', title: rule.title, ...(rule.channel ? {channelId: rule.channel} : {}), name: rule.title, sourceId: rule.source,
    seriesId: '', enabled: !rule.paused, episodes: rule.newOnly ? 'new' : 'all', allowedChannels: rule.channel ? [rule.channel] : [], blockedChannels: [], keywords: [], blockedKeywords: [],
    options: {beforeSeconds: Math.round((rule.padBeforeMs ?? 0) / 1000), afterSeconds: Math.round((rule.padAfterMs ?? 0) / 1000), priority: rule.priority,
      retentionDays: rule.keepDays ?? 0, episodeLimit: rule.keepNewest ?? 0}};
  return {id: rule.id, revision: 0, config, reconcileState: rule.paused ? 'paused' : 'active', diagnostic: '', updatedAt: ''};
}
const made = (recording: DvrRecording) => recording.state === 'recorded' || recording.state === 'partial';
/** The most pages read of History (for the Recorded tab) or of the rules (for their count). */
const PAGES = 20;

export class NextDVRClient {
  private lifetime = new AbortController();
  private accessChanged?: () => void;
  constructor(private readonly api: ServerRequester) {}
  activate() { this.dispose(); this.lifetime = new AbortController(); }
  onAccessChanged(listener: () => void) { this.accessChanged = listener; }
  dispose() { this.accessChanged = undefined; this.lifetime.abort(); }
  private get signal() { return this.lifetime.signal; }
  /** A refusal of access retires what is shown, as the old client did. */
  private async call<T>(work: (signal: AbortSignal) => Promise<T>): Promise<T> {
    try { return await work(this.signal); }
    catch (error) { const status = (error as {status?: number} | null)?.status; if (status === 401 || status === 403) this.accessChanged?.(); throw error; }
  }
  /** Every page of a list, up to PAGES of them. */
  private async every<T>(read: (cursor: string | undefined, signal: AbortSignal) => Promise<{items: T[]; nextCursor?: string}>): Promise<T[]> {
    const out: T[] = [];
    let cursor: string | undefined;
    for (let page = 0; page < PAGES; page++) {
      const answer = await this.call(signal => read(cursor, signal));
      out.push(...answer.items);
      if (!(cursor = answer.nextCursor)) break;
    }
    return out;
  }
  private history() { return this.every((cursor, signal) => readRecordings(this.api, {history: true, everyone: this.everyone}, cursor, signal)); }
  /** Everyone's recordings rather than this profile's own (rule 48): the server answers it for any profile that may watch. */
  everyone = false;
  /**
   * The Recorded TV library, which every recording that was made is a title of: what plays one names it. The server gives a
   * recording only its title, so the library is read once from one of them.
   */
  private recordedLibrary = '';
  private libraryRead?: Promise<string>;
  private async library(recordings: readonly DvrRecording[]): Promise<string> {
    const first = recordings.find(one => one.itemId)?.itemId;
    if (this.recordedLibrary || !first) return this.recordedLibrary;
    // Lists asked for together share the one read; a failed read is tried again next time.
    this.libraryRead ??= this.call(signal => readItem(this.api, first, signal)).then(item => this.recordedLibrary = item.libraryId ?? '')
      .finally(() => {this.libraryRead = undefined;});
    return this.libraryRead;
  }
  private async shown(recordings: DvrRecording[]): Promise<Recording[]> {
    const libraryId = await this.library(recordings);
    return recordings.map(one => nextRecording(one, libraryId));
  }

  /** One view's recordings; for Recorded, `group` keeps one title of the Recorded tab. */
  async list(view: Exclude<DVRView, 'storage'>, cursor = '', _sort: 'recent' | 'title' | 'oldest' = 'recent', group = ''): Promise<DVRPage> {
    const page = (recordings: Recording[], rules: RecordingRule[], next = '') => ({recordings, rules, nextCursor: next, revision: 0, captureAvailable: true,
      captureUnavailableReason: '', deletionAvailable: true, usage: {bytes: 0, pendingDeleteBytes: 0, recordings: recordings.length}});
    if (view === 'rules') {
      const answer = await this.call(signal => readRules(this.api, false, cursor || undefined, signal));
      return page([], answer.items.map(nextRule), answer.nextCursor);
    }
    if (view === 'recorded') return page(await this.shown((await this.history()).filter(one => made(one) && (!group || one.title === group))), []);
    const answer = await this.call(signal => readRecordings(this.api, {history: view === 'history', everyone: this.everyone}, cursor || undefined, signal));
    return page(await this.shown(answer.items), [], answer.nextCursor);
  }
  async summary(): Promise<DVRSummary> {
    // The server gives no count of rules or titles: they are counted from its lists.
    const [dvr, groups] = await Promise.all([this.call(signal => readDvr(this.api, signal)), this.groups()]);
    // A profile that may watch but not record has no rules to read (the server refuses it): it counts none.
    const rules = dvr.canRecord ? await this.every((cursor, signal) => readRules(this.api, false, cursor, signal)) : [];
    return {upcoming: dvr.upcoming, recorded: dvr.recorded, rules: rules.length, conflicts: dvr.conflicts, groups: groups.groups.length, unwatched: dvr.unwatched};
  }
  /** The Recorded tab's titles: the recordings made, grouped by title, newest first (or by title). */
  async groups(sort: 'recent' | 'title' | 'oldest' = 'recent', _cursor = ''): Promise<RecordingGroups> {
    const byTitle = new Map<string, DvrRecording[]>();
    const recordings = (await this.history()).filter(made), libraryId = await this.library(recordings);
    for (const recording of recordings) byTitle.set(recording.title, [...byTitle.get(recording.title) ?? [], recording]);
    const groups = [...byTitle].map(([title, recordings]) => {
      const latest = recordings.reduce((a, b) => a.startsAt > b.startsAt ? a : b);
      return {key: title, title, recordings: recordings.length, unwatched: 0, kept: recordings.filter(one => one.keep).length,
        exceptions: recordings.filter(one => one.state === 'partial').length, latest: latest.startsAt, recording: nextRecording(latest, libraryId)};
    });
    groups.sort((a, b) => sort === 'title' ? a.title.localeCompare(b.title) : sort === 'oldest' ? a.latest.localeCompare(b.latest) : b.latest.localeCompare(a.latest));
    return {groups, nextCursor: '', revision: 0};
  }
  async detail(recordingId: string): Promise<RecordingDetail> {
    const recording = await this.call(signal => readRecording(this.api, recordingId, signal));
    return {recording: nextRecording(recording, await this.library([recording])), capacity: {known: false, effective: 0}, overlaps: [], nextOverlap: ''};
  }
  /** Padding and priority. */
  async update(recording: Recording, options: Recording['options']): Promise<Recording> {
    return nextRecording(await this.call(signal => changeRecording(this.api, recording.id, {padBeforeMs: options.beforeSeconds * 1000, padAfterMs: options.afterSeconds * 1000,
      priority: options.priority}, signal)), this.recordedLibrary);
  }
  /** A conflict's choice: record this one instead, or skip it. */
  async choose(recording: Recording, choice: 'record' | 'skip'): Promise<Recording> {
    return nextRecording(await this.call(signal => changeRecording(this.api, recording.id, {choice}, signal)), this.recordedLibrary);
  }
  async keep(recording: Recording): Promise<Recording> {
    return nextRecording(await this.call(signal => changeRecording(this.api, recording.id, {keep: !recording.keep}, signal)), this.recordedLibrary);
  }
  /** Cancels a booking, or stops one that is recording (what was captured is kept). */
  async cancel(recording: Recording): Promise<Recording> {
    await this.call(signal => endRecording(this.api, recording.id, recording.state === 'recording' ? 'stop' : 'cancel', signal));
    return {...recording, state: 'cancelled' as RecordingState};
  }
  /** The server has no separate preview of a delete: it is the recording as shown. */
  async deletePreview(recording: Recording): Promise<DeletePreview> {
    return {recordingId: recording.id, revision: 0, bytes: recording.bytes, keep: recording.keep, activeReaders: false, result: 'delete'};
  }
  async delete(recording: Recording, _preview: DeletePreview): Promise<Recording> {
    await this.call(signal => endRecording(this.api, recording.id, 'delete', signal));
    return {...recording, state: 'cancelled' as RecordingState};
  }
  /** Pause or resume a rule, or change its choices. */
  async saveRule(ruleId: string, _revision: number, config: RuleConfig): Promise<RecordingRule> {
    return nextRule(await this.call(signal => changeRule(this.api, ruleId, {newOnly: config.episodes === 'new', priority: config.options.priority, paused: !config.enabled,
      anyChannel: config.allowedChannels.length === 0, ...(config.options.episodeLimit ? {keepNewest: config.options.episodeLimit} : {}),
      ...(config.options.retentionDays ? {keepDays: config.options.retentionDays} : {}), padBeforeMs: config.options.beforeSeconds * 1000,
      padAfterMs: config.options.afterSeconds * 1000}, signal)));
  }
  async deleteRule(rule: RecordingRule, future: 'keep' | 'cancel'): Promise<boolean> {
    await this.call(signal => removeRule(this.api, rule.id, future, signal));
    return true;
  }
  /** What an edited rule would record: the server previews only a rule being made from a programme, so there is none. */
  async preview(_config: RuleConfig): Promise<RulePreview> {
    throw Object.assign(new Error('This is not available on this server yet.'), {code: 'not_on_server'});
  }
  /** The Storage tab's facts, as the server gives them. */
  async storage(): Promise<RecordingSpace> {
    const dvr: DvrOverview = await this.call(signal => readDvr(this.api, signal));
    return {freeBytes: dvr.freeBytes, hoursLeft: dvr.hoursLeft, keepRule: dvr.keepRule, writeHealthy: dvr.folderOk};
  }
}
