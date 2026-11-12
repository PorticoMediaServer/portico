/**
 * Recordings on the new server, behind the same calls the recordings screens already make (`DVRClient`), so the screens
 * stay as they are. Each new-server booking is turned into the fields those screens read: its size, this profile's place in
 * it and whether it was watched, what holds the tuners at a conflict, and whether this profile may change or cancel it.
 * A booking's revision is not kept by the new server, and is left at what the screens treat as not known.
 *
 * The Recorded tab reads the recordings that were made (`state=recorded`), ordered by title, grouped by title; each one names
 * its title in the Recorded TV library (`itemId`, `libraryId`), which is what plays. The Storage tab shows what the server
 * says: free and used space, the space kept free and the most recordings may take, about how many hours are left, the keep
 * rule, the forecast for the next two weeks of bookings, and whether the recordings folder can be written.
 *
 * The facts beyond the generated contract (#2947) are read as optional, so a server that does not send them is read as before.
 */
import type {DvrRecording, DvrRule} from '../../contracts/src/next/api.ts';
import type {GuideProgramme} from './channel-guide.ts';
import type {DeletePreview, DVRPage, DVRSummary, DVRView, Recording, RecordingDetail, RecordingGroups, RecordingRule, RecordingState, RuleConfig, RulePreview} from './dvr.ts';
import {changeRecording, changeRule, endRecording, readDvr, readRecording, readRecordings, readRules, removeRule, type DvrOverview} from './live-tv.ts';
import {readItem} from './items.ts';
import type {ServerRequester} from './query/server-api.ts';

/** What the Storage tab shows: only what the server says (the optional facts only when it says them). */
export type RecordingSpace = Readonly<{freeBytes: number; hoursLeft: number; keepRule: string; writeHealthy: boolean;
  usedBytes?: number; floorBytes?: number; capBytes?: number; keepDays?: number; keepNewest?: number; upcomingBytes?: number; upcomingHours?: number}>;

/** A booking with the facts the server sends beyond the generated contract. */
type BookingFacts = DvrRecording & Partial<Readonly<{channelId: string; libraryId: string; bytes: number; positionMs: number; durationMs: number; watched: boolean;
  playingNow: number; tuners: number; tunersUsedBy: string[]; mayChange: boolean; mayCancel: boolean}>>;
type Airing = Readonly<{title?: string; episodeTitle?: string; season?: number; episode?: number; channel?: string; startsAt: string; endsAt: string; alreadyBooked?: boolean}>;
/** A rule with the facts the server sends beyond the generated contract. */
type RuleFacts = DvrRule & Partial<Readonly<{channelName: string; preview: Readonly<{matches: number; airings: readonly Airing[]}>}>>;
type OverviewFacts = DvrOverview & Partial<Readonly<{usedBytes: number; floorBytes: number; capBytes: number; keepDays: number; keepNewest: number; upcomingBytes: number;
  upcomingHours: number; everyonesRecordings: boolean}>>;
const count = (value: unknown) => typeof value === 'number' && Number.isFinite(value) && value >= 0 ? value : undefined;

const stateOf = (recording: DvrRecording): RecordingState => (
  recording.state === 'scheduled' ? (recording.conflict ? 'conflicted' : 'scheduled')
    : recording.state === 'recording' ? 'recording' : recording.state === 'recorded' ? 'completed' : recording.state === 'partial' ? 'incomplete-playable'
      : recording.state === 'failed' ? 'failed' : recording.state === 'skipped' ? 'skipped' : 'cancelled') as RecordingState;

