import React, {type ReactNode} from 'react';
import {View} from 'react-native';
import {Button} from '../controls';
import {BottomSheet, SidePanel} from '../overlays';
import {useTheme} from '../theme';

/** A filters sheet: a side panel on a TV, a bottom sheet on touch, with "Show N results" in its footer once the count is
 * known (that button is the sheet's `show-results` focus host). Closing it any way calls `onClose`. */
export function FilterSheet({title, testID, onClose, showResults, children}: {title: string; testID: string; onClose: () => void;
  showResults?: Readonly<{label: string; onPress: () => void}>; children: ReactNode}) {
  const theme = useTheme();
  const footer = showResults ? <View style={{alignItems: 'stretch'}}><Button focusId="show-results" onPress={showResults.onPress}>{showResults.label}</Button></View> : undefined;
  const change = (next: boolean) => {if (!next) onClose();};
  return theme.isTV ? <SidePanel open title={title} onOpenChange={change} footer={footer} testID={testID}>{children}</SidePanel>
    : <BottomSheet open title={title} onOpenChange={change} footer={footer} testID={testID}>{children}</BottomSheet>;
}
