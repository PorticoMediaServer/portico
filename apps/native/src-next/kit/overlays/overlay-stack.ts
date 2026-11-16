import type {OverlayReason} from './types';

type Entry = {id: object; dismiss: (reason: OverlayReason) => void};
/** Native Back and hardware Escape go to one topmost surface; closing a lower surface cannot restore focus over it. */
export function createOverlayStack() {
  const entries: Entry[] = [], listeners = new Set<() => void>();
  const changed = () => {for (const listener of [...listeners]) listener();};
  return {
    register(id: object, dismiss: Entry['dismiss']) {
      const entry = {id, dismiss};
      entries.push(entry);
      if (entries.length === 1) changed();
      return () => {
        const wasTop = entries[entries.length - 1] === entry;
        const index = entries.indexOf(entry);
        if (index >= 0) entries.splice(index, 1);
        if (index >= 0 && !entries.length) changed();
        return index >= 0 && wasTop;
      };
    },
    /** Whether any surface is open: what sits under one (a rotating hero) holds still while it is. */
    isOpen: () => entries.length > 0,
    subscribe(listener: () => void) {listeners.add(listener); return () => {listeners.delete(listener);};},
    isTop(id: object) {return entries[entries.length - 1]?.id === id;},
    dismiss(reason: OverlayReason) {
      const top = entries[entries.length - 1];
      if (!top) return false;
      top.dismiss(reason);
      return true;
    },
  };
}
