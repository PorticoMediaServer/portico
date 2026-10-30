import type {I18n} from '../../../../../packages/i18n/src';
import {seriesRuleConfig, type Occurrence, type RecordingDraft, type RecordingOptions, type RecordingRule, type RuleConfig, type RuleSummary} from '../../../../../packages/client-core/src/dvr';

/** The real keep policy a rule stores. The administration document names three modes, and
 * the server maps them onto the two independent DVR limits: keep-count is the episode limit,
 * keep-days is the retention window, and either zero means indefinite. Both may be non-zero
 * on a rule written straight through the DVR API; the pair is reported as it is stored. */
export type RuleKeep = Readonly<{mode: 'all' | 'count' | 'days' | 'both'; count: number; days: number}>;
export function ruleKeep(options: Pick<RecordingOptions, 'episodeLimit' | 'retentionDays'>): RuleKeep {
  const count = options.episodeLimit > 0 ? options.episodeLimit : 0, days = options.retentionDays > 0 ? options.retentionDays : 0;
  if (count && days) return {mode: 'both', count, days};
  if (count) return {mode: 'count', count, days: 0};
  if (days) return {mode: 'days', count: 0, days};
  return {mode: 'all', count: 0, days: 0};
}

/** The drawn list on TV frame 30: keep every episode, then the latest episode and 3, 5, 10, 20. */
export const keepChoices: readonly number[] = [0, 1, 3, 5, 10, 20];

export function keepLabel(i18n: Pick<I18n, 't'>, keep: RuleKeep): string {
  const days = keep.days ? i18n.t('recordings.keepDaysValue', {count: keep.days}) : undefined;
  if (keep.mode === 'days' && days) return days;
  const count = keep.count ? i18n.t('recordings.keepLatest', {count: keep.count}) : i18n.t('recordings.keepAll');
  return days ? `${count} · ${days}` : count;
}

/** The picker values are the drawn count choices. A rule whose real policy is keep-days keeps
 * one extra row so the side sheet shows the policy it actually has instead of a false choice. */
export type KeepChoice = Readonly<{value: string; label: string}>;
export function keepChoiceList(i18n: Pick<I18n, 't'>, options: Pick<RecordingOptions, 'episodeLimit' | 'retentionDays'>): readonly KeepChoice[] {
  const keep = ruleKeep(options), drawn = keepChoices.map(count => ({value: `count:${count}`, label: count ? i18n.t('recordings.keepLatest', {count}) : i18n.t('recordings.keepAll')}));
  return keep.days ? [...drawn, {value: `days:${keep.days}`, label: i18n.t('recordings.keepDaysValue', {count: keep.days})}] : drawn;
}

export function keepChoiceValue(options: Pick<RecordingOptions, 'episodeLimit' | 'retentionDays'>): string {
  const keep = ruleKeep(options);
  return keep.mode === 'days' || keep.mode === 'both' ? `days:${keep.days}` : `count:${keep.count}`;
}

/** Choosing a drawn row writes exactly one limit, so an older days policy cannot linger beside
 * a new count and quietly enforce the shorter of the two. */
export function withKeep(options: RecordingOptions, choice: string): RecordingOptions {
  const [kind, raw] = choice.split(':'), value = Number(raw);
  if (!Number.isSafeInteger(value) || value < 0) return {...options};
  if (kind === 'count') return {...options, episodeLimit: value, retentionDays: 0};
  if (kind === 'days') return {...options, episodeLimit: 0, retentionDays: value};
  return {...options, episodeLimit: 0, retentionDays: 0};
}

/** A rule follows any channel unless it is tied to one, by scope list, title match or channel id.
 * The summary names the channel the next airing is on, which is that tied channel. */
export function ruleScope(rule: Pick<RecordingRule, 'config'>, channel: string | undefined, i18n: Pick<I18n, 't'>): string {
  const tied = rule.config.allowedChannels.length > 0 || rule.config.matchKind === 'title' || !!rule.config.channelId;
  return tied ? channel || i18n.t('guide.thisChannel') : i18n.t('guide.anyChannel');
}

export type RuleNext = Readonly<{text: string; conflict: boolean}>;
/** Some ICU builds put a narrow no-break space before the day period; the screen prints one space. */
const plainSpace = (value: string) => value.replace(/[\u00a0\u202f]/g, ' ');
/** Frame 24: "Next: today, 4:15 PM on Atlas Documentaries", in red when that rule has a tuner conflict. */
export function ruleNext(summary: RuleSummary | undefined, i18n: I18n, now: number): RuleNext | undefined {
  if (!summary?.next) return undefined;
  const start = Date.parse(summary.next.start);
  if (!Number.isFinite(start)) return undefined;
  const time = plainSpace(i18n.time(start)), same = new Date(start).toDateString() === new Date(now).toDateString();
  const when = same ? i18n.t('recordings.nextToday', {time}) : i18n.t('recordings.nextOtherDay', {date: plainSpace(i18n.formatDate(start, {month: 'short', day: 'numeric'})), time});
  const conflict = summary.conflicts > 0;
  return {text: conflict ? i18n.t('recordings.ruleNextConflict', {when}) : i18n.t('recordings.ruleNext', {when, channel: summary.channel?.name ?? ''}), conflict};
}

/** The three facts the drawn rule row and sheet print, straight from the rule summary. */
export function ruleFacts(summary: RuleSummary | undefined): Readonly<{upcoming: number; recorded: number; tonight: number}> {
  return {upcoming: summary?.upcoming ?? 0, recorded: summary?.recorded ?? 0, tonight: summary?.matchingTonight ?? 0};
}

/** The drawn Record series form (frame 29). Its defaults are the owner's Recording defaults. */
export type SeriesForm = Readonly<{episodes: 'new' | 'all'; anyChannel: boolean; keep: string; beforeSeconds: number; afterSeconds: number}>;
export function seriesFormDefaults(document: {settings: {prePaddingMs: number; postPaddingMs: number; keepPolicy: {mode: string; keepCount: number; keepDays: number}}} | undefined): SeriesForm {
  const settings = document?.settings, policy = settings?.keepPolicy;
  const keep = policy?.mode === 'keep-count' ? `count:${policy.keepCount}` : policy?.mode === 'keep-days' ? `days:${policy.keepDays}` : 'count:0';
  return {episodes: 'new', anyChannel: false, keep, beforeSeconds: Math.max(0, Math.round((settings?.prePaddingMs ?? 0) / 1000)), afterSeconds: Math.max(0, Math.round((settings?.postPaddingMs ?? 0) / 1000))};
}

/** The form and the guide's Record series sheet build the same rule through `seriesRuleConfig`;
 * the chosen keep row then writes exactly one limit, as the rule editor does. */
export function seriesConfig(draft: RecordingDraft, form: SeriesForm): {config: RuleConfig; anchor: Occurrence} {
  const count = form.keep.startsWith('count:') ? Number(form.keep.slice(6)) : 0;
  const built = seriesRuleConfig(draft, {episodes: form.episodes, keep: Number.isSafeInteger(count) && count > 0 ? count : 0, beforeSeconds: form.beforeSeconds, afterSeconds: form.afterSeconds, anyChannel: form.anyChannel});
  return {config: {...built.config, options: withKeep(built.config.options, form.keep)}, anchor: built.anchor};
}
