import {Platform} from 'react-native';
import type {ReadNativeHost} from './ReadNativeHost';

/** TV may inspect route modules, but must never evaluate the native WebView
 * package: its native module is intentionally absent from the tvOS Pod graph. */
export function nativeReadHost(): typeof ReadNativeHost {
  if (Platform.isTV) throw new Error('native_read_unavailable');
  return (require('./ReadNativeHost') as typeof import('./ReadNativeHost')).ReadNativeHost;
}
