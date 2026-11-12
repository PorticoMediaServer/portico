import {serverNow} from '@core/server-clock.ts';
import {bookRecording, endRecording, markChannel, readProgramme, readReminders as readServerReminders, saveRule, setReminder,
  type DvrRecording} from '@core/live-tv.ts';
import type {DvrRuleSave} from '../../../../packages/contracts/src/next/api.ts';
import type {GuideChannelRow, GuideProgram} from '@core/guide/index.ts';
import {defaultI18n} from '@i18n';
import {replaceReminders} from './reminders';
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
  recording?: DvrRecording;
  /** A cancel this panel confirmed; the guide's own mark arrives with the next guide read. */
  recordingRemoved?: boolean;
  favorite: boolean;
  preview?: Readonly<{matches: number; airings: readonly PreviewAiring[]}>;
  previewLoading: boolean;
  busy?: 'record' | 'series' | 'cancel' | 'favorite';
  notice?: 'recorded' | 'series' | 'cancelled';
  error?: string;
}>;

type Environment = Readonly<{timezone: string; locale: string; now?: () => number; onChanged?: () => void}>;

const cancelled = () => new DOMException('Programme selection changed', 'AbortError');
/** The server's own sentence for a refusal (every tuner busy, not allowed to record), or a plain line. */
const refusalText = (error: unknown) => {
  const message = (error as {message?: unknown} | null)?.message;
  return typeof message === 'string' && message.trim() && message.length <= 300 ? message : defaultI18n.t('next.live.actionFailed');
};

/** The same rule shape the guide's Record series action sends: a provider series id when the
 * guide has one, otherwise the real title-and-channel match the server accepts. */
/** A series rule made from this programme with the editor's choices (definition 11: new episodes only or all, how many to
 * keep, start early and end late, this channel or any). */
export function seriesRule(programmeId: string, choice: SeriesChoice): DvrRuleSave {
  return {programme: programmeId, newOnly: choice.episodes === 'new', ...(choice.keep === 'all' ? {} : {keepNewest: Number(choice.keep)}),
    ...(choice.anyChannel ? {anyChannel: true} : {}), ...(choice.startEarly ? {padBeforeMs: choice.startEarly * 60_000} : {}),
    ...(choice.endLate ? {padAfterMs: choice.endLate * 60_000} : {})};
}

/**
 * The selected programme's recording facts and commands on the new server. Construction performs no read; the mounted panel
 * calls `load`, which reads the programme: whether it is booked, has a reminder, may be recorded, and its other airings.
 */
