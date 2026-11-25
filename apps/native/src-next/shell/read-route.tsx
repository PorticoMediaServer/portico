import {Platform} from 'react-native';
import type {NativeShellBootstrapContext} from './bootstrap';
import type {PreferenceSnapshot} from '../../../../packages/client-core/src/preferences';
import {nativeShellRoutes} from './route-registry';
import {nativeReadingSettingsPreferences, openNativeReadingSettings} from './reading-settings-route';
import {createNativeReadRenderer} from './read-renderer';
import './read-destinations';

/** Read and Settings borrow the authority's ONE confirmed preference source.
 * Importing this handheld-only module registers factories and starts no I/O. */
export function createNativeReadModule(context: NativeShellBootstrapContext) {
  // A book still opens when its settings cannot be read: it shows the server's defaults, and says nothing.
  let preferences: Readonly<{getSnapshot: () => PreferenceSnapshot; subscribe: (listener: () => void) => () => void}>;
  try {
    const source = nativeReadingSettingsPreferences(context);
    preferences = Object.freeze({getSnapshot: source.getPreferenceSnapshot, subscribe: source.subscribe});
  } catch {preferences = Object.freeze({getSnapshot: context.preferences, subscribe: context.subscribePreferences});}
  return createNativeReadRenderer(context, preferences, () => {openNativeReadingSettings(context);});
}

if (!Platform?.isTV && !nativeShellRoutes.hasRoute?.('read')) nativeShellRoutes.registerRoute('read', createNativeReadModule);
