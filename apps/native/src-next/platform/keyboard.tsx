import React, {forwardRef, useEffect, useState} from 'react';
import * as ReactNative from 'react-native';
import {Platform, ScrollView, type ScrollViewProps} from 'react-native';
import {KeyboardAwareScrollView, KeyboardEvents, KeyboardProvider as ControllerProvider, type KeyboardEventData} from 'react-native-keyboard-controller';

/** Touch keyboards follow react-native-keyboard-controller (P2-70 LIB-2): the keyboard's own
 * frames, with Will events on Android as well as iOS, in an edge-to-edge window. Televisions keep
 * React Native's views: tvOS doesn't link the library and Vega replaces this file. */
export const touchKeyboard = () => !Platform.isTV;

export function KeyboardProvider({children}: {children: React.ReactNode}) {
  return touchKeyboard() ? <ControllerProvider>{children}</ControllerProvider> : <>{children}</>;
}

/** A form's scroll view: the focused field stays above the keyboard on both platforms. */
export const FormScrollView = forwardRef<ScrollView, ScrollViewProps & {bottomOffset?: number}>(function FormScrollView({bottomOffset = 24, ...props}, ref) {
  if (!touchKeyboard()) return <ScrollView ref={ref} {...props}/>;
  return <KeyboardAwareScrollView ref={ref as never} bottomOffset={bottomOffset} {...props}/>;
});

/** The keyboard's height, set as it starts to move. Layout changes made in the same frame
 * ride the keyboard's own duration. Only phone sheets turn it on. */
export function useKeyboardHeight(active: boolean) {
  const [inset, setInset] = useState(0);
  useEffect(() => {
    if (!active) {setInset(0); return;}
    const follow = (event: KeyboardEventData, height: number) => {
      const layout = ReactNative.LayoutAnimation;
      if (event.duration > 0 && typeof layout?.configureNext === 'function')
        layout.configureNext({duration: event.duration, update: {duration: event.duration, type: layout.Types.keyboard}});
      setInset(height);
    };
    const show = KeyboardEvents.addListener('keyboardWillShow', event => follow(event, event.height));
    const hide = KeyboardEvents.addListener('keyboardWillHide', event => follow(event, 0));
    return () => {show.remove(); hide.remove();};
  }, [active]);
  return inset;
}
