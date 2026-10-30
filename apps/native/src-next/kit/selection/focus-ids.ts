import {focusTestID} from '../focus/registry';

/** The caller supplies the authority/surface fence; parts are domain keys, never copy or positions. */
export function selectionControlID(prefix: string | undefined, ...parts: readonly string[]): string | undefined {
  return prefix ? focusTestID(prefix, JSON.stringify(parts)) : undefined;
}
