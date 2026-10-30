import type {ReadingPreferences} from '@portico/client-core';
import type {ReadingController} from '@portico/reader-core';
import type {ReadReaderProps} from '../../src/screens/read/ReadReader';
import type {ReaderSurfaceProps} from '../../src/screens/read/ReaderSurface';
import type {ReadPagePreviewOptions} from '../../src/screens/read/preview';

/** Consumer-only type regression: the public confirmed facade must not require
 * private controller fields or expose the low-level preference updater. */
export function readingControllerConsumer(controller: ReadingController) {
  const reader: ReadReaderProps<ReadingPreferences>['controller'] = controller;
  const surface: ReaderSurfaceProps<ReadingPreferences>['controller'] = controller;
  const preview: ReadPagePreviewOptions<ReadingPreferences>['controller'] = controller;
  return {reader, surface, preview};
}
