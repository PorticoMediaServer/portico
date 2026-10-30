import {readingPreferences, type PreferenceSnapshot, type ReadingPreferences} from '@portico/client-core';
import {ReaderController} from './controller.ts';
import {ReaderError} from './errors.ts';
import type {ReaderAdapter} from './ports.ts';

export type ReadingControllerOptions = Readonly<{
  adapters: readonly ReaderAdapter<ReadingPreferences>[];
  /** Already confirmed by the selected viewer's preference owner. */
  preferenceSnapshot: PreferenceSnapshot;
}>;

/** One selected viewer scope, one existing session controller. No transport,
 * Settings writer, raw-key mapping, defaults or independent preference store. */
export type ReadingController = Omit<ReaderController<ReadingPreferences>, 'updatePreferences'> & {
  /** Forward confirmed Settings snapshots only, never optimistic patches.
   * Existing open-book overrides retain precedence until cleared or closed. */
  updatePreferenceSnapshot(snapshot: PreferenceSnapshot): Promise<void>;
};

function project(snapshot: PreferenceSnapshot): ReadingPreferences {
  try { return readingPreferences(snapshot); }
  catch (cause) { throw new ReaderError('invalid_preferences', {cause}); }
}

class ConfirmedReadingController extends ReaderController<ReadingPreferences> {
  private confirmedSnapshot: PreferenceSnapshot;
  constructor(options: ReadingControllerOptions) {
    super({adapters: options.adapters, preferences: project(options.preferenceSnapshot)});
    this.confirmedSnapshot = options.preferenceSnapshot;
  }
  protected override preferencesAtOpen(): ReadingPreferences {
    return project(this.confirmedSnapshot);
  }
  updatePreferenceSnapshot(snapshot: PreferenceSnapshot): Promise<void> {
    const preferences = project(snapshot);
    this.confirmedSnapshot = snapshot;
    return super.updatePreferences(preferences);
  }
}

/** Recreate after viewer/server authority changes. The scope owner retains its
 * normal confirmation/revision fence; this reader never saves preferences. */
export function createReadingController(options: ReadingControllerOptions): ReadingController {
  return new ConfirmedReadingController(options);
}
