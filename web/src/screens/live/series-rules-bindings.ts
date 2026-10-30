import {DVRClient, isAccessError, validRecordingOptions, type DVRPage, type DVRSummary, type RecordingRule, type RecordingStorage, type RuleConfig} from '@core/dvr.ts';
import {fetchGuideSourceSummaries, type GuideSourceSummaries} from '@core/guide/directory.ts';
import {localDayStart} from '@core/guide/time.ts';
import type {GuideProgramme} from '@core/channel-guide.ts';
import {defaultI18n} from '@i18n';
import {screenLifetime, type ScreenContext} from '../detail/context';
import type {RuleAiring, RuleSettings, SeriesRule, SeriesRulesView} from './series-rules-model';

const cancelled = () => new DOMException('Recording rules changed', 'AbortError');
const invalid = () => Object.assign(new Error('invalid_recording_rules'), {code: 'invalid_recording_rules'});
const freeze = <T>(value: T): T => {
  if (value && typeof value === 'object') {for (const child of Object.values(value)) freeze(child);Object.freeze(value);}
  return value;
};
type RuleEntry = Readonly<{rule: RecordingRule; model: SeriesRule}>;
export type SeriesRulesSnapshot = Readonly<{phase: 'idle' | 'loading' | 'ready' | 'error' | 'retired'; view?: SeriesRulesView;
  loadingMore: boolean; nextCursor?: string; mutating: boolean; error?: unknown; previewError?: unknown; previewLoading: boolean;
  preview?: Readonly<{ruleId: string; settings: RuleSettings; count: number; airings: readonly RuleAiring[]}>}>;
type Environment = Readonly<{timezone: string; locale: string; now?: () => number; requestId?: () => Promise<string>}>;

export function ruleSettings(rule: RecordingRule): RuleSettings {
  const config = rule.config, title = config.matchKind === 'title';
  const channel = title ? 'this' : config.allowedChannels.length === 0 ? 'any'
    : config.allowedChannels.length === 1 && config.allowedChannels[0] === rule.summary?.channel.id ? 'this' : undefined;
  return Object.freeze({enabled: config.enabled, episodes: config.episodes, ...(channel ? {channel} : {}), ...(title ? {channelLocked: true} : {}),
    keep: config.options.episodeLimit === 0 ? 'all' : String(config.options.episodeLimit), startEarly: config.options.beforeSeconds / 60, endLate: config.options.afterSeconds / 60});
}

/** Preserve all undrawn rule predicates, owner/source and duration precision.
 * The genuine original revision remains with this editor; no stale rebase. */
export function editedRuleConfig(rule: RecordingRule, settings: RuleSettings): RuleConfig {
  const original = rule.config;
  const seconds = (value: number, current: number) => value === current / 60 ? current : value * 60;
  const config: RuleConfig = {...original, enabled: settings.enabled, episodes: settings.episodes,
    allowedChannels: [...original.allowedChannels], blockedChannels: [...original.blockedChannels], keywords: [...original.keywords], blockedKeywords: [...original.blockedKeywords],
    options: {...original.options, episodeLimit: settings.keep === 'all' ? 0 : Number(settings.keep),
      beforeSeconds: seconds(settings.startEarly, original.options.beforeSeconds), afterSeconds: seconds(settings.endLate, original.options.afterSeconds)}};
  if (settings.channel !== undefined) {
    if (config.matchKind === 'title') {if (settings.channel !== 'this') throw invalid();}
    else if (settings.channel === 'any') config.allowedChannels = [];
    else {
      const id = rule.summary?.channel.id;
      if (!id) throw invalid();config.allowedChannels = [id];
    }
  }
  if (typeof config.enabled !== 'boolean' || !['all', 'new'].includes(config.episodes) || !validRecordingOptions(config.options)) throw invalid();
  return config;
}

/** Actual DVR client reads/receipt-backed commands. Fixture projection is not
 * imported here. Only a mounted caller starts a bounded cursor request. */
