import {NativeModules, Platform} from 'react-native';
import type {TextEnvironment} from '../../../../../packages/client-core/src/presentation/kit-model';

type Owner = Readonly<{current: () => boolean; subscribePreferences: (listener: () => void) => () => void}>;

/** The existing tvOS producer owns persistence and shelf invalidation. This
 * caller lends it only the resolved UI locale, never a token or viewer record. */
export function bindNativeTopShelfLocale(owner: Owner, text: TextEnvironment, reportError: (error: unknown) => void): (() => void) | undefined {
  if (Platform.OS !== 'ios' || !Platform.isTV || !owner.current()) return;
  const native = NativeModules.PorticoTopShelf as {setLocale?: (tag: string) => void} | undefined;
  if (typeof native?.setLocale !== 'function') return;
  let live = true, delivered: string | undefined;
  const update = () => {
    if (!live || !owner.current()) return;
    try {
      const locale = text.locale;
      if (!locale || locale === delivered) return;
      native.setLocale!(locale);
      delivered = locale;
    } catch (error) {if (live && owner.current()) reportError(error);}
  };
  const unsubscribe = owner.subscribePreferences(update);
  update();
  return () => {if (live) {live = false; unsubscribe();}};
}
