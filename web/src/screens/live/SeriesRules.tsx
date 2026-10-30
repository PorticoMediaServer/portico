import {useEffect, useReducer, useRef, type ReactNode} from 'react';
import {defaultI18n} from '@i18n';
import {Button, Chip, Segmented, Select, Switch, Tabs} from '../../kit/controls';
import {Banner, SidePanel} from '../../kit/overlays';
import {Icon} from '../../kit/Icon';
import {RecordingsLayout, RecordingStorage} from '../../kit/dvr/RecordingsLayout';
import {RuleCard, RuleDecoration, RuleField, RuleGap} from '../../kit/dvr/RuleCard';
import {createRulesState, matchingAirings, matchingCount, rulesReducer, type RuleAiring, type SeriesRule, type SeriesRulesView, type RuleSettings} from './series-rules-model';
import styles from '../../kit/dvr/rules.module.css';
import live from './live.module.css';
const t = defaultI18n.t;
const keepLabel = (value: RuleSettings['keep']) => value === 'all' ? t('recordings.keepAll') : value === '1' ? t('next.live.keepLatest') : t('next.live.keepLatestCount', {count: Number(value)});
const minutes = (value: number) => value ? t('next.live.minutes', {count: value}) : t('recordings.none');
function nextLine(rule: SeriesRule): string {
  if (!rule.settings.enabled) {const count = rule.actualCounts ? rule.matchingTonight : rule.upcomingCount;return count === undefined ? '' : t('next.live.pausedMatch', {count});}
  if (rule.actualCounts) return rule.next;
  const next = matchingAirings(rule)[0];
  return next ? rule.conflict ? t('next.live.nextConflict', {time: next.time}) : rule.showNextChannel ? t('next.live.nextOn', {time: next.time, channel: next.channel}) : t('next.live.next', {time: next.time}) : rule.next;
}
/** Entirely local, typed presentation boundary. No server or platform services. */
export type SeriesRulesProps = Readonly<{view: SeriesRulesView; navigation?: ReactNode; busy?: boolean; error?: string; tail?: ReactNode;
  onGuide?: () => void; onView?: (view: 'upcoming' | 'recorded' | 'rules' | 'history' | 'storage') => void;
  onSave: (rule: SeriesRule, settings: RuleSettings) => Promise<boolean>; onDelete: (rule: SeriesRule) => Promise<boolean>;
  onDraft?: (rule: SeriesRule | undefined, settings: RuleSettings | undefined) => void;
  preview?: Readonly<{ruleId: string; settings: RuleSettings; count: number; airings: readonly RuleAiring[]}>}>;