export function createProgrammeBindings(context: ScreenContext, row: GuideChannelRow, programme: GuideProgram | undefined, env: Environment) {
  const life = screenLifetime(context), listeners = new Set<() => void>();
  const now = env.now ?? serverNow;
  let closed = false, started = false, canRecord = row.recordAvailable;
  let state: ProgrammeSnapshot = Object.freeze({phase: 'idle', favorite: row.favorite, previewLoading: false});
  const hidden = (row.native as {hidden?: unknown} | undefined)?.hidden === true;

  const current = () => !closed && life.current();
  const assert = () => { if (!current()) throw cancelled(); };
  const publish = (patch: Partial<ProgrammeSnapshot>) => {
    if (closed) return;
    state = Object.freeze({...state, ...patch});
    for (const listener of [...listeners]) listener();
  };
  const recordable = () => !!programme && canRecord && programme.end > now();
  const time = (at: number) => new Intl.DateTimeFormat(env.locale, {hour: 'numeric', minute: '2-digit', timeZone: env.timezone}).format(at);
  const airing = (id: string, start: number, end: number, channelId: string): PreviewAiring => Object.freeze({id, start, time: time(start),
    range: defaultI18n.t('guide.cellTime', {start: time(start), end: time(end)}),
    day: new Date(start).toDateString() === new Date(now()).toDateString() ? defaultI18n.t('guide.today') : new Intl.DateTimeFormat(env.locale, {weekday: 'long', timeZone: env.timezone}).format(start),
    title: programme?.subtitle || programme?.title || '', channelId});

  return {
    getSnapshot: () => state,
    subscribe(listener: () => void) { if (closed) return () => {};listeners.add(listener);return () => {listeners.delete(listener);}; },
    async load(): Promise<void> {
      assert();
      if (started || !programme) { if (!programme) publish({phase: 'ready'}); return; }
      started = true;
      publish({phase: 'loading', error: undefined, previewLoading: true});
      try {
        const [detail, reminders] = await Promise.all([readProgramme(life.api, programme.id, life.signal()), readServerReminders(life.api, undefined, life.signal()).catch(() => undefined)]);
        assert();
        canRecord = detail.canRecord && row.recordAvailable;
        if (reminders) replaceReminders(reminders.items.map(item => ({programId: item.programme, channelId: item.channel, title: item.title, channelName: row.name, start: Date.parse(item.startsAt)})));
        // More airings are the programme's own other airings, as the server listed them.
        const airings = detail.airings.map(one => airing(one.id, Date.parse(one.startsAt), Date.parse(one.endsAt), one.channel))
          .filter(one => one.id !== programme.id && one.start > now()).sort((left, right) => left.start - right.start);
        publish({phase: 'ready', favorite: detail.channel.favorite, previewLoading: false, ...(detail.recording ? {recording: detail.recording} : {}),
          preview: Object.freeze({matches: airings.length, airings: Object.freeze(airings.slice(0, 5))})});
      } catch (error) {
        if (!current()) return;
        publish({phase: 'ready', previewLoading: false, error: refusalText(error)});
      }
    },
    /** The editor's choices change what a rule would record, not which airings exist: nothing to read again. */
    preview: (_choice: SeriesChoice) => Promise.resolve(),
    async record(): Promise<boolean> {
      if (!programme || !recordable() || state.busy) return false;
      publish({busy: 'record', error: undefined, notice: undefined});
      try {
        const recording = await bookRecording(life.api, {programme: programme.id}, life.signal());
        assert();publish({recording, recordingRemoved: false, notice: 'recorded'});env.onChanged?.();
        return true;
      } catch (error) { if (current()) publish({error: refusalText(error)});return false; }
      finally { publish({busy: undefined}); }
    },
    async recordSeries(choice: SeriesChoice): Promise<boolean> {
      if (!programme || !recordable() || state.busy) return false;
      publish({busy: 'series', error: undefined, notice: undefined});
      try {
        await saveRule(life.api, seriesRule(programme.id, choice), false, life.signal());
        assert();publish({notice: 'series'});env.onChanged?.();
        return true;
      } catch (error) { if (current()) publish({error: refusalText(error)});return false; }
      finally { publish({busy: undefined}); }
    },
    async cancel(): Promise<boolean> {
      if (!state.recording || state.busy) return false;
      publish({busy: 'cancel', error: undefined, notice: undefined});
      try {
        await endRecording(life.api, state.recording.id, 'cancel', life.signal());
        assert();publish({recording: undefined, recordingRemoved: true, notice: 'cancelled'});env.onChanged?.();
        return true;
      } catch (error) { if (current()) publish({error: refusalText(error)});return false; }
      finally { publish({busy: undefined}); }
    },
    async toggleFavorite(): Promise<boolean> {
      if (state.busy) return false;
      const favorite = !state.favorite;
      publish({busy: 'favorite', error: undefined});
      try {
        await markChannel(life.api, row.id, {favorite, hidden}, life.signal());
        assert();publish({favorite, busy: undefined});env.onChanged?.();
        return true;
      } catch (error) { if (current()) publish({busy: undefined, error: refusalText(error)});return false; }
    },
    /** A reminder kept by the server for this profile; the app shows it when it comes due. */
    async remind(programmeId: string, on: boolean): Promise<void> {
      assert();
      await setReminder(life.api, programmeId, on, life.signal());
    },
    dispose() {
      if (closed) return;
      closed = true;life.dispose();listeners.clear();
    },
  };
}
export type ProgrammeBindings = ReturnType<typeof createProgrammeBindings>;
