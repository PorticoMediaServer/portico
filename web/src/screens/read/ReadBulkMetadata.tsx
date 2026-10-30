import {useEffect, useRef, useState, type RefObject} from 'react';
import {defaultI18n} from '@i18n';
import {fetchBulkMetadataPreview, type BulkMetadataPreview} from '@core/home-selection/metadata-preview.ts';
import {bulkTargetLimit, validBulkEdit, validateBulkReceipt, type BulkEdit, type BulkReceipt} from '@core/metadata-bulk.ts';
import {routePath} from '../../../../packages/contracts/src/routes.ts';
import {BulkEditor, type BulkEditorDraft} from '../../kit/selection';
import {Button} from '../../kit/controls';
import {Dialog} from '../../kit/overlays';
import {OwnerTag} from '../../kit/shell';
import {editorCoreDraft, editorResultView, editorValues} from '../../shell/selection-presentation';
import {screenLifetime, type ScreenContext} from '../detail/context';
import {ReadSeriesOrder} from './ReadSeriesOrder';
import type {ReadingLibraryItem} from './library-collection';

const {t} = defaultI18n;
const previewTargets = (selected: readonly ReadingLibraryItem[], kind: 'ebook' | 'comic') => selected.map(item => ({kind: 'item' as const, contentKind: kind, id: item.id, libraryId: item.libraryId}));
const emptyDraft: BulkEditorDraft = {fields: {}, lists: {}, lockEdited: true};
type Operation = Readonly<{bodies: readonly BulkEdit[]; draft: BulkEditorDraft; preview: BulkMetadataPreview}>;
type Open = Readonly<{life: ReturnType<typeof screenLifetime>; preview: BulkMetadataPreview}>;