/** A new-server booking in the fields the recordings screens read. */
export function nextRecording(booking: DvrRecording, libraryId = ''): Recording {
  const recording = booking as BookingFacts, channelId = recording.channelId || recording.channel;
  // The title's place and watched mark come with the title, for a server that sends them (it sends whether this profile may change it).
  const told = typeof recording.mayChange === 'boolean' && !!recording.itemId;
  const programme: GuideProgramme = {id: recording.id, channelId, title: recording.title, start: recording.startsAt, end: recording.endsAt,
    lineage: 'interval-only', seriesId: '', episodeId: '', newEvidence: 'unknown', description: '',
    ...(recording.episodeTitle ? {subtitle: recording.episodeTitle} : {}),
    ...(recording.season !== undefined || recording.episode !== undefined ? {episode: {season: recording.season, number: recording.episode}} : {})} as GuideProgramme;
  return {id: recording.id, revision: 0, occurrence: {sourceId: '', channelId, generation: '', programmeId: recording.id}, programme,
    ruleId: recording.fromRule ? 'rule' : '', options: {beforeSeconds: Math.round(recording.padBeforeMs / 1000), afterSeconds: Math.round(recording.padAfterMs / 1000),
      priority: recording.priority, retentionDays: 0, episodeLimit: 0},
    start: recording.startsAt, end: recording.endsAt, state: stateOf(recording), reason: recording.reason ?? '', keep: recording.keep,
    itemId: recording.itemId ?? '', libraryId: recording.itemId ? recording.libraryId || libraryId : '', coverageStart: recording.startsAt, coverageEnd: recording.endsAt,
    bytes: count(recording.bytes) ?? 0,
    ...(told && count(recording.positionMs) ? {positionMs: recording.positionMs} : {}), ...(told && count(recording.durationMs) ? {durationMs: recording.durationMs} : {}),
    ...(told ? {watched: recording.watched === true} : {}),
    // The server names the channel and who booked it (rule 48).
    ...(recording.channel ? {channel: {id: channelId, name: recording.channel, number: ''}} : {}),
    ...(recording.bookedBy ? {bookedBy: recording.bookedBy} : {}),
    ...(recording.tunersUsedBy?.length ? {tunersUsedBy: [...recording.tunersUsedBy]} : {}),
    ...(typeof recording.mayChange === 'boolean' ? {mayChange: recording.mayChange, mayCancel: recording.mayCancel === true} : {}),
    // A conflict's tuner count only when the server says how many tuners the source has (rule 42).
    conflicts: recording.conflict ? [{start: recording.startsAt, end: recording.endsAt, ...(count(recording.tuners) ? {capacity: recording.tuners} : {})}] : []} as Recording;
}
/** A new-server series rule in the fields the Series rules screen reads. */
export function nextRule(answer: DvrRule): RecordingRule {
  const rule = answer as RuleFacts;
  const config: RuleConfig = {matchKind: 'title', title: rule.title, ...(rule.channel ? {channelId: rule.channel} : {}), name: rule.title, sourceId: rule.source,
    seriesId: '', enabled: !rule.paused, episodes: rule.newOnly ? 'new' : 'all', allowedChannels: rule.channel ? [rule.channel] : [], blockedChannels: [], keywords: [], blockedKeywords: [],
    options: {beforeSeconds: Math.round((rule.padBeforeMs ?? 0) / 1000), afterSeconds: Math.round((rule.padAfterMs ?? 0) / 1000), priority: rule.priority,
      retentionDays: rule.keepDays ?? 0, episodeLimit: rule.keepNewest ?? 0}};
  return {id: rule.id, revision: 0, config, reconcileState: rule.paused ? 'paused' : 'active', diagnostic: '', updatedAt: '',
    ...(rule.channel && rule.channelName ? {channel: {id: rule.channel, name: rule.channelName, number: ''}} : {})};
}
/** The most pages read of the recordings made (for the Recorded tab) or of the rules (for their count). */
const PAGES = 20;

/** A rule's choices as the server takes them. A keep choice of 0 clears it (the owner's default then applies). */
function ruleChange(config: RuleConfig) {
  return {newOnly: config.episodes === 'new', priority: config.options.priority, paused: !config.enabled, anyChannel: config.allowedChannels.length === 0,
    keepNewest: config.options.episodeLimit, keepDays: config.options.retentionDays, padBeforeMs: config.options.beforeSeconds * 1000, padAfterMs: config.options.afterSeconds * 1000};
}
/** One airing a rule would record, as the programme the rule editor's Coming up draws. */
function previewProgramme(airing: Airing, id: string): GuideProgramme {
  return {id, channelId: airing.channel ?? '', title: airing.title ?? '', start: airing.startsAt, end: airing.endsAt, lineage: 'interval-only', seriesId: '', episodeId: '',
    newEvidence: 'unknown', description: '', ...(airing.episodeTitle ? {subtitle: airing.episodeTitle} : {}),
    ...(airing.season !== undefined || airing.episode !== undefined ? {episode: {season: airing.season, number: airing.episode}} : {}),
    ...(airing.alreadyBooked ? {recordingState: 'scheduled'} : {})} as GuideProgramme;
}

