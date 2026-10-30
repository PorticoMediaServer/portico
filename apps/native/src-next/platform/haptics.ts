import {NativeModules, Platform} from 'react-native';

/** True means the OS admitted the feedback request, never proof that hardware vibrated. */
export async function selectionHaptic(enabled = true): Promise<boolean> {
  if (!enabled || Platform.isTV || (Platform.OS !== 'android' && Platform.OS !== 'ios')) return false;
  const bridge = NativeModules.PorticoHaptics as {selection?: () => Promise<boolean>} | undefined;
  if (typeof bridge?.selection !== 'function') return false;
  try { return await bridge.selection() === true; } catch { return false; }
}

/** Q6: one firm menu request. True means submitted, never measured hardware delivery. */
export async function touchMenuHaptic(enabled = true): Promise<boolean> {
  if (!enabled || Platform.isTV || (Platform.OS !== 'android' && Platform.OS !== 'ios')) return false;
  const bridge = NativeModules.PorticoHaptics as {selection?: () => Promise<boolean>; contextMenu?: () => Promise<boolean>} | undefined;
  try {
    // Android's existing bridge uses HapticFeedbackConstants.LONG_PRESS.
    if (Platform.OS === 'android') return typeof bridge?.selection === 'function' && await bridge.selection() === true;
    return typeof bridge?.contextMenu === 'function' && await bridge.contextMenu() === true;
  } catch {return false;}
}
