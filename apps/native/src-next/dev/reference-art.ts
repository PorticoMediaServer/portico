import {NativeModules, Platform} from 'react-native';

/** Where the gallery's reference artwork is served (the mock-ups' `design/ui-v1/assets`, on mini-1 port 8787).
 * An Android phone on USB loads its bundle through `adb reverse` from localhost and cannot reach the Tailscale
 * address, so it asks localhost too (`adb reverse tcp:8787 tcp:8787`, the art server listening on 127.0.0.1:8787).
 * Emulators and the Apple simulators reach the Tailscale address. Development builds only. */
const scriptURL: string = NativeModules.SourceCode?.getConstants?.().scriptURL ?? NativeModules.SourceCode?.scriptURL ?? '';
const overUSB = Platform.OS === 'android' && /^https?:\/\/(localhost|127\.0\.0\.1)[:/]/.test(scriptURL);
export const referenceOrigin = overUSB ? 'http://127.0.0.1:8787' : 'http://100.120.50.24:8787';