export class NextDVRClient {
  private lifetime = new AbortController();
  private accessChanged?: () => void;
  constructor(private readonly api: ServerRequester) {}
  activate() { this.dispose(); this.lifetime = new AbortController(); }
  onAccessChanged(listener: () => void) { this.accessChanged = listener; }
  dispose() { this.accessChanged = undefined; this.lifetime.abort(); }
  private get signal() { return this.lifetime.signal; }
  /**
   * A refused read (401, 403) retires what is shown: the profile no longer has Recordings. A refused command is only a
   * refusal of that command (someone else's recording in Everyone's answers 403), unless the sign-in itself is gone (401).
   */
  private async call<T>(work: (signal: AbortSignal) => Promise<T>, command = false): Promise<T> {
    try { return await work(this.signal); }
    catch (error) { const status = (error as {status?: number} | null)?.status; if (status === 401 || status === 403 && !command) this.accessChanged?.(); throw error; }
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
  /** The recordings that were made, ordered by title then newest first, so one title's recordings stay together. */
  private made() { return this.every((cursor, signal) => readRecordings(this.api, {recorded: true, everyone: this.everyone}, cursor, signal)); }
  /** Everyone's recordings rather than this profile's own (rule 48): the server answers it for any profile that may watch. */
  everyone = false;
  /**
   * The Recorded TV library, which every recording that was made is a title of: what plays one names it. The server names it
   * with the title (`libraryId`); a server that does not is asked once, from one of the titles.
   */
  private recordedLibrary = '';
  private libraryRead?: Promise<string>;
  private async library(recordings: readonly DvrRecording[]): Promise<string> {
    const named = (recordings as readonly BookingFacts[]).find(one => one.itemId && one.libraryId)?.libraryId;
    if (named) return this.recordedLibrary = named;
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
    if (view === 'recorded') return page(await this.shown((await this.made()).filter(one => !group || one.title === group)), []);
    const answer = await this.call(signal => readRecordings(this.api, {history: view === 'history', everyone: this.everyone}, cursor || undefined, signal));
    return page(await this.shown(answer.items), [], answer.nextCursor);
  }
  async summary(): Promise<DVRSummary> {
    // The server gives no count of rules or titles: they are counted from its lists.
    const [answer, groups] = await Promise.all([this.call(signal => readDvr(this.api, signal)), this.groups()]);
    const dvr = answer as OverviewFacts;
    // A profile that may watch but not record has no rules to read (the server refuses it): it counts none.
    const rules = dvr.canRecord ? await this.every((cursor, signal) => readRules(this.api, false, cursor, signal)) : [];
    return {upcoming: dvr.upcoming, recorded: dvr.recorded, rules: rules.length, conflicts: dvr.conflicts, groups: groups.groups.length, unwatched: dvr.unwatched,
      ...(typeof dvr.everyonesRecordings === 'boolean' ? {everyonesRecordings: dvr.everyonesRecordings} : {})};
  }
  /** The Recorded tab's titles: the recordings made, grouped by title, newest first (or by title). */
  async groups(sort: 'recent' | 'title' | 'oldest' = 'recent', _cursor = ''): Promise<RecordingGroups> {
    const byTitle = new Map<string, DvrRecording[]>();
    const recordings = await this.made(), libraryId = await this.library(recordings);
    for (const recording of recordings) byTitle.set(recording.title, [...byTitle.get(recording.title) ?? [], recording]);
    const groups = [...byTitle].map(([title, recordings]) => {
      const latest = recordings.reduce((a, b) => a.startsAt > b.startsAt ? a : b);
      const unwatched = (recordings as BookingFacts[]).filter(one => typeof one.mayChange === 'boolean' && one.itemId && one.watched !== true).length;
      return {key: title, title, recordings: recordings.length, unwatched, kept: recordings.filter(one => one.keep).length,
        exceptions: recordings.filter(one => one.state === 'partial').length, latest: latest.startsAt, recording: nextRecording(latest, libraryId)};
    });
    groups.sort((a, b) => sort === 'title' ? a.title.localeCompare(b.title) : sort === 'oldest' ? a.latest.localeCompare(b.latest) : b.latest.localeCompare(a.latest));
    return {groups, nextCursor: '', revision: 0};
  }
  async detail(recordingId: string): Promise<RecordingDetail> {
    const recording = await this.call(signal => readRecording(this.api, recordingId, signal)) as BookingFacts;
    const tuners = count(recording.tuners);
    return {recording: nextRecording(recording, await this.library([recording])), capacity: {known: tuners !== undefined, effective: tuners ?? 0}, overlaps: [], nextOverlap: ''};
  }
  /** Padding and priority. */
  async update(recording: Recording, options: Recording['options']): Promise<Recording> {
    return nextRecording(await this.call(signal => changeRecording(this.api, recording.id, {padBeforeMs: options.beforeSeconds * 1000, padAfterMs: options.afterSeconds * 1000,
      priority: options.priority}, signal), true), this.recordedLibrary);
  }
  /** A conflict's choice: record this one instead, or skip it. */
  async choose(recording: Recording, choice: 'record' | 'skip'): Promise<Recording> {
    return nextRecording(await this.call(signal => changeRecording(this.api, recording.id, {choice}, signal), true), this.recordedLibrary);
  }
  async keep(recording: Recording): Promise<Recording> {
    return nextRecording(await this.call(signal => changeRecording(this.api, recording.id, {keep: !recording.keep}, signal), true), this.recordedLibrary);
  }
  /** Cancels a booking, or stops one that is recording (what was captured is kept). */
  async cancel(recording: Recording): Promise<Recording> {
    await this.call(signal => endRecording(this.api, recording.id, recording.state === 'recording' ? 'stop' : 'cancel', signal), true);
    return {...recording, state: 'cancelled' as RecordingState};
  }
  /** What Delete removes, read as the server has it now: its size on disk, whether it is kept, and how many are playing it. */
  async deletePreview(recording: Recording): Promise<DeletePreview> {
    const now = await this.call(signal => readRecording(this.api, recording.id, signal)) as BookingFacts;
    return {recordingId: recording.id, revision: 0, bytes: count(now.bytes) ?? recording.bytes, keep: now.keep, activeReaders: (count(now.playingNow) ?? 0) > 0, result: 'delete'};
  }
  async delete(recording: Recording, _preview: DeletePreview): Promise<Recording> {
    await this.call(signal => endRecording(this.api, recording.id, 'delete', signal), true);
    return {...recording, state: 'cancelled' as RecordingState};
  }
  /** Pause or resume a rule, or change its choices. */
  async saveRule(ruleId: string, _revision: number, config: RuleConfig): Promise<RecordingRule> {
    return nextRule(await this.call(signal => changeRule(this.api, ruleId, ruleChange(config), signal), true));
  }
  async deleteRule(rule: RecordingRule, future: 'keep' | 'cancel'): Promise<boolean> {
    await this.call(signal => removeRule(this.api, rule.id, future, signal), true);
    return true;
  }
  /** What an edited rule would record, from the server (`PATCH ?preview=true`, which saves nothing). */
  async preview(config: RuleConfig, ruleId?: string): Promise<RulePreview> {
    if (!ruleId) throw Object.assign(new Error('This is not available on this server yet.'), {code: 'not_on_server'});
    const answer = await this.call(signal => changeRule(this.api, ruleId, ruleChange(config), signal, true)) as RuleFacts;
    if (!answer.preview) throw Object.assign(new Error('This is not available on this server yet.'), {code: 'not_on_server'});
    return {matches: answer.preview.matches, unknownNewEvidence: 0, generation: '', capacity: {known: false, effective: 0},
      sample: answer.preview.airings.map((airing, index) => previewProgramme(airing, `${ruleId}:${index}`))};
  }
  /** The Storage tab's facts, as the server gives them. */
  async storage(): Promise<RecordingSpace> {
    const dvr = await this.call(signal => readDvr(this.api, signal)) as OverviewFacts;
    const known = (name: 'usedBytes' | 'floorBytes' | 'capBytes' | 'keepDays' | 'keepNewest' | 'upcomingBytes' | 'upcomingHours') => count(dvr[name]) !== undefined ? {[name]: dvr[name]} : {};
    return {freeBytes: dvr.freeBytes, hoursLeft: dvr.hoursLeft, keepRule: dvr.keepRule, writeHealthy: dvr.folderOk, ...known('usedBytes'), ...known('floorBytes'), ...known('capBytes'),
      ...known('keepDays'), ...known('keepNewest'), ...known('upcomingBytes'), ...known('upcomingHours')};
  }
}
