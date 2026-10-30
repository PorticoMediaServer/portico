import {Platform} from 'react-native';
import type {NativeShellBootstrapContext} from '../../shell/bootstrap';
import {nativeReadingPreferencesAvailable} from './reading-preferences';

/** A real Settings child route; Back restores the retained publication's exact history. */
export function openNativeReadingSettings(context: Pick<NativeShellBootstrapContext, 'current' | 'preferences' | 'navigation' | 'authority'>): boolean {
  if (Platform.isTV || !context.current() || !nativeReadingPreferencesAvailable(context.preferences())) return false;
  return context.navigation.open({kind: 'settings-page', scope: 'account', page: 'reading'}, context.authority());
}
