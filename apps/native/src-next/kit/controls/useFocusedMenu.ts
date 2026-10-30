import {useEffect, useRef} from 'react';
import type {PressableProps} from 'react-native';
import {registerFocusedMenu} from '../../platform/menu-key';

/** The broker must never retain an action after its real control loses focus. */
export function useFocusedMenu(isTV: boolean, focused: boolean, disabled: PressableProps['disabled'], action: (() => void) | undefined): void {
  const owner = useRef({});
  useEffect(() => {
    if (isTV && focused && !disabled && action) return registerFocusedMenu(owner.current, action);
  }, [isTV, focused, disabled, action]);
}
