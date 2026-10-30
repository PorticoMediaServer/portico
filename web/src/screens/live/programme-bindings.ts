import {DVRClient, defaultRecordingOptions, draftRule, dvrMessage, seriesRuleConfig,
  type Recording, type RuleConfig} from '@core/dvr.ts';
import {channelOperationId, saveChannelPreference, type GuideChannel, type GuideProgramme} from '@core/channel-guide.ts';
import {legacyChannel, legacyProgramme, type GuideChannelRow, type GuideProgram} from '@core/guide/index.ts';
import {defaultI18n} from '@i18n';
import {screenLifetime, type ScreenContext} from '../detail/context';

/** The editor's bounded choices, the same values the Series rules editor offers. */
export type SeriesChoice = Readonly<{episodes: 'new' | 'all'; keep: string; startEarly: number; endLate: number; anyChannel: boolean}>;
export const defaultSeriesChoice: SeriesChoice = Object.freeze({episodes: 'new', keep: 'all', startEarly: 0, endLate: 0, anyChannel: false});

/** One upcoming airing of the selected programme, as the preview actually returned it. */
export type PreviewAiring = Readonly<{id: string; start: number; time: string; range: string; day: string;
  title: string; episode?: string; channelId: string}>;

export type ProgrammeSnapshot = Readonly<{
  phase: 'idle' | 'loading' | 'ready' | 'error';
  /** The one-off recording this programme actually holds, read back from the server. */
  recording?: Recording;
  /** A cancel this panel confirmed; the guide's own mark arrives with the next guide read. */
  recordingRemoved?: boolean;
  favorite: boolean;
  preview?: Readonly<{matches: number; airings: readonly PreviewAiring[]}>;
  previewLoading: boolean;
  busy?: 'record' | 'series' | 'cancel' | 'favorite';
  notice?: 'recorded' | 'series' | 'cancelled';
  error?: string;
}>;

type Environment = Readonly<{timezone: string; locale: string; now?: () => number; requestId?: () => Promise<string>;
  operationId?: () => Promise<string>; onChanged?: () => void}>;

const randomHex = async () => Array.from(crypto.getRandomValues(new Uint8Array(24)), value => value.toString(16).padStart(2, '0')).join('');
const cancelled = () => new DOMException('Programme selection changed', 'AbortError');
const noDraft = () => Object.assign(new Error('This programme cannot be recorded.'), {code: 'programme-not-recordable'});

/** The same rule shape the guide's Record series action sends: a provider series id when the
 * guide has one, otherwise the real title-and-channel match the server accepts. */
export function previewRule(draft: Readonly<{channel: GuideChannel; programme: GuideProgramme; series: boolean}>, choice: SeriesChoice): RuleConfig {
  const config = draftRule(draft);
  config.episodes = choice.episodes;
  config.allowedChannels = choice.anyChannel && config.matchKind !== 'title' ? [] : [draft.channel.id];
  config.options = {...defaultRecordingOptions, beforeSeconds: choice.startEarly * 60, afterSeconds: choice.endLate * 60,
    episodeLimit: choice.keep === 'all' ? 0 : Number(choice.keep)};
  return config;
}

/**
 * The selected programme's real recording facts and commands. Construction performs no read;
 * the mounted panel calls `load`. Every request is fenced by the captured screen authority, and
 * a rule preview only describes airings the server returned.
 */
