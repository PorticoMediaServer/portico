import type {FocusAddress, FocusRegistry} from './registry';

/** One system-input opening returns only to its original real keyboard leaf.
 * Retirement releases the proof without moving focus in a successor screen. */
export function retainRecognizerReturn(registry: Pick<FocusRegistry, 'child' | 'retain' | 'focus'>, original: FocusAddress | undefined) {
  const address = original && registry.child(original) ? {...original} : undefined;
  const release = address ? registry.retain(address) : undefined;
  let finished = false;
  return (current: boolean) => {
    if (finished) return;
    finished = true;
    try {if (current && address) registry.focus(address);}
    finally {release?.();}
  };
}
