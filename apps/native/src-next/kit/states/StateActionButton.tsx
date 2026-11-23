import React, {forwardRef} from 'react';
import type {View} from 'react-native';
import {focusTestID} from '../focus/registry';
import {Button, type ButtonProps} from '../controls/Button';
import type {StateAction} from './types';

/** Keep semantic action IDs and copy out of the native Pressable's props. The same
 * effective identity is given to the control as its testID and its focusId: a
 * shared action inside a FocusRegion must never reach the region without one, or
 * the region throws instead of drawing the retry it was asked for. The caller's own
 * testID still wins, the derived control id is unchanged, and only when neither
 * exists does the caller's already-required semantic action id supply it. */
export const StateActionButton = forwardRef<View, {action: StateAction} & Pick<ButtonProps, 'variant' | 'size' | 'align' | 'style' | 'textStyle' | 'hitSlop' | 'iconSize' | 'iconColor' | 'testID'>>(function StateActionButton({action, ...props}, ref) {
  const {id, label, ...native} = action, testID = native.testID ?? controlID(props.testID, 'action', id);
  // The forwarded ref wins over an action's own, so a layout aiming at its primary always gets the host.
  return <Button {...props} {...native} ref={ref ?? native.ref} testID={testID} focusId={testID ?? id}>{label}</Button>;
});

const controlID = (prefix: string | undefined, ...parts: readonly string[]) => prefix ? focusTestID(prefix, JSON.stringify(parts)) : undefined;