/** Explicit Reading selections only. The server previews every target and owns each revision/CAS. */
export function ReadBulkMetadata({context, items, kind, anchor, onClose}: {context: ScreenContext; items: readonly ReadingLibraryItem[]; kind: 'ebook' | 'comic'; anchor: RefObject<HTMLElement | null>; onClose: () => void}) {
  const [order, setOrder] = useState<readonly string[]>(() => Object.freeze(items.map(item => item.id)));
  const [open, setOpen] = useState<Open>(), [draft, setDraft] = useState<BulkEditorDraft>(emptyDraft), [busy, setBusy] = useState(false);
  const [failed, setFailed] = useState(false), [retry, setRetry] = useState(0), [uncertain, setUncertain] = useState(false), [receipts, setReceipts] = useState<readonly BulkReceipt[]>([]);
  const operation = useRef<{capture: Operation; next: number} | undefined>(undefined), pending = useRef<ReturnType<typeof screenLifetime> | undefined>(undefined);
  useEffect(() => {
    const life = screenLifetime(context); let active = true;
    setOrder(Object.freeze(items.map(item => item.id))); setOpen(undefined); setBusy(false); setFailed(false); setUncertain(false); setReceipts([]); setDraft(emptyDraft); operation.current = undefined; pending.current = undefined;
    const owner = context.identity?.viewer;
    if (owner?.authority !== 'local' || owner.role !== 'owner' || !items.length || items.length > bulkTargetLimit || new Set(items.map(item => item.id)).size !== items.length) {life.dispose(); setFailed(true); return;}
    void fetchBulkMetadataPreview(life.api, {targets: previewTargets(items, kind)}, context.scope.serverId, life.signal(), {isCurrent: life.current}).then(preview => {
      if (active && life.current()) setOpen({life, preview});
    }).catch(error => {if (active && life.current()) {setFailed(true); context.effects.reportError(error);}});
    return () => {active = false; life.dispose();};
  }, [context, items, kind, retry]);
  const send = async (held: Open, pendingOperation: {capture: Operation; next: number}) => {
    try {
      while (pendingOperation.next < pendingOperation.capture.bodies.length) {
        if (!held.life.current() || operation.current !== pendingOperation) return;
        const body = pendingOperation.capture.bodies[pendingOperation.next];
        const raw = await held.life.api.request(routePath('post_metadata_bulk'), 'POST', body, held.life.signal());
        if (!held.life.current() || operation.current !== pendingOperation) return;
        const receipt = validateBulkReceipt(raw, body);
        pendingOperation.next++; setReceipts(previous => [...previous, receipt]);
      }
      operation.current = undefined; setUncertain(false);
    } catch (error) {
      if (held.life.current()) {setUncertain(true); context.effects.reportError(error);}
    }
  };
  const save = async (selected: readonly ReadingLibraryItem[], applied: BulkEditorDraft) => {
    const held = open;
    if (!held || pending.current || !held.life.current() || operation.current) return;
    pending.current = held.life; setBusy(true); setFailed(false);
    try {
      const preview = await fetchBulkMetadataPreview(held.life.api, {targets: previewTargets(selected, kind)}, context.scope.serverId, held.life.signal(), {isCurrent: held.life.current});
      if (!held.life.current()) return;
      const core = editorCoreDraft(applied);
      const numbered = items.length > 1 && !!applied.fields.series?.trim() && preview.schema.some(field => field.field === 'seriesNumber' && field.bulk);
      const numbers = new Map(order.map((id, index) => [id, index + 1]));
      if (numbered && (order.length !== items.length || numbers.size !== items.length || items.some(item => !numbers.has(item.id)))) throw new Error('invalid_metadata_edit');
      const targets = preview.targets.map(target => Object.freeze({kind: target.kind, id: target.id, expectedRevision: target.expectedRevision}));
      if (numbered && targets.some(target => !numbers.has(target.id))) throw new Error('invalid_metadata_edit');
      const bodies: readonly BulkEdit[] = Object.freeze(numbered ? targets.map(target => Object.freeze({operationId: crypto.randomUUID(), targets: Object.freeze([target]), ...core,
        fields: Object.freeze({...core.fields, seriesNumber: Object.freeze({value: String(numbers.get(target.id))})})}))
        : [Object.freeze({operationId: crypto.randomUUID(), targets: Object.freeze(targets), ...core})]);
      if (bodies.some(body => !validBulkEdit(body, preview.schema))) throw new Error('invalid_metadata_edit');
      const captured: Operation = Object.freeze({bodies, draft: applied, preview});
      const pendingOperation = {capture: captured, next: 0}; operation.current = pendingOperation;
      await send(held, pendingOperation);
    } catch (error) {if (held.life.current()) {setFailed(true); context.effects.reportError(error);}}
    finally {if (pending.current === held.life) pending.current = undefined; if (held.life.current()) setBusy(false);}
  };
  const retryUncertain = async () => {
    const held = open, captured = operation.current;
    if (!held || !captured || pending.current || !held.life.current()) return;
    pending.current = held.life; setBusy(true);
    try {await send(held, captured);} finally {if (pending.current === held.life) pending.current = undefined; if (held.life.current()) setBusy(false);}
  };
  const close = (value: boolean) => {if (!value && !busy) onClose();};
  if (!open || uncertain) return <Dialog open title={t('next.home.editSelection')} onOpenChange={close} returnFocusRef={anchor}
    footer={failed || uncertain ? <Button disabled={busy} onClick={() => {if (uncertain) void retryUncertain(); else setRetry(value => value + 1);}}>{t('next.states.tryAgain')}</Button> : undefined}>
    <p role={failed || uncertain ? 'alert' : 'status'}>{t(failed || uncertain ? 'next.states.pageFailedBody' : 'next.selection.checking')}</p>
  </Dialog>;
  const numbered = items.length > 1 && !!draft.fields.series?.trim() && open.preview.schema.some(field => field.field === 'seriesNumber' && field.bulk);
  const changeDraft = (next: BulkEditorDraft) => {
    if (items.length > 1 && next.fields.series?.trim()) {const {seriesNumber: _number, ...fields} = next.fields; setDraft({...next, fields});}
    else setDraft(next);
  };
  return <BulkEditor open onOpenChange={close} returnFocusRef={anchor} selection={{count: items.length, kind}}
    schema={numbered ? open.preview.schema.filter(field => field.field !== 'seriesNumber') : open.preview.schema} singleItemFields={open.preview.singleItemFields} values={editorValues(open.preview)} draft={draft} onDraftChange={changeDraft}
    fieldAdditions={numbered ? {series: <ReadSeriesOrder items={items} order={order} comic={kind === 'comic'} disabled={busy} artwork={item => context.artwork.foreground(item.coverUrl, 96, 2 / 3)} onChange={next => {if (open.life.current() && !busy && next.length === items.length && new Set(next).size === items.length && next.every(id => items.some(item => item.id === id))) setOrder(Object.freeze([...next]));}}/>} : undefined}
    targets={items.map(item => ({id: item.id, title: item.title}))} result={editorResultView(receipts)} busy={busy} error={failed ? t('next.states.pageFailedBody') : undefined}
    onSave={value => {void save(items, value);}} onRetry={(ids, value) => {
      const result = editorResultView(receipts), failures = result?.results.filter(row => !row.ok).map(row => row.id) ?? [];
      if (!ids.length || ids.some(id => !failures.includes(id))) return;
      const selected = items.filter(item => ids.includes(item.id)); if (selected.length === ids.length) void save(selected, value);
    }} ownerTag={<OwnerTag/>}/>;
}
