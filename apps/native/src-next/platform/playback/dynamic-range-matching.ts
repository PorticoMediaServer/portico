import {NativeModules, Platform} from 'react-native';

/** Only tvOS's implemented app-owned AVDisplayManager request is controllable.
 * Android engines currently expose no independent dynamic-range matching port. */
export function nativeDynamicRangeMatchingAvailable(): boolean {
  if (Platform.OS !== 'ios' || !Platform.isTV) return false;
  const module = NativeModules.PorticoPlaybackModule;
  try {return (module?.getConstants?.() ?? module)?.dynamicRangeMatching === true;}
  catch {return false;}
}
