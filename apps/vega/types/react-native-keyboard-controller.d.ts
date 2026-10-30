// The touch keyboard controller is never bundled for Vega (src/overrides/native-next/platform/keyboard.tsx
// replaces its one importer); the shared file still type-checks against the names it uses.
declare module 'react-native-keyboard-controller' {
  import type {ComponentType, ReactNode} from 'react';
  import type {ScrollViewProps} from 'react-native';
  export type KeyboardEventData = {height: number; duration: number; timestamp: number; target: number};
  export const KeyboardProvider: ComponentType<{children?: ReactNode}>;
  export const KeyboardAwareScrollView: ComponentType<ScrollViewProps & {bottomOffset?: number; ref?: unknown}>;
  export const KeyboardEvents: {addListener(name: 'keyboardWillShow' | 'keyboardDidShow' | 'keyboardWillHide' | 'keyboardDidHide', listener: (event: KeyboardEventData) => void): {remove(): void}};
}
