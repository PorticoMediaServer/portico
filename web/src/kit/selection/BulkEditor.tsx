import {Fragment, useEffect, useId, useRef, useState} from 'react';
import {defaultI18n} from '@i18n';
import type {RepairFieldSpec} from '@core/metadata-repair';
import {Button, Select, Switch, TextField} from '../controls';
import {BottomSheet, Dialog} from '../overlays';
import {usePhone} from '../overlays/shared';
import {Icon} from '../Icon';
import {selectionText as text} from './copy';
import {addListValue, bulkDraftValid, bulkEditorSchema, bulkFieldDisplayValue, bulkFieldHint, bulkFieldStateLabel, bulkSummary, retryLabel, selectionCountText, toggleListRemoval} from './presentation';
import type {BulkEditorDraft, BulkEditorProps, BulkFieldValues} from './types';
import styles from './selection.module.css';

type FieldProps = Readonly<{spec: RepairFieldSpec; aggregate: BulkFieldValues; count: number; draft: BulkEditorDraft; onChange: (draft: BulkEditorDraft) => void; busy: boolean; phone: boolean; showListHelp: boolean; initial?: boolean}>;
function BulkField({spec, aggregate, count, draft, onChange, busy, phone, showListHelp, initial}: FieldProps) {
  const id = useId(), [adding, setAdding] = useState(''), [editing, setEditing] = useState(false);
  const edited = Object.hasOwn(draft.fields, spec.field), list = draft.lists[spec.field];
  const mixed = aggregate.state !== 'shared', known = aggregate.state === 'shared' && aggregate.value !== undefined;
  const value = edited ? draft.fields[spec.field]! : known ? aggregate.value! : '';
  const changed = edited || !!(list?.add.length || list?.remove.length);
  const hint = <span className={styles.valueHint}>{bulkFieldHint(aggregate, count, edited, phone)}</span>;
  const addLabel = text(spec.field === 'genres' ? 'next.selection.addGenre' : spec.field === 'tags' ? 'next.selection.addTag' : spec.field === 'labels' ? 'next.selection.addLabel' : 'next.selection.addValue', {field: spec.label});
  const emptyList = spec.type === 'list' && aggregate.state === 'shared' && aggregate.value !== undefined && !aggregate.truncated && aggregate.entries.length === 0 && !list?.add.length;
  const scalarChange = (next: string) => {
    const fields = {...draft.fields};
    if (known && aggregate.value === next) delete fields[spec.field]; else fields[spec.field] = next;
    onChange({...draft, fields});
  };
  const append = () => {
    const next = addListValue(list, adding);
    if (next) { onChange({...draft, lists: {...draft.lists, [spec.field]: next}}); setAdding(''); }
  };
  return <div className={[styles.bulkField, spec.type === 'list' && styles.fullField].filter(Boolean).join(' ')} data-changed={changed}>
    <div className={styles.fieldLabel}><label htmlFor={id}>{phone && spec.field === 'metadataLanguage' ? text('next.selection.language') : spec.label}</label>{edited && <span className={styles.changed}>{text('next.selection.changed')}</span>}</div>
    {spec.type === 'list' ? <>
      <div className={styles.listValues}>
        {aggregate.entries.map(entry => <Button key={entry.value} variant="text" size="xs" disabled={busy} className={styles.listChip} data-partial={entry.count !== count} data-removed={list?.remove.includes(entry.value)} aria-label={text(list?.remove.includes(entry.value) ? 'next.selection.undoRemoveValue' : 'next.selection.removeValue', {value: entry.value})}
          onClick={() => onChange({...draft, lists: {...draft.lists, [spec.field]: toggleListRemoval(list, entry.value)}})}>{entry.value}<i><span className={styles.desktopChipCount}>{text('next.selection.countOf', {count: entry.count, total: count})}</span><span className={styles.phoneChipCount}>{defaultI18n.number(entry.count)}</span></i><Icon name={list?.remove.includes(entry.value) ? 'i-restart' : 'i-x'} size={12} /></Button>)}
        {list?.add.filter(entry => !aggregate.entries.some(original => original.value === entry)).map(entry => <Button key={entry} variant="text" size="xs" disabled={busy} className={styles.listChip} data-added aria-label={text('next.selection.removeValue', {value: entry})}
          onClick={() => onChange({...draft, lists: {...draft.lists, [spec.field]: toggleListRemoval(list, entry)}})}>{entry}<i>{text('next.selection.newValue')}</i><Icon name="i-x" size={12} /></Button>)}
        <TextField id={id} variant="compact" value={adding} disabled={busy} aria-label={addLabel} placeholder={emptyList && (spec.field === 'tags' || spec.field === 'labels') ? text(spec.field === 'tags' ? 'next.selection.emptyTag' : 'next.selection.emptyLabel') : addLabel} containerClassName={styles.addListValue} onChange={event => setAdding(event.currentTarget.value)} onKeyDown={event => { if (event.key === 'Enter') { event.preventDefault(); append(); } }} onBlur={append} />
      </div>
      {aggregate.state === 'many' && <p className={styles.fieldHelp}>{bulkFieldStateLabel(aggregate)}</p>}
      {spec.field === 'genres' && showListHelp && <p className={styles.fieldHelp}>{text('next.selection.listHelp', {count})}</p>}
    </> : <div className={styles.scalarShell} data-changed={edited} data-mixed={mixed && !edited}>
      {spec.type === 'enum' && spec.allowed?.length ? <Select id={id} options={spec.allowed.filter(Boolean).map(option => ({value: option, label: bulkFieldDisplayValue(spec.field, option)}))} value={value || undefined} onValueChange={scalarChange} disabled={busy} aria-label={spec.label} placeholder={mixed && !edited ? bulkFieldStateLabel(aggregate) : known || edited ? text('next.selection.notSet') : undefined} containerClassName={styles.scalarControl} /> : <TextField id={id} value={editing ? value : bulkFieldDisplayValue(spec.field, value)} autoFocus={initial} onFocus={() => setEditing(true)} onBlur={() => setEditing(false)} onChange={event => scalarChange(event.currentTarget.value)} disabled={busy} aria-label={spec.label} trailing={hint}
        type={spec.type === 'integer' || spec.type === 'number' ? 'number' : spec.type === 'date' ? 'date' : 'text'} min={spec.min} max={spec.max} maxLength={spec.maxLength} step={spec.type === 'integer' ? 1 : spec.type === 'number' ? 'any' : undefined} containerClassName={styles.scalarControl} placeholder={known || edited ? text('next.selection.notSet') : undefined} />}
      {mixed && !edited && spec.type !== 'enum' && <span className={styles.mixedValue}>{bulkFieldStateLabel(aggregate)}</span>}
      {spec.type === 'enum' && hint}
    </div>}
  </div>;
}

