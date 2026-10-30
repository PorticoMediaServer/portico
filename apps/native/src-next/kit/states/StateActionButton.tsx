import React from 'react';
import {focusTestID} from '../focus/registry';
import {Button, type ButtonProps} from '../controls/Button';
import type {StateAction} from './types';

/** Keep semantic action IDs and copy out of the native Pressable's props. The same
 * effective identity is given to the control as its testID and its focusId: a
 * shared action inside a FocusRegion must never reach the region without one, or
 * the region throws instead of drawing the retry it was asked for. The caller's own
 * testID still wins, the derived control id is unchanged, and only when neither
 * exists does the caller's already-required semantic action id supply it. */
export function StateActionButton({action, ...props}: {action: StateAction} & Pick<ButtonProps, 'variant' | 'size' | 'style' | 'textStyle' | 'hitSlop' | 'iconSize' | 'iconColor' | 'testID'>) {
  const {id, label, ...native} = action, testID = native.testID ?? controlID(props.testID, 'action', id);
  return <Button {...props} {...native} testID={testID} focusId={testID ?? id}>{label}</Button>;
}

const controlID = (prefix: string | undefined, ...parts: readonly string[]) => prefix ? focusTestID(prefix, JSON.stringify(parts)) : undefined;
