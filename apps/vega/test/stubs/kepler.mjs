// The parts of the SDK's React Native that the bridge names. Nothing is drawn under test.
export const StyleSheet = {absoluteFill: {}};
export const View = 'View';
export const Text = 'Text';
export const Platform = {OS: 'kepler', isTV: true, constants: {Model: 'TEST', Release: '1.2'}, Version: 1};
export const useKeplerAppStateManager = () => ({getComponentInstance: () => ({})});
export const useTVEventHandler = () => {};
const appStateListeners = new Set();
/** The app's place on screen; `AppState.emit('background')` is the test leaving the app. */
export const AppState = {
  currentState: 'active',
  addEventListener(_type, listener) { appStateListeners.add(listener); return {remove: () => appStateListeners.delete(listener)}; },
  emit(state) { this.currentState = state; for (const listener of [...appStateListeners]) listener(state); },
};
