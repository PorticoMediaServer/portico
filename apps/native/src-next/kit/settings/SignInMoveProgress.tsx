import React from 'react';
import {View} from 'react-native';
import {Progress} from '../cards/primitives';
import {useFrameMetrics} from '../frames/metrics';

export function SignInMoveProgress({total, waiting}: {total: number; waiting: number}) {
  const frame = useFrameMetrics(), moved = Math.max(0, total - waiting);
  return <View testID="server-signin-progress" accessibilityRole="progressbar" accessibilityValue={{min: 0, max: total, now: moved}}
    style={{paddingHorizontal: frame.cardPadding}}>
    <Progress value={total > 0 ? moved / total : 0}/>
  </View>;
}