export function LiveRecordingTabs({view, conflicts, onChange}: {view: 'guide' | 'recordings'; conflicts?: number; onChange?: (value: 'guide' | 'recordings') => void}) {
  return <Tabs className={styles.tabs} listClassName={styles.tabList} value={view} onValueChange={value => {if (value === 'guide' || value === 'recordings') onChange?.(value);}} aria-label={t('next.home.shell.live')} items={[
    {value: 'guide', label: t('channels.title')}, {value: 'recordings', label: <>{t('recordings.title')}{conflicts !== undefined && <span className={styles.conflictNote}>{t('next.live.conflicts', {count: conflicts})}</span>}</>},
  ]}/>;
}
export function SeriesRules({view, navigation, busy = false, error, tail, onGuide, onView, onSave, onDelete, onDraft, preview}: SeriesRulesProps) {
  const [state, dispatch] = useReducer(rulesReducer, view, createRulesState);
  useEffect(() => dispatch({type: 'replace', view}), [view]);
  const opener = useRef<HTMLButtonElement | null>(null);
  const cardButtons = useRef(new Map<string, HTMLButtonElement>());
  const selected = state.rules.find(rule => rule.id === state.selectedId), draft = state.draft;
  const actualPreview = selected && draft && preview?.ruleId === selected.id && JSON.stringify(preview.settings) === JSON.stringify(draft) ? preview : undefined;
  const airings = actualPreview?.airings ?? (selected && draft && !selected.actualCounts ? matchingAirings(selected, draft) : []);
  const count = actualPreview?.count ?? (selected && draft && !selected.actualCounts ? matchingCount(selected, draft) : undefined);
  const views: readonly Readonly<{id: 'upcoming' | 'recorded' | 'rules' | 'history' | 'storage'; label: string; count?: number}>[] = [
    {id: 'upcoming', label: t('recordings.upcoming'), count: view.upcomingCount}, {id: 'recorded', label: t('recordings.recorded'), count: view.recordedCount},
    {id: 'rules', label: t('recordings.rules'), count: view.ruleCount ?? (view.fromServer ? undefined : state.rules.length)},
    {id: 'history', label: t('saved.tab.history')}, {id: 'storage', label: t('recordings.storage')},
  ];
  useEffect(() => {onDraft?.(selected, draft);}, [onDraft, selected, draft]);
  const patch = (patch: Partial<RuleSettings>) => {if (!busy) dispatch({type: 'change', patch});};
  const close = () => {opener.current?.focus(); dispatch({type: 'cancel'});};
  const save = async () => {
    if (!selected || !draft || busy) return;
    if (await onSave(selected, draft)) close();
  };
  const remove = async () => {
    if (!selected || busy) return;
    const index = state.rules.findIndex(rule => rule.id === state.selectedId);
    const neighbour = state.rules[index + 1] ?? state.rules[index - 1];
    if (await onDelete(selected)) {dispatch({type: 'cancel'});if (neighbour) cardButtons.current.get(neighbour.id)?.focus();}
  };
  return <RecordingsLayout title={t('next.home.shell.live')} sourceLabel={t('channels.source')} allChannels={t('channels.all')}
    sourceName={view.sourceName} channels={view.channelCount === undefined ? undefined : t('player.info.channels', {count: view.channelCount})} through={view.guideThrough === undefined ? undefined : t('next.live.guideThrough', {day: view.guideThrough})} navigation={navigation}>
    <LiveRecordingTabs view="recordings" conflicts={view.conflictCount} onChange={value => {if (value === 'guide') onGuide?.();}}/>
    <div className={styles.recordingBar}><div className={styles.chips} aria-label={t('recordings.title')}>
      {views.map(item => <Chip className={styles.chip} key={item.id} selected={item.id === 'rules'} count={item.count} onClick={onView ? () => onView(item.id) : undefined}>{item.label}</Chip>)}
    </div>{view.storage && <RecordingStorage used={t('next.live.storageUsed', {amount: view.storage.used})} free={t('next.live.storageFree', {amount: view.storage.free})}
      remaining={t('next.live.storageLeft', {hours: view.storage.hoursLeft})} label={t('next.live.storageLabel', view.storage)} fraction={view.storage.used / (view.storage.used + view.storage.free)}/>}</div>
    <div className={styles.body}><div className={styles.rules}>{state.rules.map(rule => <RuleCard key={rule.id} title={rule.title} glyph={rule.glyph} tone={rule.tone}
      selected={selected?.id === rule.id} paused={!rule.settings.enabled} disabled={busy} options={[t(rule.settings.episodes === 'all' ? 'guide.allEpisodes' : 'guide.newOnly'), rule.settings.channel === 'any' ? t('guide.anyChannel') : rule.settings.channel === 'this' ? rule.channelName : undefined, keepLabel(rule.settings.keep)].filter((value): value is string => value !== undefined)}
      next={nextLine(rule)} conflict={rule.conflict}
      state={t(rule.settings.enabled ? 'recordings.on' : 'recordings.paused')} upcoming={rule.settings.enabled && matchingCount(rule) !== undefined ? t('next.live.upcomingCount', {count: matchingCount(rule)!}) : undefined}
      actionsLabel={t('next.live.actionsFor', {title: rule.title})} buttonRef={element => {if (element) cardButtons.current.set(rule.id, element); else cardButtons.current.delete(rule.id); if (selected?.id === rule.id) opener.current = element;}} onOpen={() => dispatch({type: 'select', id: rule.id})}/> )}</div>
      {tail}<p className={styles.hint}>{t('next.live.addRuleHint')}</p></div>
    <SidePanel open={!!selected} onOpenChange={open => {if (!open) close();}} width={600} topInset={76} className={styles.panel} returnFocusRef={opener}
      accessibilityLabel={selected ? t('next.live.editRule', {title: selected.title}) : undefined}
      title={<><span className={styles.kind}><Icon name="i-series" size={18}/>{t('next.live.seriesRule')}</span><span className={styles.title}>{selected?.title}</span></>}
      description={<>{count !== undefined && t('next.live.matchCount', {count})}{selected?.recorded !== undefined && <>{count !== undefined && <RuleGap/>}{t('next.live.recordedCount', {count: selected.recorded})}</>}</>}
      footer={<><Button className={`${styles.footerButton} ${live.footerButton}`} variant="primary" loading={busy} onClick={() => {void save();}}>{t('action.save')}</Button><Button className={`${styles.footerButton} ${live.footerButton}`} disabled={busy} onClick={close}>{t('action.cancel')}</Button><Button className={`${styles.footerButton} ${styles.delete} ${live.footerButton}`} variant="ghost" disabled={busy} onClick={() => {void remove();}}>{t('web.dvr.deleteRule')}</Button></>}>
      {selected && draft && <><RuleDecoration glyph={selected.glyph} tone={selected.tone}/>{error && <Banner>{error}</Banner>}
        <div className={styles.toggleRow}><span><label htmlFor="series-rule-enabled">{t(draft.enabled ? 'next.live.ruleOn' : 'recordings.paused')}</label><small id="series-rule-help">{t('next.live.pauseHelp')}</small></span><Switch id="series-rule-enabled" aria-describedby="series-rule-help" disabled={busy} checked={draft.enabled} onCheckedChange={enabled => patch({enabled})}/></div>
        <div className={styles.form}><RuleField label={t('guide.record')}><Segmented className={styles.segmented} aria-label={t('guide.record')} disabled={busy} value={draft.episodes} onValueChange={value => {if (value === 'all' || value === 'new') patch({episodes: value});}} items={[{value: 'new', label: t('web.dvr.newEpisodes')}, {value: 'all', label: t('guide.allEpisodes')}]}/></RuleField>
          <RuleField label={t('guide.channelColumn')}><Segmented className={styles.segmented} aria-label={t('guide.channelColumn')} disabled={busy} value={draft.channel ?? ''} onValueChange={value => {if (value === 'this' || value === 'any') patch({channel: value});}} items={[{value: 'this', label: t('guide.thisChannel')}, {value: 'any', label: t('guide.anyChannel'), disabled: draft.channelLocked}]}/></RuleField>
          <RuleField label={t('recordings.keepLabel')}><Select className={`${styles.select} ${live.editorSelect}`} aria-label={t('recordings.keepLabel')} disabled={busy} value={draft.keep} options={[...new Set(['all', '1', '3', draft.keep])].map(value => ({value, label: keepLabel(value)}))} onValueChange={value => {if (value === 'all' || Number.isSafeInteger(Number(value)) && Number(value) > 0 && Number(value) <= 100000) patch({keep: value});}}/></RuleField>
          {(['startEarly', 'endLate'] as const).map(field => <RuleField key={field} label={t(field === 'startEarly' ? 'recordings.startEarly' : 'recordings.endLate')}><Select className={`${styles.select} ${live.editorSelect}`} aria-label={t(field === 'startEarly' ? 'recordings.startEarly' : 'recordings.endLate')} disabled={busy} value={String(draft[field])} options={[...new Set([0, 2, draft[field]])].map(value => ({value: String(value), label: minutes(value)}))} onValueChange={value => patch({[field]: Number(value)})}/></RuleField>)}
        </div><h3 className={styles.comingUp}>{t('next.live.comingUp')}</h3><div>{airings.map(airing => <div className={styles.airing} key={airing.id}><span className={styles.at}>{airing.time}</span><div><b>{airing.episode && <>{airing.episode}<RuleGap/></>}{airing.title}</b><small>{airing.isNew && <span className={styles.newFlag}>{t('guide.new')}</span>}{airing.day}{airing.channel && <><RuleGap/>{airing.channel}</>}</small></div>{airing.scheduled && <span className={styles.recordDot} role="img" aria-label={t('dvr.state.scheduled')}/>}</div>)}</div>
      </>}
    </SidePanel>
  </RecordingsLayout>;
}
