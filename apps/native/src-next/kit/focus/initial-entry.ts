import type {FocusRegistry} from './registry';

type EntryRegistry = Pick<FocusRegistry, 'attachedFocus' | 'hasRetainedFocus' | 'entryAddress' | 'focus'>;
/** One explicit page-entry lifetime. Historical/detached snapshots are not current ownership.
 * Only real registered native leaves qualify; direction keys remain native-engine decisions. */
export function createInitialRegionEntry(registry: EntryRegistry, scope: string) {
  let finished = false;
  return {
    scope,
    finish() {finished = true;},
    request({active, primary, fallback = [], allowFallback = false}: Readonly<{active: boolean; primary: string; fallback?: readonly string[]; allowFallback?: boolean}>): 'waiting' | 'owned' | 'requested' | 'finished' {
      if (finished) return 'finished';
      if (!active) return 'waiting';
      if (registry.attachedFocus()) {finished = true; return 'owned';}
      // A retiring overlay still owns its original-return lease through actual exit.
      if (registry.hasRetainedFocus()) return 'waiting';
      const candidates = allowFallback ? [primary, ...fallback] : [primary];
      for (const id of candidates) {
        const address = registry.entryAddress(id);
        if (address && registry.focus(address)) {finished = true; return 'requested';}
      }
      return 'waiting';
    },
  };
}
