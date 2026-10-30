import type {PreferenceSnapshot} from '@portico/client-core';
import {createReadingController, ReaderError, readerErrorCode, type ReadingController, type ReadingControllerOptions, type ReaderErrorCode} from '@portico/reader-core';
import type {ReaderMountAuthority} from './mount';

/** The selected shell's existing confirmed owner, never an optimistic draft. */
export type ReadPreferenceSource = Readonly<{
  getSnapshot: () => PreferenceSnapshot;
  subscribe: (listener: () => void) => (() => void);
}>;
export type ReadPreferenceBinding = Readonly<{
  controller: ReadingController;
  dispose: () => Promise<void>;
}>;

/** One reader consumes the same confirmed snapshots as Settings. Projection,
 * open-book overrides and their reset remain exclusively inside reader-core. */
export function bindReadPreferences({adapters, preferences, authority, reportError}: Readonly<{
  adapters: ReadingControllerOptions['adapters']; preferences: ReadPreferenceSource;
  authority: ReaderMountAuthority; reportError: (code: ReaderErrorCode) => void;
}>): ReadPreferenceBinding {
  if (!authority.isCurrent()) throw new ReaderError('closed');
  const controller = createReadingController({adapters, preferenceSnapshot: preferences.getSnapshot()});
  let active = true, closing: Promise<void> | undefined;
  let offPreferences = () => {}, offAuthority = () => {};
  const current = () => active && authority.isCurrent();
  const dispose = () => {
    if (closing) return closing;
    active = false; offPreferences(); offAuthority();
    closing = controller.close();
    return closing;
  };
  const failed = (error: unknown) => { if (current()) reportError(readerErrorCode(error)); };
  const refresh = () => {
    if (!current()) return;
    try { void controller.updatePreferenceSnapshot(preferences.getSnapshot()).catch(failed); }
    catch (error) { failed(error); }
  };
  try {
    offAuthority = authority.subscribe(() => { if (!authority.isCurrent()) void dispose().catch(() => {}); });
    if (!current()) { void dispose().catch(() => {}); offAuthority(); }
    else {
      offPreferences = preferences.subscribe(refresh);
      if (!active) offPreferences();
      else refresh(); // Covers confirmation between construction and subscribing.
    }
  } catch (error) { void dispose().catch(() => {}); throw error; }
  return Object.freeze({controller, dispose});
}