function BulkResults({result, targets, resultDescriptions, onRetry, draft, busy}: Pick<BulkEditorProps, 'result' | 'targets' | 'resultDescriptions' | 'onRetry' | 'draft' | 'busy'>) {
  const phone = usePhone();
  if (!result) return null;
  const byId = new Map(targets.map(target => [target.id, target]));
  return <div className={styles.bulkResults}>
    <div className={styles.resultBanner} role="status"><Icon name={result.failed ? 'i-warn' : 'i-check'} size={22} /><div><b>{text('next.selection.bulkResult', {updated: result.updated, failed: result.failed})}</b>{result.failed > 0 && <p>{text(phone ? 'next.selection.bulkResultHelpPhone' : 'next.selection.bulkResultHelp', {count: result.updated, titles: defaultI18n.list(result.results.filter(row => !row.ok).map(row => byId.get(row.id)?.title ?? text('next.selection.title')))})}</p>}</div></div>
    <div className={styles.resultRows}>{result.results.map(row => {
      const entry = byId.get(row.id), reason = row.code === 'metadata_conflict' ? 'next.selection.metadataConflict' : row.code === 'invalid_metadata_repair' ? 'next.selection.invalidMetadata' : row.code === 'unsupported_item_kind' ? 'next.selection.unsupportedMetadata' : row.code === 'not_found' ? 'next.selection.notFound' : 'next.selection.metadataFailed';
      return <div key={`${row.kind}-${row.id}`} className={styles.resultRow}><span className={styles.resultArtwork}>{entry?.artwork}</span><div><b>{entry?.title ?? text('next.selection.title')}</b><small>{row.ok && phone ? text('next.selection.saved') : !row.ok && phone && row.code === 'metadata_conflict' ? text('next.selection.metadataConflictPhone') : resultDescriptions?.[row.id] ?? text(row.ok ? 'next.selection.saved' : reason)}</small></div><span className={styles.resultStatus} data-ok={row.ok}><Icon name={row.ok ? 'i-check' : 'i-x'} size={16} /></span>{!row.ok && <Button size="xs" disabled={busy} onClick={() => onRetry([row.id], draft)}>{text('next.selection.retry')}</Button>}</div>;
    })}</div>
    {!phone && result.failed > 0 && <div className={styles.singleItemNote}><Icon name="i-info" size={18} /><p>{text('next.selection.bulkRetryHelp')}</p></div>}
  </div>;
}

