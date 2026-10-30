import {MetadataFacts} from '../../../../kit/MetadataFacts';
import {useEffect, useState, useSyncExternalStore} from 'react';
import {defaultI18n, type MessageId} from '@i18n';
import type {Library} from '@core/index.ts';
import type {ChannelApi} from '@core/channel-guide.ts';
import {ChannelPins} from './ChannelPins';
import {ChannelConditions} from './ChannelConditions';
import {ChannelHours} from './ChannelHours';
import type {BlockPresetsSource} from './block-presets-source';
import {applyChannelConditions} from '@core/server-admin/channel-conditions.ts';
import {librarySampleLabel, type LibraryChannelConfig, type LibraryChannelRule} from '@core/library-channels.ts';
import {Button, RichSelect, Switch, TextField} from '../../../../kit/controls';
import {Dialog} from '../../../../kit/overlays';
import {Artwork} from '../../../../kit/artwork/Artwork';
import type {ArtworkSource} from '../../../../kit/artwork/types';
import type {SettingsChannelEditorSource} from './channel-editor-source';
import styles from './channels.module.css';

const t = defaultI18n.t;
function channelClock(at: number, timeZone: string): string {try {return new Intl.DateTimeFormat(undefined, {timeZone, hour: 'numeric', minute: '2-digit'}).format(at);} catch {return '—';}}
const modes = ['sequential', 'shuffle-bag', 'weighted-random', 'sequential-then-shuffle'] as const;
const modeWords: readonly MessageId[] = ['builder.order.inOrder', 'builder.order.shuffle', 'builder.order.random', 'builder.order.thenShuffle'];
const orders = ['title', 'recent', 'oldest', 'year', 'episode', 'release', 'rating'] as const;
const episodes = ['none', 'in-order', 'marathon', 'randomized', 'rotate'] as const;
const episodeWords: readonly MessageId[] = ['builder.episodes.none', 'builder.episodes.inOrder', 'builder.episodes.marathon', 'builder.episodes.randomized', 'builder.episodes.rotate'];

