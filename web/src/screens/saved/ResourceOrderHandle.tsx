import {useEffect, useRef, useState} from 'react';
import {defaultI18n} from '@i18n';
import {Icon} from '../../kit/Icon';
import {createOrderAutoScroll, orderScrollContainer} from './resource-order-scroll';
import styles from './saved.module.css';

type Move = (entryId: string, targetId: string) => void;
type Lift = {entryId: string; targetId: string; ids: readonly string[]; move: Move; root: HTMLElement; target?: HTMLElement; pointer?: number; scroll?: ReturnType<typeof createOrderAutoScroll>};

/** A handle moves only within the server-owned window currently on screen. The
 * captured command is submitted on drop; cancel never changes the resource. */
export function ResourceOrderHandle({entryId, entryIds, title, disabled, onMove, onLiftChange}: {
  entryId: string; entryIds: readonly string[]; title: string; disabled: boolean; onMove: Move; onLiftChange(entryId?: string): void;
}) {
  const lift = useRef<Lift | undefined>(undefined), [picked, setPicked] = useState(false);
  const clear = (commit = false) => {
    const held = lift.current; lift.current = undefined;
    held?.scroll?.stop();
    onLiftChange();
    held?.target?.removeAttribute('data-order-drop'); setPicked(false);
    if (commit && held && held.entryId !== held.targetId) held.move(held.entryId, held.targetId);
  };
  useEffect(() => () => {if (lift.current) onLiftChange();lift.current?.scroll?.stop();lift.current?.target?.removeAttribute('data-order-drop');lift.current = undefined;}, [onLiftChange]);
  useEffect(() => {if (disabled) {onLiftChange();lift.current?.scroll?.stop();lift.current?.target?.removeAttribute('data-order-drop');lift.current = undefined;setPicked(false);}}, [disabled, onLiftChange]);
  const begin = (button: HTMLButtonElement, pointer?: number) => {
    const root = button.closest<HTMLElement>('[data-resource-order]');
    if (disabled || !root || !entryIds.includes(entryId)) return;
    lift.current?.scroll?.stop();lift.current?.target?.removeAttribute('data-order-drop');
    const held: Lift = {entryId, targetId: entryId, ids: [...entryIds], move: onMove, root, pointer};
    lift.current = held; setPicked(true);onLiftChange(entryId);
    if (pointer !== undefined) {
      const container = orderScrollContainer(button), page = container === document.scrollingElement;
      held.scroll = createOrderAutoScroll({request: callback => requestAnimationFrame(callback), cancel: id => cancelAnimationFrame(id),
        bounds: () => {const rect = container.getBoundingClientRect();return page ? {top: 0, bottom: window.innerHeight} : {top: Math.max(0, rect.top), bottom: Math.min(window.innerHeight, rect.bottom)};},
        scroll: delta => {container.scrollTop += delta;}, retarget: (x, y) => pointerTarget(x, y)});
    }
  };
  const target = (id: string) => {
    const held = lift.current;
    if (!held || !held.ids.includes(id)) return;
    const row = Array.from(held.root.querySelectorAll<HTMLElement>('[data-resource-entry]')).find(row => row.dataset.resourceEntry === id);
    if (!row) return;
    held.target?.removeAttribute('data-order-drop'); held.targetId = id;
    held.target = row;
    if (id !== held.entryId) held.target?.setAttribute('data-order-drop', held.ids.indexOf(id) < held.ids.indexOf(held.entryId) ? 'before' : 'after');
    if (held.pointer === undefined) row.scrollIntoView({block: 'nearest'});
  };
  const pointerTarget = (x: number, y: number) => {
    const held = lift.current;if (!held) return;
    const row = document.elementFromPoint(x, y)?.closest<HTMLElement>('[data-resource-entry]');
    if (row && row.closest('[data-resource-order]') === held.root && row.dataset.resourceEntry) target(row.dataset.resourceEntry);
  };
  return <button type="button" className={styles.orderHandle} disabled={disabled} data-picked={picked || undefined}
    aria-label={`${defaultI18n.t('order.reorder')}: ${title}`} aria-pressed={picked}
    aria-description={defaultI18n.t('next.home.layoutMoveHint')}
    onKeyDown={event => {
      if (event.key === 'Escape') {if (lift.current) {event.preventDefault();event.stopPropagation();clear();} return;}
      if (event.key === ' ' || event.key === 'Enter') {event.preventDefault();event.stopPropagation();if (lift.current) clear(true);else begin(event.currentTarget);return;}
      const held = lift.current;
      if (!held || held.pointer !== undefined) return;
      const step = event.key === 'ArrowUp' ? -1 : event.key === 'ArrowDown' ? 1 : undefined;
      if (step !== undefined) {
        event.preventDefault();event.stopPropagation();
        const drawn = new Set(Array.from(held.root.querySelectorAll<HTMLElement>('[data-resource-entry]')).map(row => row.dataset.resourceEntry));
        for (let index = held.ids.indexOf(held.targetId) + step; index >= 0 && index < held.ids.length; index += step) {
          if (drawn.has(held.ids[index])) {target(held.ids[index]);break;}
        }
      }
    }}
    onPointerDown={event => {if (event.button !== 0) return;event.preventDefault();event.currentTarget.focus();begin(event.currentTarget, event.pointerId);if (lift.current) {event.currentTarget.setPointerCapture(event.pointerId);lift.current.scroll?.update(event.clientX, event.clientY);}}}
    onPointerMove={event => {
      const held = lift.current;if (!held || held.pointer !== event.pointerId) return;
      held.scroll?.update(event.clientX, event.clientY);pointerTarget(event.clientX, event.clientY);
    }}
    onPointerUp={event => {if (lift.current?.pointer !== event.pointerId) return;clear(true);if (event.currentTarget.hasPointerCapture(event.pointerId)) event.currentTarget.releasePointerCapture(event.pointerId);}}
    onPointerCancel={() => clear()} onLostPointerCapture={() => {if (lift.current?.pointer !== undefined) clear();}}
    onBlur={() => {if (lift.current?.pointer === undefined) clear();}}>
    <Icon name="i-grip" size={22}/>
  </button>;
}