/** Controlled metadata fields and receipts; the caller supplies the real schema and aggregate read. */
export function BulkEditor(props: BulkEditorProps) {
  const {selection, schema, singleItemFields, values, draft, onDraftChange, fieldAdditions, onSave, onRetry, targets, resultDescriptions, result, busy = false, error, ownerTag, description, ...surface} = props;
  const phone = usePhone(), [viewport, setViewport] = useState(() => typeof window === 'undefined' ? 844 : window.visualViewport?.height ?? window.innerHeight);
  const focus = useRef<HTMLElement | null>(null);
  useEffect(() => { const update = () => setViewport(window.visualViewport?.height ?? window.innerHeight); window.visualViewport?.addEventListener('resize', update); window.addEventListener('resize', update); return () => { window.visualViewport?.removeEventListener('resize', update); window.removeEventListener('resize', update); }; }, []);
  const allowedSchema = bulkEditorSchema(schema, selection.kind);
  const fields = allowedSchema.filter(spec => values[spec.field]);
  const summary = bulkSummary(fields, values, draft, selection.count);
  const untouched = fields.filter(spec => !Object.hasOwn(draft.fields, spec.field) && !draft.lists[spec.field]?.add.length && !draft.lists[spec.field]?.remove.length).map(spec => spec.label);
  const failed = result?.results.filter(row => !row.ok) ?? [];
  const names = new Map(targets.map(target => [target.id, target.title]));
  const items = selection.kind === 'song' ? text('next.selection.track', {count: selection.count}) : selectionCountText(selection);
  const title = text('next.selection.editTitle', {items});
  const heading = <span className={styles.editorHeading}><span className={styles.editorOwner}>{ownerTag}{phone && !busy && <Button variant="icon" size="small" iconOnly icon="i-x" iconSize={18} aria-label={text('next.selection.close')} className={styles.editorClose} onClick={() => surface.onOpenChange(false, 'close-button')} />}</span><span className={styles.editorTitle}>{title}</span>
    {!result && <span className={styles.editorDescription}>{description ?? text(phone ? 'next.selection.bulkPhoneDescription' : 'next.selection.bulkDescription', {count: selection.count})}</span>}
    {targets.some(target => target.artwork) && <span className={styles.editorArtwork} data-kind={selection.kind}>{targets.slice(0, 5).map(target => <span key={target.id}>{target.artwork}</span>)}{!result && <small>{selection.count > 5 ? text('next.selection.andMore', {count: selection.count - 5}) : defaultI18n.list(targets.map(target => target.title))}</small>}</span>}
  </span>;
  const body = <>
    {error && <div className={styles.resultBanner} role="alert"><Icon name="i-warn" size={20} /><p>{error}</p></div>}
    {result ? <BulkResults result={result} targets={targets} resultDescriptions={resultDescriptions} onRetry={onRetry} draft={draft} busy={busy} /> : <>
      <div className={styles.fieldGrid} ref={element => { focus.current = element?.querySelector<HTMLElement>('input,button') ?? null; }}>{fields.map((spec, index) => <Fragment key={spec.field}><BulkField spec={spec} aggregate={values[spec.field]!} count={selection.count} draft={draft} onChange={onDraftChange} busy={busy} phone={phone} showListHelp={!phone && selection.kind === 'movie'} initial={index === 0} />{fieldAdditions?.[spec.field]}</Fragment>)}</div>
      <div className={styles.singleItemNote}><Icon name="i-info" size={18} /><div><b>{text(phone ? 'next.selection.oneAtATime' : selection.kind === 'episode' ? 'next.selection.episodeOneAtATime' : selection.kind === 'song' ? 'next.selection.trackOneAtATime' : 'next.selection.editedOneAtATime')}</b> {defaultI18n.list(singleItemFields.filter(field => !field.bulk).map(field => field.label.toLocaleLowerCase(defaultI18n.region.uiLocale)))}. {!phone && text(selection.kind === 'episode' ? 'next.selection.episodeOneHelp' : selection.kind === 'song' ? 'next.selection.trackOneHelp' : 'next.selection.titleOneHelp')}</div></div>
      {fields.length === 0 && <p className={styles.fieldHelp}>{text('next.selection.noSeasonFields')}</p>}
      <label className={styles.lockRow}><span>{text('next.selection.lock')}<small>{text('next.selection.lockHelp')}</small></span><Switch checked={draft.lockEdited} disabled={busy} onCheckedChange={value => onDraftChange({...draft, lockEdited: value})} aria-label={text('next.selection.lock')} /></label>
    </>}
  </>;
  const footer = <>
    {!result && summary.length > 0 && <div className={styles.summary}><h5>{!phone && <Icon name="i-check" size={16} />}{text('next.selection.summary')}<small>{text('next.selection.changes', {count: summary.length})}</small></h5><ul>{summary.map(row => <li key={row.id}>{row.fieldLabel && row.text.startsWith(row.fieldLabel) ? <><b>{row.fieldLabel}</b>{row.text.slice(row.fieldLabel.length)}</> : row.text}{!phone && row.changedCount !== undefined && row.changedCount !== selection.count && <span> {text(selection.kind === 'episode' ? row.changedTitles?.length ? 'next.selection.episodeChangesNamed' : 'next.selection.episodeChangeCount' : selection.kind === 'song' ? row.changedTitles?.length ? 'next.selection.trackChangesNamed' : 'next.selection.trackChangeCount' : 'next.selection.changeCount', {count: row.changedCount, titles: defaultI18n.list(row.changedTitles ?? [])})}</span>}</li>)}</ul>{!phone && untouched.length > 0 && <p>{text('next.selection.leftAlone', {fields: defaultI18n.list(untouched)})}{selection.kind === 'movie' && <> {text('next.selection.onlyChangedSaved')}</>}</p>}</div>}
    <div className={styles.editorButtons}><Button size="compact" disabled={busy} onClick={() => surface.onOpenChange(false, 'action')}>{text(result ? 'next.selection.close' : 'next.selection.cancel')}</Button>
      {result ? failed.length > 0 && <Button variant="primary" size="compact" loading={busy} onClick={() => onRetry(failed.map(row => row.id), draft)}>{retryLabel(failed.map(row => ({title: names.get(row.id) ?? text('next.selection.title')})))}</Button> : <Button variant="primary" size="compact" loading={busy} disabled={!bulkDraftValid(allowedSchema, values, draft)} onClick={() => { if (bulkDraftValid(allowedSchema, values, draft)) onSave(draft); }}>{text('next.selection.save', {items})}</Button>}
    </div>
  </>;
  const common = {...surface, title: heading, accessibilityLabel: title, children: body, footer, initialFocusRef: focus, className: styles.editor, headerClassName: styles.editorHeader, bodyClassName: styles.editorBody, footerClassName: styles.editorFooter, dismissOnOutsidePress: !busy, dismissOnBack: !busy, showCloseButton: !phone && !busy};
  return phone ? <BottomSheet {...common} detents={[viewport - 44]} detent={0} /> : <Dialog {...common} responsive={false} placement="right" width={600} />;
}