export function ChannelEditor({source, api, reportError, openPresets, presets, libraries, isCurrent, close, creating, sampleArt}: Readonly<{creating: boolean; sampleArt?: (itemId: string) => ArtworkSource | undefined; source: SettingsChannelEditorSource; openPresets: () => void; presets: BlockPresetsSource; api: ChannelApi; reportError: (error: unknown) => void; libraries: readonly Library[]; isCurrent: () => boolean; close: () => void}>) {
  const state = useSyncExternalStore(source.subscribe, source.getSnapshot, source.getSnapshot);
  const presetState = useSyncExternalStore(presets.subscribe, presets.getSnapshot, presets.getSnapshot);
  const [advanced, setAdvanced] = useState(false), [tried, setTried] = useState(false);
  const [validation] = useState(() => ({conditions: true, hours: true})), [conditionsValid, setConditionsValid] = useState(true), [hoursEditing, setHoursEditing] = useState(false);
  const config = state.config, rule = config?.rules.find(row => row.id === config.defaultRuleId);
  const video = libraries.filter(library => ['movie', 'tv', 'anime'].includes(library.kind));
  const current = () => isCurrent() && source.getSnapshot().phase === 'editing' && source.getSnapshot().config === config;
  const locked = state.busy || state.pending;
  const change = (update: (value: LibraryChannelConfig) => LibraryChannelConfig) => {if (!current() || locked) return false;source.change(update);return source.getSnapshot().config !== config;};
  const changeRule = (patch: Partial<LibraryChannelRule>) => {if (rule) change(value => ({...value, rules: value.rules.map(row => row.id === rule.id ? {...row, ...patch} : row)}));};
  const query = (patch: Partial<LibraryChannelRule['query']>) => {if (rule) changeRule({query: {...rule.query, ...patch}});};
  let zoneValid = false;
  try {if (config?.timezone) {new Intl.DateTimeFormat(undefined, {timeZone: config.timezone});zoneValid = true;}} catch { /* The entered zone stays editable. */ }
  const valid = !!config?.name.trim() && !!rule?.query.libraryIds.length && !!rule?.query.kinds.length && zoneValid;
  useEffect(() => {
    if (!config || !rule?.query.libraryIds.length || state.pending || !isCurrent() || !validation.conditions || !validation.hours) return;
    const timer = setTimeout(() => {if (isCurrent() && validation.conditions && validation.hours && source.getSnapshot().config === config) void source.preview();}, 600);
    return () => clearTimeout(timer);
  }, [config, rule?.query.libraryIds.length, state.pending, source, isCurrent, conditionsValid, hoursEditing, validation]);
  const save = () => {if (!current() || state.busy) return;setTried(true);if (valid && validation.conditions && validation.hours) void (state.pending ? source.retry() : source.save());};
  const preview = state.preview, count = preview?.rules.reduce((sum, item) => sum + item.eligible, 0) ?? 0;
  const unresolved = preview?.rules.reduce((sum, item) => sum + item.unresolved, 0) ?? 0;
  const title = state.templateName ? t('builder.reviewTemplate', {name: state.templateName}) : creating ? t('builder.create') : t('builder.editTitle');
  return <Dialog responsive={false} className={styles.editorDialog} headerClassName={styles.editorHeader} footerClassName={styles.editorFooter} open={state.phase !== 'closed' && isCurrent()} title={title} width={960} onOpenChange={value => {if (!value && !source.getSnapshot().busy) close();}} bodyClassName={styles.editorBody}
    dismissOnBack={!state.busy} dismissOnOutsidePress={!state.busy} showCloseButton={!state.busy}
    footer={<>{preview && <span className={styles.footerCount}>{t(preview.complete ? 'builder.matches' : 'next.browse.countAtLeast', {count})}</span>}<Button size="compact" variant="glass" disabled={state.busy} onClick={close}>{t('builder.cancel')}</Button><Button size="compact" variant="primary" disabled={state.busy || !config} onClick={save}>{state.pending ? t('action.tryAgain') : creating ? t('builder.create') : t('builder.save')}</Button></>}>
    {state.phase === 'loading' && !state.error && <p role="status">{t('next.settings.loading')}</p>}
    {config && rule && <div className={styles.editorGrid} onFocusCapture={() => source.setQuietInputActive(true)} onBlurCapture={event => {if(!event.currentTarget.contains(event.relatedTarget))source.setQuietInputActive(false);}}>
      <fieldset className={styles.editorFields} disabled={locked}>
        <TextField label={t('builder.name')} value={config.name} maxLength={120} disabled={locked} error={tried && !config.name.trim() ? t('builder.error.name') : undefined} onChange={event => {const name = event.target.value;change(value => ({...value, name}));}}/>
        <TextField label={t('builder.description')} value={config.description} maxLength={2048} disabled={locked} onChange={event => {const description = event.target.value;change(value => ({...value, description}));}}/>
        <section className={styles.editorSection}><h3>{t('builder.whatPlays')}</h3>
          <div><b>{t('builder.libraries')}</b><div className={styles.choices}>{video.map(library => <label key={library.id}><input type="checkbox" disabled={locked} checked={rule.query.libraryIds.includes(library.id)} onChange={event => query({libraryIds: event.target.checked ? [...rule.query.libraryIds, library.id] : rule.query.libraryIds.filter(id => id !== library.id)})}/>{library.name}</label>)}</div></div>
          {!video.length && <p className={styles.help}>{t('builder.noVideoLibraries')}</p>}
          {tried && !rule.query.libraryIds.length && <p role="alert" className={styles.warning}>{t('builder.error.libraries')}</p>}
          <div><b>{t('builder.kinds')}</b><div className={styles.choices}>{(['movie', 'episode'] as const).map(kind => <label key={kind}><input type="checkbox" disabled={locked} checked={rule.query.kinds.includes(kind)} onChange={event => query({kinds: event.target.checked ? [...rule.query.kinds, kind] : rule.query.kinds.filter(value => value !== kind)})}/>{t(kind === 'movie' ? 'builder.movies' : 'builder.shows')}</label>)}</div></div>
          {tried && !rule.query.kinds.length && <p role="alert" className={styles.warning}>{t('builder.kindsHelp')}</p>}
          <ChannelConditions api={api} libraries={libraries} query={rule.query} locked={locked} isCurrent={isCurrent} reportError={reportError} onChange={(conditions, complete) => {
            if (!current() || locked) return false;
            validation.conditions = complete;source.setQuietIncomplete('conditions',!complete);setConditionsValid(complete);
            if (complete) source.change(value => applyChannelConditions(value, conditions));
            return true;
          }}/>
          <TextField label={t('builder.mentions')} value={rule.query.text ?? ''} maxLength={200} disabled={locked} onChange={event => query({text: event.target.value})}/><p className={styles.help}>{t('builder.mentionsHint')}</p>
        </section>
        <ChannelPins api={api} libraries={libraries} libraryIds={rule.query.libraryIds} ids={rule.query.includeItemIds ?? []} locked={locked} isCurrent={isCurrent} reportError={reportError} onChange={includeItemIds => query({includeItemIds})}/>
        <ChannelPins api={api} libraries={libraries} libraryIds={rule.query.libraryIds} ids={rule.query.excludeItemIds ?? []} excluded locked={locked} isCurrent={isCurrent} reportError={reportError} onChange={excludeItemIds => query({excludeItemIds})}/>
        <label className={styles.control}><span>{t('builder.order')}</span><RichSelect aria-label={t('builder.order')} value={rule.mode} disabled={locked} options={modes.map((mode, index) => ({value: mode, label: t(modeWords[index]!)}))} onValueChange={value => changeRule({mode: value as LibraryChannelRule['mode']})}/></label>
        {rule.mode === 'sequential' && <label className={styles.control}><span>{t('builder.sortBy')}</span><RichSelect aria-label={t('builder.sortBy')} value={rule.query.order} disabled={locked} options={[...orders.map(order => ({value: order, label: t(`builder.sort.${order}` as MessageId)})),...(presetState.applicationOrders?.includes('least-played') || rule.query.order === 'least-played' ? [{value:'least-played',label:t('builder.preset.leastPlayed')}] : [])]} onValueChange={value => query({order: value as LibraryChannelRule['query']['order']})}/></label>}
        {rule.query.kinds.includes('episode') && <label className={styles.control}><span>{t('builder.episodes')}</span><RichSelect aria-label={t('builder.episodes')} value={rule.episodeMode} disabled={locked} options={episodes.map((mode, index) => ({value: mode, label: t(episodeWords[index]!)}))} onValueChange={value => changeRule({episodeMode: value as LibraryChannelRule['episodeMode']})}/></label>}
        <TextField label={t('builder.recent')} type="number" min={0} max={36500} value={rule.query.recentDays} disabled={locked} onChange={event => query({recentDays: Number(event.target.value)})}/>
        <label className={styles.control}><span>{t('builder.on')}</span><Switch aria-label={t('builder.on')} checked={config.enabled} disabled={locked} onCheckedChange={enabled => change(value => ({...value, enabled}))}/></label>
        <ChannelHours presets={presets} openPresets={openPresets} config={config} locked={locked} isCurrent={isCurrent} onChange={change} onEditing={editing => {if (isCurrent()) {validation.hours = !editing;source.setQuietIncomplete('hours',editing);setHoursEditing(editing);}}}/>
        <p className={styles.help}>{t('builder.audience')}</p>
        <Button size="small" variant="text" onClick={() => setAdvanced(value => !value)}>{t(advanced ? 'builder.hideAdvanced' : 'builder.advanced')}</Button>
        {advanced && <section className={styles.editorSection}>
          <label className={styles.control}><span>{t('builder.whenDone')}</span><RichSelect aria-label={t('builder.whenDone')} value={rule.exhaustion} disabled={locked} options={(['loop', 'slate'] as const).map(value => ({value, label: t(`builder.whenDone.${value}`)}))} onValueChange={value => changeRule({exhaustion: value as LibraryChannelRule['exhaustion']})}/></label>
          <TextField label={t('builder.avoidRepeats')} type="number" min={0} max={1000} value={rule.deduplicationWindow} disabled={locked} onChange={event => changeRule({deduplicationWindow: Number(event.target.value)})}/><p className={styles.help}>{t('builder.avoidRepeatsHelp')}</p>
          <label className={styles.control}><span>{t('builder.quality')}</span><RichSelect aria-label={t('builder.quality')} value={config.quality.mode} disabled={locked} options={(['automatic', 'original', 'limited'] as const).map(value => ({value, label: t(`builder.quality.${value}`)}))} onValueChange={mode => change(value => ({...value, quality: {...value.quality, mode: mode as LibraryChannelConfig['quality']['mode']}}))}/></label>
          <TextField label={t('builder.timeZone')} value={config.timezone} disabled={locked} error={tried && !zoneValid ? t('builder.error.timeZone') : undefined} onChange={event => {const timezone = event.target.value;change(value => ({...value, timezone}));}}/>
        </section>}
      </fieldset>
      <aside className={styles.preview} aria-busy={state.previewLoading}><h3>{t('builder.preview')}</h3>
        {state.previewLoading && <p role="status">{t('builder.previewing')}</p>}
        {!preview && !state.previewLoading && <p className={styles.help}>{t('builder.previewEmpty')}</p>}
        {preview && conditionsValid && !hoursEditing && <><b>{t(preview.complete ? 'builder.matches' : 'next.browse.countAtLeast', {count})}</b>{preview.durationMs > 0 && <p>{t('builder.hours', {hours: Number((preview.durationMs / 3600000).toFixed(1))})}</p>}{unresolved > 0 && <p>{t('builder.unresolved', {count: unresolved})}</p>}
          {preview.complete && count === 0 && <p className={styles.help}>{t('builder.previewNoMatches')}</p>}
          <h4>{t('builder.samples')}</h4><ul className={styles.sampleList}>{preview.rules.flatMap(item => item.sample.map(sample => <li key={`${item.ruleId}/${sample.itemId}`}><Artwork source={sampleArt?.(sample.itemId)} alt="" width={28} height={42}/><span>{librarySampleLabel(sample)}</span></li>))}</ul>
          {!!preview.firstDay.length && <><h4>{t('builder.firstDay')}</h4><ul>{preview.firstDay.map(entry => <li key={entry.id}><MetadataFacts parts={[channelClock(entry.startMs, config.timezone), entry.itemId ? entry.title : t('builder.nothingScheduled')]} /></li>)}</ul></>}
        </>}
      </aside>
    </div>}
    {state.error && <p role="alert" className={styles.warning}>{t(state.error === 'preview' ? 'builder.error.preview' : state.error === 'read' ? 'error.unavailable' : state.pending ? 'error.unconfirmedChange' : 'builder.error.rejected')}</p>}
  </Dialog>;
}
