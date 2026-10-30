import type {ReaderAdapter} from '../ports.ts';
import type {EpubDisplayPreferences} from './display.ts';
import type {EpubAdapterOptions} from './adapter.ts';
export type {EpubAdapterOptions} from './adapter.ts';
/** Defer every DOM-dependent engine module until an actual browser open.
 * Importing the common package remains safe in Node and future native hosts. */
export function createEpubAdapter<P extends EpubDisplayPreferences>(options: EpubAdapterOptions): ReaderAdapter<P> {
  return {format:'epub',async open(source) {
    const {createBrowserEpubAdapter}=await import('./adapter.ts');
    return createBrowserEpubAdapter<P>(options).open(source);
  }};
}
