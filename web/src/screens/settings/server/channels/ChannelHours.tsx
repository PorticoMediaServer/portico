import {useEffect, useLayoutEffect, useState, useSyncExternalStore} from 'react';
import {defaultI18n} from '@i18n';
import type {LibraryChannelBlock, LibraryChannelConfig, LibraryChannelRule} from '@core/library-channels.ts';
import {channelRequestID} from '@core/live-source-draft.ts';
import {Button, RichSelect, TextField} from '../../../../kit/controls';
import {Dialog, Menu} from '../../../../kit/overlays';
import {channelDaySegments} from './channel-hours-layout';
import type {BlockPresetsSource} from './block-presets-source';
import styles from './channels.module.css';
const t = defaultI18n.t;
const minuteText = (minute: number) => `${String(Math.floor(minute / 60)).padStart(2,'0')}:${String(minute % 60).padStart(2,'0')}`;
const minuteValue = (text: string) => /^([01]\d|2[0-3]):[0-5]\d$/.test(text) ? Number(text.slice(0,2))*60+Number(text.slice(3)) : undefined;
const dayLabel = (day: number) => new Intl.DateTimeFormat(undefined,{weekday:'short',timeZone:'UTC'}).format(Date.UTC(2023,0,1+day));
type Draft = {moving?: boolean; original?: LibraryChannelBlock; block: LibraryChannelBlock; from: string; to: string; copiedRule?: LibraryChannelRule};
export function ChannelHours({config, locked, openPresets, presets, isCurrent, onChange, onEditing}: Readonly<{config: LibraryChannelConfig; presets: BlockPresetsSource; openPresets: () => void; locked: boolean; isCurrent: () => boolean; onChange: (change: (value: LibraryChannelConfig) => LibraryChannelConfig) => boolean; onEditing: (editing: boolean) => void}>) {
  const [draft, setDraft] = useState<Draft>(), [attempt] = useState(() => ({draft: undefined as Draft | undefined, applying: undefined as AbortController | undefined})), [error, setError] = useState(false), [applying, setApplying] = useState(false);
  const presetState = useSyncExternalStore(presets.subscribe, presets.getSnapshot, presets.getSnapshot);
  useEffect(() => {if (!presets.getSnapshot().loaded && !presets.getSnapshot().pending) void presets.load();}, [presets]);
  useLayoutEffect(() => () => {attempt.applying?.abort();attempt.draft = undefined;}, [attempt]);
  const current = () => isCurrent() && !locked;
  const open = (original?: LibraryChannelBlock, moving = false) => {
    if (!current() || attempt.draft || original && !config.blocks.includes(original)) return;
    try {
      const block = original ? structuredClone(original) : {id:'block-'+channelRequestID().slice(0,24),weekdays:[],startMinute:0,endMinute:0,priority:0,ruleId:config.defaultRuleId,fallbackRuleId:config.defaultRuleId,anchor:'channel-cursor' as const,overrun:'finish' as const};
      const next={original,moving,block,from:original ? minuteText(original.startMinute) : '',to:original ? minuteText(original.endMinute) : ''};
      attempt.draft=next;onEditing(true);setDraft(next);setError(false);
    } catch {setError(true);}
  };
  const update = (next: Draft) => {if (!current() || !draft || attempt.applying || attempt.draft !== draft) return;attempt.draft=next;setDraft(next);};
  const close = () => {if (!isCurrent() || !draft || attempt.draft !== draft) return;attempt.applying?.abort();attempt.applying=undefined;setApplying(false);attempt.draft=undefined;onEditing(false);setDraft(undefined);setError(false);};
  const apply = (remove = false) => {
    if (!current() || !draft || attempt.applying || attempt.draft !== draft) return;
    const from=minuteValue(draft.from),to=minuteValue(draft.to);
    if (!remove && (from === undefined || to === undefined || !draft.block.weekdays.length || !config.rules.some(rule => rule.id === draft.block.ruleId) && draft.copiedRule?.id !== draft.block.ruleId)) {setError(true);return;}
    const block={...draft.block,startMinute:from ?? draft.block.startMinute,endMinute:to ?? draft.block.endMinute};
    if (onChange(value => ({...value,rules:!remove && draft.copiedRule ? [...value.rules,draft.copiedRule] : value.rules,blocks:remove ? value.blocks.filter(row => row.id !== block.id) : draft.original ? value.blocks.map(row => row.id === block.id ? block : row) : [...value.blocks,block]}))) close();
  };
  const choosePreset = async (id: string) => {
    const record = presets.getSnapshot().items.find(record => record.id === id), captured = draft;
    if (!record || !captured || !current() || attempt.draft !== captured || attempt.applying) return;
    const read = new AbortController();attempt.applying=read;setApplying(true);setError(false);
    const owns = () => current() && attempt.draft === captured && attempt.applying === read && !read.signal.aborted;
    const result = await presets.apply(record, config, captured.block, read.signal, owns);
    if (!owns()) return;
    attempt.applying=undefined;setApplying(false);
    const copiedRule = result?.rules.find(rule => !config.rules.some(existing => existing.id === rule.id));
    if (!copiedRule) {setError(true);return;}
    const from = captured.from || (record.body.startMinute >= 0 ? minuteText(record.body.startMinute) : '');
    const start = minuteValue(from);
    const to = captured.to || (start !== undefined ? minuteText((start + record.body.durationMinutes) % 1440) : '');
    const days = ['sunday','monday','tuesday','wednesday','thursday','friday','saturday'];
    const weekdays = captured.block.weekdays.length ? captured.block.weekdays : record.body.days.length ? record.body.days.map(day => days.indexOf(day)) : [0,1,2,3,4,5,6];
    const next = {...captured,copiedRule,from,to,block:{...captured.block,ruleId:copiedRule.id,weekdays}};attempt.draft=next;setDraft(next);
  };
  const ruleLabel = (id: string) => id === config.defaultRuleId ? t('builder.block.default') : (draft?.copiedRule?.id === id ? draft.copiedRule.name : config.rules.find(rule => rule.id === id)?.name) || t('error.unavailable');
  return <section className={styles.editorSection}><h3>{t('builder.schedule.hours')}</h3><p className={styles.help}>{t('builder.schedule.help')}</p>
    <div className={styles.choices}><Button size="small" variant={!config.blocks.length ? 'primary' : 'glass'} disabled={locked || !!draft} onClick={() => {if (current()) onChange(value => ({...value,blocks:[]}));}}>{t('builder.schedule.always')}</Button><Button size="small" variant={config.blocks.length ? 'primary' : 'glass'} disabled={locked || !!draft || config.blocks.length >= 64} onClick={() => open()}>{t('builder.schedule.scheduled')}</Button></div>
    <div className={styles.weekStrip}>
      <div className={styles.hourScale} aria-hidden="true">{[0,6,12,18,24].map(hour => <span key={hour} style={{top: `${hour / 24 * 100}%`}}>{String(hour).padStart(2, '0')}</span>)}</div>
      <div className={styles.weekGrid}>{[1,2,3,4,5,6,0].map(day => <div key={day}><span>{dayLabel(day)}</span><div className={styles.dayTrack}>{channelDaySegments(config.blocks, day).map(({block, start, end, continuation}) => <button type="button" key={`${block.id}/${continuation}`} disabled={locked || !!draft} onClick={() => open(block)} style={{top: `${start / 1440 * 100}%`, height: `${(end - start) / 1440 * 100}%`}} title={`${dayLabel(day)}: ${ruleLabel(block.ruleId)}, ${minuteText(block.startMinute)} – ${minuteText(block.endMinute)}`} aria-label={`${dayLabel(day)}: ${ruleLabel(block.ruleId)}, ${minuteText(block.startMinute)} – ${minuteText(block.endMinute)}`}><span>{ruleLabel(block.ruleId)}</span></button>)}</div></div>)}</div>
    </div>
    <div className={styles.blockList}>{config.blocks.map(block => <div key={block.id} className={styles.blockRow}><div><b>{ruleLabel(block.ruleId)}</b><p>{block.weekdays.map(dayLabel).join(', ')}<span>{minuteText(block.startMinute)} – {minuteText(block.endMinute)}</span></p></div><BlockMenu label={ruleLabel(block.ruleId)} disabled={locked || !!draft} edit={() => open(block)} move={() => open(block, true)} remove={() => {if (current() && config.blocks.includes(block)) onChange(value => ({...value, blocks: value.blocks.filter(row => row.id !== block.id)}));}}/></div>)}</div>
    <p className={styles.help}>{t('builder.schedule.otherwise')}</p><Button size="small" variant="glass" disabled={locked || !!draft || config.blocks.length >= 64} onClick={() => open()}>{t('builder.schedule.add')}</Button>
    <Button size="small" variant="text" disabled={locked} onClick={() => {if (current() && !presets.getSnapshot().busy) openPresets();}}>{t('builder.schedule.presets')}</Button>
    <Dialog open={!!draft && isCurrent()} title={draft?.moving ? t('builder.block.moveTitle') : t('builder.block.edit')} onOpenChange={value => {if (!value) close();}} width={540} footer={<><Button size="compact" variant="glass" onClick={close}>{t('builder.cancel')}</Button><Button size="compact" variant="primary" disabled={applying} onClick={() => apply()}>{t('action.done')}</Button></>}>
      {draft && <div className={styles.editorFields}><div className={styles.choices}>{[1,2,3,4,5,6,0].map(day => <label key={day}><input type="checkbox" checked={draft.block.weekdays.includes(day)} onChange={event => update({...draft,block:{...draft.block,weekdays:event.target.checked ? [...draft.block.weekdays,day] : draft.block.weekdays.filter(value => value !== day)}})}/>{dayLabel(day)}</label>)}</div>
        <div className={styles.choices}><TextField aria-label={t('builder.preset.starts')} type="time" value={draft.from} onChange={event => update({...draft,from:event.target.value})}/><span>{t('next.kit.filter.between')}</span><TextField aria-label={t('next.kit.filter.between')} type="time" value={draft.to} onChange={event => update({...draft,to:event.target.value})}/></div>
        {!draft.moving && <><label className={styles.control}><span>{t('builder.block.plays')}</span><RichSelect aria-label={t('builder.block.plays')} value={draft.block.ruleId} disabled={applying} options={[...config.rules.map(rule => ({value:rule.id,label:ruleLabel(rule.id)})),...(draft.copiedRule ? [{value:draft.copiedRule.id,label:draft.copiedRule.name}] : []),...presetState.items.map(record => ({value:'preset:'+record.id,label:record.name}))]} onValueChange={ruleId => {if (ruleId.startsWith('preset:')) {void choosePreset(ruleId.slice(7));return;}if (config.rules.some(rule => rule.id === ruleId) || draft.copiedRule?.id === ruleId) update({...draft,copiedRule:draft.copiedRule?.id === ruleId ? draft.copiedRule : undefined,block:{...draft.block,ruleId}});}}/></label>
        <label className={styles.control}><span>{t('builder.block.overrun')}</span><RichSelect aria-label={t('builder.block.overrun')} value={draft.block.overrun} options={(['finish','cut','fit'] as const).map(value => ({value,label:t(`builder.block.${value}`)}))} onValueChange={value => {if (value === 'finish' || value === 'cut' || value === 'fit') update({...draft,block:{...draft.block,overrun:value}});}}/></label>
        <label className={styles.control}><span>{t('builder.block.anchor')}</span><RichSelect aria-label={t('builder.block.anchor')} value={draft.block.anchor} options={[{value:'channel-cursor',label:t('builder.block.resume')},{value:'block-start',label:t('builder.block.restart')}]} onValueChange={value => {if (value === 'channel-cursor' || value === 'block-start') update({...draft,block:{...draft.block,anchor:value}});}}/></label>
        <label className={styles.control}><span>{t('builder.block.fallback')}</span><RichSelect aria-label={t('builder.block.fallback')} value={draft.block.fallbackRuleId} options={[{value:config.defaultRuleId,label:t('builder.block.default')},{value:'',label:t('builder.whenDone.slate')},...config.rules.filter(rule => rule.id !== config.defaultRuleId).map(rule => ({value:rule.id,label:rule.name}))]} onValueChange={fallbackRuleId => {if (!fallbackRuleId || config.rules.some(rule => rule.id === fallbackRuleId)) update({...draft,block:{...draft.block,fallbackRuleId}});}}/></label>
        </>}
        {draft.original && !draft.moving && <Button size="small" variant="text" className={styles.danger} onClick={() => apply(true)}>{t('builder.block.remove')}</Button>}
        {error && <p role="alert" className={styles.warning}>{t('builder.error.rejected')}</p>}
      </div>}
    </Dialog>
    {error && !draft && <p role="alert" className={styles.warning}>{t('error.unavailable')}</p>}
  </section>;
}

function BlockMenu({label, disabled, edit, move, remove}: {label: string; disabled: boolean; edit: () => void; move: () => void; remove: () => void}) {
  const [open, setOpen] = useState(false);
  return <Menu open={open} onOpenChange={setOpen} title={label} trigger={<Button size="small" variant="icon" icon="i-more" iconOnly disabled={disabled} aria-label={t('server.row.actions', {name: label})}/>}
    items={[{id: 'edit', label: t('web.selection.edit'), onSelect: edit}, {id: 'move', label: t('next.home.layoutMoveAction'), onSelect: move}, {id: 'remove', label: t('builder.block.remove'), destructive: true, onSelect: remove}]}/>;
}
