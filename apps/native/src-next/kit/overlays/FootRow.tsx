import React, {Children, Fragment, cloneElement, isValidElement, type ReactNode} from 'react';
import {View} from 'react-native';
import type {OverlayFoot} from '@portico/design';
import {Button} from '../controls/Button';
import {useTheme} from '../theme';

/** The buttons themselves, through any fragment a caller wraps them in (`foot={<>Cancel Delete</>}`). */
const flatten = (children: ReactNode): ReactNode[] => Children.toArray(children).flatMap(child => isValidElement<{children?: ReactNode}>(child) && child.type === Fragment ? flatten(child.props.children) : child ? [child] : []);
/** R9: a foot's buttons as the table lays them out. `halves` and `full` give each button an equal share of the width
 * (one button is the full width); `end` keeps them at their own width at the end, primary last. Never hugging the left. */
export function OverlayFootRow({layout, children}: {layout: OverlayFoot; children: ReactNode}) {
  const theme = useTheme(), items = flatten(children);
  if (layout === 'end') return <View style={{flexDirection: 'row', justifyContent: 'flex-end', gap: theme.space.controlGap}}>{items}</View>;
  // Each button fills its share (the kit Button keeps its own width unless told to stretch); a label too long for a
  // half takes a second line rather than being cut.
  return <View style={{flexDirection: 'row', gap: theme.space.controlGap}}>{items.map((item, index) => <View key={index} style={{flex: 1, minWidth: 0}}>{isValidElement<{align?: string; labelLines?: number}>(item) && item.type === Button ? cloneElement(item, {align: 'stretch', labelLines: 2}) : item}</View>)}</View>;
}

