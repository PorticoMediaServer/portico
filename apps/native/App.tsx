import React from 'react';
import * as ReactNative from 'react-native';
import NativeApp, {type NativeAppProps} from './src-next/App';
import {getLiteEffects, setLiteEffects} from './src-next/platform/motion-policy';
import {useMenuKey} from './src-next/platform/menu-key';

// Developer switch for the lite (performance) effects tier until device detection exists.
// A build can also start in lite with globalThis.__PORTICO_EFFECTS_TIER__ = 'lite'.
const devSettings = (ReactNative as {DevSettings?: {addMenuItem?: (title: string, handler: () => void) => void}}).DevSettings;
if (typeof __DEV__ !== 'undefined' && __DEV__ && typeof devSettings?.addMenuItem === 'function') {
  devSettings.addMenuItem('Toggle lite effects', () => setLiteEffects(!getLiteEffects()));
}

export default function App(props: NativeAppProps) {
  useMenuKey();
  return <NativeApp {...props} />;
}