export function createProgrammeBindings(context: ScreenContext, row: GuideChannelRow, programme: GuideProgram | undefined, env: Environment) {
  const life = screenLifetime(context), listeners = new Set<() => void>();
  const rawChannel = legacyChannel(row), rawProgramme = programme ? legacyProgramme(programme) : undefined;
  const draft = rawChannel && rawProgramme ? Object.freeze({channel: rawChannel, programme: rawProgramme, series: false}) : undefined;
  const requestId = env.requestId ?? randomHex, operationId = env.operationId ?? (async () => channelOperationId(() => Promise.resolve(crypto.randomUUID())));
  const now = env.now ?? Date.now;
  const client = new DVRClient(life.api, context.scope.serverId, requestId);
  let closed = false, started = false, previewSerial = 0;
  let revision = rawChannel?.preferenceRevision ?? 0;
  let state: ProgrammeSnapshot = Object.freeze({phase: 'idle', favorite: row.favorite, previewLoading: false});

  const current = () => !closed && life.current();
  const assert = () => { if (!current()) throw cancelled(); };
  const publish = (patch: Partial<ProgrammeSnapshot>) => {
    if (closed) return;
    state = Object.freeze({...state, ...patch});
    for (const listener of [...listeners]) listener();
  };
  const recordable = () => !!draft && !!rawChannel?.recordAvailable && Date.parse(draft.programme.end) > now();

  const airing = (programme: GuideProgramme): PreviewAiring => {
    const start = Date.parse(programme.start), today = new Date(start).toDateString() === new Date(now()).toDateString();
    const episode = programme.episode?.display ?? (programme.episode?.season !== undefined && programme.episode.number !== undefined
      ? defaultI18n.t('downloads.title.episode', {season: programme.episode.season, episode: programme.episode.number}) : undefined);
    return Object.freeze({id: programme.id, start,
      time: new Intl.DateTimeFormat(env.locale, {hour: 'numeric', minute: '2-digit', timeZone: env.timezone}).format(start),
      range: defaultI18n.t('guide.cellTime', {
        start: new Intl.DateTimeFormat(env.locale, {hour: 'numeric', minute: '2-digit', timeZone: env.timezone}).format(start),
        end: new Intl.DateTimeFormat(env.locale, {hour: 'numeric', minute: '2-digit', timeZone: env.timezone}).format(Date.parse(programme.end))}),
      day: today ? defaultI18n.t('guide.today') : new Intl.DateTimeFormat(env.locale, {weekday: 'long', timeZone: env.timezone}).format(start),
      title: programme.subtitle || programme.title, ...(episode ? {episode} : {}), channelId: programme.channelId});
  };

  const loadPreview = async (choice: SeriesChoice): Promise<void> => {
    if (!draft || !recordable()) return;
    const serial = ++previewSerial;
    publish({previewLoading: true});
    try {
      const result = await client.preview(previewRule(draft, choice));
      assert();if (serial !== previewSerial) throw cancelled();
      const airings = result.sample.map(airing).filter(item => item.id !== draft.programme.id && item.start > now())
        .sort((left, right) => left.start - right.start);
      publish({previewLoading: false, preview: Object.freeze({matches: result.matches, airings: Object.freeze(airings.slice(0, 5))})});
    } catch (error) {
      if (!current() || serial !== previewSerial) return;
      // A preview that the server refuses only omits the drawn list; the panel itself still works.
      publish({previewLoading: false, preview: undefined});
    }
  };

  return {
    getSnapshot: () => state,
    subscribe(listener: () => void) { if (closed) return () => {};listeners.add(listener);return () => {listeners.delete(listener);}; },
    async load(): Promise<void> {
      assert();
      if (started) return;
      started = true;
      publish({phase: 'loading', error: undefined});
      const recordingId = programme?.recording?.id;
      try {
        if (recordingId) {
          const detail = await client.detail(recordingId);
          assert();publish({recording: detail.recording});
        }
        publish({phase: 'ready'});
      } catch (error) {
        if (!current()) return;
        publish({phase: 'ready', error: dvrMessage(error)});
      }
      if (current()) await loadPreview({...defaultSeriesChoice, episodes: rawProgramme && ['new', 'repeat'].includes(rawProgramme.newEvidence) ? 'new' : 'all'});
    },
    preview: (choice: SeriesChoice) => { if (!current()) return Promise.resolve();return loadPreview(choice); },
    async record(): Promise<boolean> {
      if (!draft || !recordable() || state.busy) return false;
      publish({busy: 'record', error: undefined, notice: undefined});
      try {
        const recording = await client.schedule(draft, defaultRecordingOptions);
        assert();publish({recording, recordingRemoved: false, notice: 'recorded'});env.onChanged?.();
        return true;
      } catch (error) { if (current()) publish({error: dvrMessage(error)});return false; }
      finally { publish({busy: undefined}); }
    },
    async recordSeries(choice: SeriesChoice): Promise<boolean> {
      if (!draft || !recordable() || state.busy) return false;
      publish({busy: 'series', error: undefined, notice: undefined});
      try {
        const {config, anchor} = seriesRuleConfig({...draft, series: true},
          {episodes: choice.episodes, keep: choice.keep === 'all' ? 0 : Number(choice.keep),
            beforeSeconds: choice.startEarly * 60, afterSeconds: choice.endLate * 60, anyChannel: choice.anyChannel});
        await client.saveRule(await client.newRuleID(), 0, config, anchor);
        assert();publish({notice: 'series'});env.onChanged?.();
        return true;
      } catch (error) { if (current()) publish({error: dvrMessage(error)});return false; }
      finally { publish({busy: undefined}); }
    },
    async cancel(): Promise<boolean> {
      if (!state.recording || state.busy) return false;
      publish({busy: 'cancel', error: undefined, notice: undefined});
      try {
        await client.cancel(state.recording);
        assert();publish({recording: undefined, recordingRemoved: true, notice: 'cancelled'});env.onChanged?.();
        return true;
      } catch (error) { if (current()) publish({error: dvrMessage(error)});return false; }
      finally { publish({busy: undefined}); }
    },
    async toggleFavorite(): Promise<boolean> {
      if (!rawChannel || state.busy) return false;
      const favorite = !state.favorite;
      publish({busy: 'favorite', error: undefined});
      try {
        revision = await saveChannelPreference(life.api, context.scope.serverId,
          {...rawChannel, favorite, preferenceRevision: revision}, await operationId(), {favorite, hidden: rawChannel.hidden});
        assert();publish({favorite, busy: undefined});env.onChanged?.();
        return true;
      } catch (error) { if (current()) publish({busy: undefined, error: dvrMessage(error)});return false; }
    },
    dispose() {
      if (closed) return;
      closed = true;previewSerial++;life.dispose();client.dispose();listeners.clear();
    },
  };
}
export type ProgrammeBindings = ReturnType<typeof createProgrammeBindings>;
