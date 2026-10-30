import {registerCallableModule} from 'react-native';

/**
 * Old-style native view events (RCTDirectEventBlock and friends) reach JavaScript through a
 * callable module named RCTEventEmitter. In the New Architecture only the legacy renderer
 * registers it, and a production bundle never loads that renderer, so the first such event
 * crashed a Release build at launch ("Module has not been registered as callable"). In Debug the
 * development renderer registered it but didn't know the new renderer's views, so these events
 * were dropped there too. Portico's own views report through native-module events instead; this
 * catches any remaining source (a library view) and names it rather than crashing.
 */
registerCallableModule('RCTEventEmitter', {
  receiveEvent(tag: number, name: string) {
    console.warn(`Dropped a legacy view event: ${name} (view ${tag})`);
  },
  receiveTouches() {},
});
