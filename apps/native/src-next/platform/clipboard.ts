import * as ReactNative from 'react-native';
import type {TurboModule} from 'react-native';

type NativeClipboard = TurboModule & {setString: (text: string) => void | Promise<void>};

export class ClipboardUnavailableError extends Error {
  readonly code = 'clipboard_unavailable';
  constructor() {
    super('The platform clipboard is unavailable.');
    this.name = 'ClipboardUnavailableError';
  }
}

/** Uses the installed RN clipboard bridge; callers present their own catalogue-backed result. */
export async function copyText(text: string): Promise<void> {
  const {Platform, NativeModules, TurboModuleRegistry} = ReactNative;
  // RN's tvOS setString implementation deliberately does nothing.
  if ((Platform.OS !== 'android' && Platform.OS !== 'ios') || (Platform.OS === 'ios' && Platform.isTV)) throw new ClipboardUnavailableError();
  const clipboard = TurboModuleRegistry?.get<NativeClipboard>('Clipboard') ?? (NativeModules.Clipboard as NativeClipboard | undefined);
  if (typeof clipboard?.setString !== 'function') throw new ClipboardUnavailableError();
  await clipboard.setString(text);
}
