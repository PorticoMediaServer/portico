import {Platform} from 'react-native';
import type {ReaderPage} from '../../pages/touch/reading/ReaderPage';

/** TV may inspect route modules, but must never evaluate the native WebView
 * package: its native module is intentionally absent from the tvOS Pod graph. */
export function nativeReadHost(): typeof ReaderPage {
  if (Platform.isTV) throw new Error('native_read_unavailable');
  return (require('../../pages/touch/reading/ReaderPage') as typeof import('../../pages/touch/reading/ReaderPage')).ReaderPage;
}
