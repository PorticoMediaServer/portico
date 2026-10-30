import {useRef, useState, type ReactNode} from 'react';
import {defaultI18n} from '@i18n';
import {Icon} from '../Icon';
import {Button} from '../controls';
import styles from './settings.module.css';

const t = defaultI18n.t;
export type OrderedSettingsItem = Readonly<{id: string; label: string; description?: string}>;
export function reorderedIds(items: readonly {id: string}[], id: string, to: number): readonly string[] {
  const ids = items.map(item => item.id), from = ids.indexOf(id);
  if (from < 0 || to < 0 || to >= ids.length || from === to) return ids;
  ids.splice(to, 0, ...ids.splice(from, 1)); return ids;
}
/** Pointer and keyboard paths submit the same complete, stable-ID order once. */
export function SettingsOrderedList({items, label, numbered = false, onReorder, onError, renderActions, onRemove, add, empty}: {
  items: readonly OrderedSettingsItem[]; label: string; numbered?: boolean; onReorder: (ids: readonly string[]) => void | Promise<void>;
  onError: (error: unknown) => void; renderActions?: (item: OrderedSettingsItem) => ReactNode; onRemove?: (id: string) => void | Promise<void>; add?: ReactNode; empty?: string;
}) {
  const root = useRef<HTMLOListElement>(null), drag = useRef<{id: string; pointer: number; to: number; moved: boolean} | undefined>(undefined);
  const [preview, setPreview] = useState<readonly string[]>(), [busy, setBusy] = useState(false), [announcement, setAnnouncement] = useState('');
  const visible = preview ? preview.map(id => items.find(item => item.id === id)!).filter(Boolean) : items;
  const move = async (id: string, to: number) => {
    const next = reorderedIds(items, id, to); if (next.every((id, i) => id === items[i].id)) return;
    setBusy(true);
    try { await onReorder(next); setAnnouncement(t('next.settings.moved', {name: items.find(item => item.id === id)!.label, position: to + 1, count: items.length})); }
    catch (error) {onError(error);}
    finally {setBusy(false); setPreview(undefined);}
  };
  const remove = async (id: string, index: number) => {
    setBusy(true);
    try {
      await onRemove?.(id); setAnnouncement(t('next.settings.removed', {name: items[index].label}));
      const next = items[index + 1]?.id ?? items[index - 1]?.id;
      requestAnimationFrame(() => {
        const row = [...(root.current?.querySelectorAll<HTMLElement>('[data-ordered-id]') ?? [])].find(element => element.dataset.orderedId === next);
        (row?.querySelector<HTMLElement>('[data-remove]') ?? root.current?.parentElement?.querySelector<HTMLElement>('[data-list-add] button'))?.focus();
      });
    } catch (error) {onError(error);} finally {setBusy(false);}
  };
  return <div className={styles.ordered} aria-busy={busy}>
    <ol ref={root} aria-label={label} className={styles.orderedRows}>{visible.map((item, index) => <li key={item.id} data-ordered-id={item.id} data-dragging={drag.current?.id === item.id || undefined}>
      <button type="button" className={styles.dragHandle} disabled={busy} aria-label={t('next.settings.reorder', {name: item.label})}
        onKeyDown={event => {if (!['ArrowUp', 'ArrowDown'].includes(event.key) || busy) return; event.preventDefault(); void move(item.id, index + (event.key === 'ArrowUp' ? -1 : 1));}}
        onPointerDown={event => {if (busy || event.button !== 0) return; event.preventDefault(); event.currentTarget.focus(); event.currentTarget.setPointerCapture(event.pointerId); drag.current = {id: item.id, pointer: event.pointerId, to: index, moved: false};}}
        onPointerMove={event => {
          const current = drag.current; if (!current || current.pointer !== event.pointerId) return;
          const rows = [...(root.current?.querySelectorAll<HTMLElement>('[data-ordered-id]') ?? [])];
          const to = rows.findIndex(row => {const box = row.getBoundingClientRect(); return event.clientY >= box.top && event.clientY < box.bottom;});
          if (to < 0 || to === current.to) return; current.to = to; current.moved = true; setPreview(reorderedIds(items, current.id, to));
        }}
        onPointerUp={event => {const current = drag.current; if (!current || current.pointer !== event.pointerId) return; drag.current = undefined; if (current.moved) void move(current.id, current.to); else setPreview(undefined);}}
        onPointerCancel={() => {drag.current = undefined; setPreview(undefined);}}><span aria-hidden="true">⠿</span></button>
      {numbered && <span className={styles.ordinal} aria-hidden="true">{index + 1}</span>}
      <span className={styles.orderedLabel}>{item.label}{item.description && <small>{item.description}</small>}</span>
      {renderActions?.(item)}
      {numbered && <><Button size="xs" variant="icon" disabled={busy || index === 0} aria-label={t('next.settings.moveUp', {name: item.label})} onClick={() => {void move(item.id, index - 1);}}><Icon name="i-arrow-up" size={16} /></Button><Button size="xs" variant="icon" disabled={busy || index === items.length - 1} aria-label={t('next.settings.moveDown', {name: item.label})} onClick={() => {void move(item.id, index + 1);}}><Icon name="i-arrow-down" size={16} /></Button></>}
      {onRemove && <Button size="xs" variant="icon" data-remove disabled={busy} aria-label={t('next.settings.remove', {name: item.label})} onClick={() => {void remove(item.id, index);}}><Icon name="i-x" size={16} /></Button>}
    </li>)}</ol>
    {!items.length && empty && <p className={styles.orderedEmpty}>{empty}</p>}
    {add && <div className={styles.listAdd} data-list-add>{add}</div>}
    <span className={styles.srOnly} role="status">{announcement}</span>
  </div>;
}
