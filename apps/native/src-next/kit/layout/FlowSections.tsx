import React, {type ReactNode} from 'react';
import {View, type StyleProp, type ViewStyle} from 'react-native';
import {useTheme} from '../theme';
import {MotionItem} from './MotionItem';
import {flowSectionWidth} from './model';

export type FlowSection = Readonly<{id: string; count: number; cap: number; itemWidth: number; gap?: number; fullWidth?: boolean; grow?: boolean; content: ReactNode}>;
export type FlowSectionsProps = {sections: readonly FlowSection[]; columnGap?: number; rowGap?: number; style?: StyleProp<ViewStyle>; testID?: string};

/** Sections arrive already authored and grouped. Only their drawn geometry is composed here. */
export function FlowSections({sections, columnGap = 40, rowGap = 52, style, testID}: FlowSectionsProps) {
  const theme = useTheme();
  return <View testID={testID} style={[{flexDirection: 'row', flexWrap: 'wrap', alignItems: 'flex-start', columnGap: theme.platform === 'phone' ? 0 : columnGap, rowGap: theme.platform === 'phone' ? 28 : rowGap}, style]}>
    {sections.filter(section => section.count > 0).map(section => <MotionItem mode="section" key={section.id} style={{minWidth: 0, maxWidth: '100%', flexGrow: section.grow ? 1 : 0, width: theme.platform === 'phone' || section.fullWidth ? '100%' : flowSectionWidth(section.count, section.cap, section.itemWidth, section.gap ?? 16)}}>{section.content}</MotionItem>)}
  </View>;
}
