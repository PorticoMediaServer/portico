import React from 'react';
import {View, useWindowDimensions, type StyleProp, type ViewStyle} from 'react-native';
import {defaultI18n} from '../../../../../packages/i18n/src/index';
import {AmbientArt} from '../artwork/index';
import {useTheme} from '../theme';
import {Lockup} from '../layout/index';
import type {PressedSkeletonContent} from './HomeSkeleton';
import {GridSkeleton} from './GridSkeleton';
import {SkeletonBlock} from './SkeletonBlock';
import {isSlowLoading, showSkeleton, skeletonTiming} from './model';
import type {StateAction} from './types';
import {StateActionButton} from './StateActionButton';

export type TitleSkeletonProps = Readonly<{
  elapsedMs: number;
  pressed: PressedSkeletonContent;
  shell?: React.ReactNode;
  /** Actual tabs for the known title kind, with their controlled callbacks. */
  tabs?: React.ReactNode;
  /** TV Play is already possible from the pressed card and must take real focus. */
  playAction?: StateAction;
  slowNotice?: React.ReactNode;
  children?: React.ReactNode;
  visible?: boolean;
  style?: StyleProp<ViewStyle>;
  testID?: string;
}>;

/** Keep the pressed title and sharp artwork while its remaining details load. */
export function TitleSkeleton({elapsedMs, pressed, shell, tabs, playAction, slowNotice, children, visible = true, style, testID}: TitleSkeletonProps) {
  const theme = useTheme();
  const {width, height} = useWindowDimensions();
  const tv = theme.isTV;
  const show = showSkeleton(elapsedMs);
  return <View testID={testID} accessibilityLabel={defaultI18n.t('status.loading')} accessibilityState={{busy: true}}
    style={[{flex: 1, overflow: 'hidden', backgroundColor: theme.colors.ink}, style]}>
    {pressed.art && <View pointerEvents="none" style={{position: 'absolute', left: 0, top: 0, width, height: tv ? 900 : 396}}><AmbientArt {...pressed.art} visible={visible && pressed.art.visible !== false} arrivalDurationMs={skeletonTiming.pictureFadeMs} treatment={tv ? 'title' : 'phone'} priority /></View>}
    {shell}
    <View style={{position: 'absolute', left: tv ? theme.gutter : 20, right: tv ? 96 : 20, top: tv ? 150 : 300, alignItems: tv ? 'flex-start' : 'center'}}>
      <Lockup title={pressed.title} long={pressed.longTitle} style={{textAlign: tv ? 'left' : 'center', marginBottom: tv ? 26 : 12}} />
      {show && <>
        <SkeletonBlock width={tv ? 460 : 210} height={tv ? 24 : 11} radius={tv ? 12 : 6} />
        {tv && <>
          <SkeletonBlock width={820} height={22} radius={11} style={{marginTop: 34}} />
          <SkeletonBlock width={760} height={22} radius={11} style={{marginTop: 14}} />
          <SkeletonBlock width={520} height={22} radius={11} style={{marginTop: 14}} />
        </>}
      </>}
      {tv && <View style={{flexDirection: 'row', alignItems: 'center', gap: 18, marginTop: 44}}>
        {playAction && <StateActionButton testID={testID} action={{...playAction, icon: playAction.icon ?? 'i-play', hasTVPreferredFocus: playAction.hasTVPreferredFocus ?? true}} variant="primary" size="hero" />}
        {show && <><SkeletonBlock width={200} height={72} radius={14} shine /><SkeletonBlock width={72} height={72} radius={14} /><SkeletonBlock width={72} height={72} radius={14} /></>}
      </View>}
    </View>
    {!tv && <View style={{position: 'absolute', left: 20, right: 20, top: 398}}>
      {show && <>
        <View style={{flexDirection: 'row', gap: 10}}>
          <SkeletonBlock height={46} shine style={{flex: 1}} />
          <SkeletonBlock width={46} height={46} /><SkeletonBlock width={46} height={46} /><SkeletonBlock width={46} height={46} />
        </View>
        <SkeletonBlock width="100%" height={14} radius={theme.platform === 'phone' ? 7 : undefined} style={{marginTop: 20}} />
        <SkeletonBlock width="70%" height={14} radius={theme.platform === 'phone' ? 7 : undefined} style={{marginTop: 9}} />
      </>}
      <View style={{marginTop: 22}}>{tabs}</View>
      {children ?? (show ? <View style={{flexDirection: 'row', alignItems: 'center', gap: 14, marginTop: 18}}>
        <SkeletonBlock width={136} height={136 * 9 / 16} />
        <View style={{flex: 1}}><SkeletonBlock width="80%" height={14} radius={theme.platform === 'phone' ? 7 : undefined} /><SkeletonBlock width="40%" height={11} radius={6} style={{marginTop: 9}} /></View>
      </View> : null)}
    </View>}
    {tv && <>
      <View style={{position: 'absolute', left: theme.gutter, right: 96, top: 742}}>{tabs}</View>
      <View style={{position: 'absolute', left: theme.gutter, right: 0, top: 826}}>{children ?? <GridSkeleton elapsedMs={elapsedMs} scrollOffset={0} viewportHeight={Math.max(0, height - 826)} width={width - theme.gutter} columns={7} cardWidth={214} gap={28} captions={false} />}</View>
    </>}
    {isSlowLoading(elapsedMs) && slowNotice}
  </View>;
}
