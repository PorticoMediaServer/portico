// The Vega counterpart of `apps/native/src-next/platform/keyboard.tsx`. The touch keyboard
// controller isn't built for Vega, and a television has no touch keyboard to follow: the same names,
// on React Native's own views.
import React, {forwardRef} from 'react';
import {ScrollView, type ScrollViewProps} from 'react-native';

export const touchKeyboard = () => false;

export function KeyboardProvider({children}: {children: React.ReactNode}) {
  return <>{children}</>;
}

export const FormScrollView = forwardRef<ScrollView, ScrollViewProps & {bottomOffset?: number}>(function FormScrollView({bottomOffset: _bottomOffset, ...props}, ref) {
  return <ScrollView ref={ref} {...props}/>;
});

export function useKeyboardHeight(_active: boolean) {
  return 0;
}
