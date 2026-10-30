// Node test stand-in for react-native-keyboard-controller: its CommonJS build requires
// `react-native`, which these tests replace with ES modules. The app's own wrapper
// (src-next/platform/keyboard.tsx) is still the code under test.
import React from 'react';
import {ScrollView} from 'react-native';

export const KeyboardProvider = ({children}) => React.createElement(React.Fragment, null, children);
export const KeyboardAwareScrollView = React.forwardRef(({bottomOffset: _bottomOffset, ...props}, ref) => React.createElement(ScrollView, {...props, ref}));
// A test may install a Map to receive the listeners and drive them.
export const KeyboardEvents = {addListener(name, listener) {
  const listeners = globalThis.__porticoKeyboardControllerListeners;
  listeners?.set(name, listener);
  return {remove() {if (listeners?.get(name) === listener) listeners.delete(name);}};
}};