export function createSeriesRulesBindings(context: ScreenContext, env: Environment) {
  const life = screenLifetime(context), listeners = new Set<() => void>(), entries = new Map<string, RuleEntry>();
  const requestId = env.requestId ?? (async () => Array.from(crypto.getRandomValues(new Uint8Array(24)), value => value.toString(16).padStart(2, '0')).join(''));
  const now = env.now ?? Date.now;
  let offIdentity: (() => void) | undefined;
  let closed = false, generation = 0, pending: Promise<void> | undefined, previewSerial = 0, previewClient: DVRClient | undefined;
  let page: DVRPage | undefined, summary: DVRSummary | undefined, storage: RecordingStorage | undefined, sources: GuideSourceSummaries | undefined;
  let state: SeriesRulesSnapshot = Object.freeze({phase: 'idle', loadingMore: false, mutating: false, previewLoading: false});
  const clients = new Set<DVRClient>();
  const api = {async request<T>(...args: Parameters<typeof life.api.request>) {
    try {return await life.api.request<T>(...args);}
    catch (error) {if (isAccessError(error)) dispose(error);throw error;}
  }};
  const client = new DVRClient(api, context.scope.serverId, requestId);clients.add(client);
  const publish = (patch: Partial<SeriesRulesSnapshot>) => {if (closed) return;state = Object.freeze({...state, ...patch});for (const listener of [...listeners]) listener();};
  const current = () => {if (closed) return false;if (!life.current()) {dispose();return false;}return true;};
  const assert = () => {if (!current()) throw cancelled();};
  const formatTime = (start: string) => new Intl.DateTimeFormat(env.locale, {hour: 'numeric', minute: '2-digit', timeZone: env.timezone}).format(Date.parse(start));
  const airing = (programme: GuideProgramme, rule: RecordingRule): RuleAiring => {
    const at = Date.parse(programme.start), today = localDayStart(at, env.timezone) === localDayStart(now(), env.timezone);
    const episode = programme.episode?.display ?? (programme.episode?.season !== undefined && programme.episode.number !== undefined
      ? defaultI18n.t('downloads.title.episode', {season: programme.episode.season, episode: programme.episode.number}) : undefined);
    return Object.freeze({id: programme.id, time: formatTime(programme.start), day: today ? defaultI18n.t('guide.today')
      : new Intl.DateTimeFormat(env.locale, {weekday: 'long', timeZone: env.timezone}).format(at), channelId: programme.channelId,
      ...(rule.summary?.channel.id === programme.channelId ? {channel: rule.summary.channel.name} : {}), ...(episode ? {episode} : {}),
      title: programme.subtitle ?? programme.title, isNew: programme.newEvidence === 'new',
      ...(programme.recordingState ? {scheduled: programme.recordingState === 'scheduled'} : {})});
  };
  const model = (rule: RecordingRule): SeriesRule => {
    const next = rule.summary?.next, channel = rule.summary?.channel, conflict = next?.recordingState ? next.recordingState === 'conflicted' : undefined;
    return freeze({id: rule.id, title: rule.config.name, glyph: Array.from(rule.config.name)[0] ?? '', channelId: channel?.id ?? '',
      ...(channel?.name ? {channelName: channel.name} : {}), actualCounts: true,
      ...(rule.summary ? {recorded: rule.summary.recorded, upcomingCount: rule.summary.upcoming, matchingTonight: rule.summary.matchingTonight, conflict} : {}),
      next: next ? conflict ? defaultI18n.t('next.live.nextConflict', {time: formatTime(next.start)})
        : channel?.id === next.channelId && channel.name && rule.config.allowedChannels.length === 0
          ? defaultI18n.t('next.live.nextOn', {time: formatTime(next.start), channel: channel.name}) : defaultI18n.t('next.live.next', {time: formatTime(next.start)}) : '', settings: ruleSettings(rule)});
  };
  const project = (): SeriesRulesView => {
    const available = sources?.sources.filter(source => source.availableEnd !== ''), ends = available?.map(source => Date.parse(source.availableEnd));
    const end = ends?.length && ends.every(Number.isFinite) ? Math.min(...ends) : undefined;
    return Object.freeze({sourceName: context.identity?.serverName ?? '', fromServer: true,
      ...(sources ? {channelCount: sources.sources.reduce((total, source) => total + source.channelCount, 0)} : {}),
      ...(end === undefined ? {} : {guideThrough: new Intl.DateTimeFormat(env.locale, {weekday: 'long', timeZone: env.timezone}).format(end - 1)}),
      ...(summary ? {upcomingCount: summary.upcoming, recordedCount: summary.recorded, ruleCount: summary.rules, conflictCount: summary.conflicts} : {}),
      ...(storage ? {storage: Object.freeze({used: Math.round(storage.measurement.usedBytes / 1e9), free: Math.round(storage.measurement.freeBytes / 1e9), hoursLeft: storage.forecastHours})} : {}),
      rules: Object.freeze([...entries.values()].map(entry => entry.model))});
  };
  const cancelPreview = () => {previewSerial++;previewClient?.dispose();if (previewClient) clients.delete(previewClient);previewClient = undefined;publish({preview: undefined, previewError: undefined, previewLoading: false});};
  const load = (more = false): Promise<void> => {
    assert();if (pending) return pending;
    if (more && (!page?.nextCursor || state.phase !== 'ready')) return Promise.resolve();
    const epoch = generation, cursor = more ? page!.nextCursor : '';
    if (!more) {generation++;cancelPreview();publish({phase: 'loading', error: undefined});}
    const ticket = more ? epoch : generation;
    const task = Promise.resolve().then(async () => {
      try {
        assert();
        if (!more) [summary, storage, sources] = await Promise.all([client.summary(), client.storage(), fetchGuideSourceSummaries(api, context.scope.serverId, env.timezone, life.signal())]);
        const result = await client.list('rules', cursor);assert();if (ticket !== generation) throw cancelled();
        if (more && (result.revision !== page?.revision || result.rules.some(rule => entries.has(rule.id)) || result.nextCursor && result.nextCursor === cursor)) throw invalid();
        if (!more) entries.clear();
        for (const rule of result.rules) {const frozen = freeze(rule);entries.set(rule.id, {rule: frozen, model: model(frozen)});}
        page = result;publish({phase: 'ready', view: project(), nextCursor: result.nextCursor || undefined, loadingMore: false, error: undefined});
      } catch (error) {if (current() && ticket === generation) publish({phase: state.view ? 'ready' : 'error', loadingMore: false, error});throw error;}
      finally {if (pending === task) pending = undefined;}
    });
    pending = task;if (more) publish({loadingMore: true, error: undefined});return task;
  };
  const captured = (row: SeriesRule) => {
    assert();const entry = entries.get(row.id);if (!entry || entry.model !== row || state.phase !== 'ready') throw cancelled();return entry.rule;
  };
  const mutate = async (row: SeriesRule, work: (rule: RecordingRule) => Promise<unknown>): Promise<boolean> => {
    if (state.mutating || pending) return false;
    const rule = captured(row), ticket = generation;cancelPreview();publish({mutating: true, error: undefined});
    try {await work(rule);assert();if (ticket !== generation) throw cancelled();await load();return true;}
    catch (error) {if (current()) publish({error});return false;}
    finally {publish({mutating: false});}
  };
  function dispose(reason?: unknown) {
    if (closed) return;
    closed = true;offIdentity?.();generation++;previewSerial++;life.dispose();for (const owner of clients) owner.dispose();clients.clear();entries.clear();
    page = undefined;summary = undefined;storage = undefined;sources = undefined;
    state = Object.freeze({phase: 'retired', loadingMore: false, mutating: false, previewLoading: false, ...(reason === undefined ? {} : {error: reason})});
    try {for (const listener of [...listeners]) listener();} finally {listeners.clear();}
  }
  offIdentity = context.subscribeIdentity?.(() => {if (current() && state.view) publish({view: project()});});
  return {getSnapshot: () => {if (!closed && !life.current()) dispose();return state;}, subscribe: (listener: () => void) => {if (closed) return () => {};listeners.add(listener);return () => {listeners.delete(listener);};},
    load: () => load(), more: () => load(true),
    save: (row: SeriesRule, settings: RuleSettings) => mutate(row, rule => client.saveRule(rule.id, rule.revision, editedRuleConfig(rule, settings))),
    remove: (row: SeriesRule, future: 'keep' | 'cancel') => mutate(row, rule => client.deleteRule(rule, future)),
    cancelPreview,
    async preview(row: SeriesRule, settings: RuleSettings) {
      const rule = captured(row);cancelPreview();const serial = previewSerial, ticket = generation;
      const owner = new DVRClient(api, context.scope.serverId, requestId);previewClient = owner;clients.add(owner);publish({previewLoading: true});
      try {
        const result = await owner.preview(editedRuleConfig(rule, settings));assert();
        if (serial !== previewSerial || ticket !== generation || entries.get(row.id)?.model !== row) throw cancelled();
        publish({previewLoading: false, preview: Object.freeze({ruleId: row.id, settings: freeze({...settings}), count: result.matches,
          airings: Object.freeze(result.sample.map(programme => airing(programme, rule)))})});
      } catch (error) {if (current() && serial === previewSerial) publish({previewLoading: false, previewError: error});}
      finally {owner.dispose();clients.delete(owner);if (previewClient === owner) previewClient = undefined;}
    }, dispose};
}
export type SeriesRulesBindings = ReturnType<typeof createSeriesRulesBindings>;
