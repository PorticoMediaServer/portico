import type {NativeShellBootstrapContext} from '../../shell/bootstrap';
import {nativeShellRoutes} from '../../shell/route-registry';
import {nativeReadingSettingsPreferences, openNativeReadingSettings} from '../settings/ReadingSettingsRoute';
import {createNativeReadRenderer} from './ReadRoute';
import './read-destinations';

/** Read and Settings borrow the authority's ONE confirmed preference source.
 * Importing this handheld-only module registers factories and starts no I/O. */
export function createNativeReadModule(context: NativeShellBootstrapContext) {
  const source = nativeReadingSettingsPreferences(context);
  const preferences = Object.freeze({getSnapshot: source.getPreferenceSnapshot, subscribe: source.subscribe});
  return createNativeReadRenderer(context, preferences, () => {openNativeReadingSettings(context);});
}

nativeShellRoutes.registerRoute('read', createNativeReadModule);
