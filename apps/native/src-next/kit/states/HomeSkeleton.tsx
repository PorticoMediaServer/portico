import React from 'react';
import {Text, View, useWindowDimensions, type StyleProp, type ViewStyle} from 'react-native';
import {defaultI18n} from '../../../../../packages/i18n/src/index';
import {AmbientArt, StandardBackdrop, type AmbientArtProps} from '../artwork/index';
import {useTheme} from '../theme';
import {Lockup} from '../layout/index';
import {GridSkeleton} from './GridSkeleton';
import {SkeletonBlock} from './SkeletonBlock';
import {isSlowLoading, showSkeleton, skeletonTiming} from './model';

export type PressedSkeletonContent = Readonly<{
  title: string;
  longTitle?: boolean;
  subtitle?: React.ReactNode;
  art?: AmbientArtProps;
}>;
export type HomeSkeletonProps = Readonly<{
  elapsedMs: number;
  pressed?: PressedSkeletonContent;
  shell?: React.ReactNode;
  slowNotice?: React.ReactNode;
  rowTitle?: React.ReactNode;
  renderArrived?: (index: number) => React.ReactNode;
  visible?: boolean;
  style?: StyleProp<ViewStyle>;
  testID?: string;
}>;

/** Home's real loading geometry. It never composes a Home response or supplies sample cards. */
export function HomeSkeleton({elapsedMs, pressed, shell, slowNotice, rowTitle, renderArrived, visible = true, style, testID}: HomeSkeletonProps) {
  const theme = useTheme();
  const {width, height} = useWindowDimensions();
  const tv = theme.isTV;
  const show = showSkeleton(elapsedMs);
  const gutter = theme.gutter;
  const wideWidth = tv ? 400 : 262;
  const wideGap = tv ? 28 : 12;
  const wideCount = Math.ceil((width - gutter) / (wideWidth + wideGap)) + 1;
  const rowTop = tv ? 588 : 586;
  return <View testID={testID} accessibilityState={{busy: true}} accessibilityLabel={defaultI18n.t('status.loading')}
    style={[{flex: 1, overflow: 'hidden', backgroundColor: theme.colors.ink}, style]}>
    {/* The standard backdrop (Justin, 29 Oct): never a screen-specific loading glow. Pressed art layers over it. */}
    <StandardBackdrop />
    {pressed?.art && <View pointerEvents="none" style={{position: 'absolute', left: 0, top: 0, width, height: tv ? 900 : 560}}><AmbientArt {...pressed.art} visible={visible && pressed.art.visible !== false} arrivalDurationMs={skeletonTiming.pictureFadeMs} treatment={tv ? 'hero' : 'phone'} /></View>}
    {shell}
    {tv && pressed ? <View style={{position: 'absolute', left: gutter, top: 150, width: 960}}>
      {pressed.subtitle != null && <View style={{marginBottom: 16}}>{typeof pressed.subtitle === 'string' || typeof pressed.subtitle === 'number'
        ? <Text style={[theme.type.meta, {fontSize: 24 * theme.textScale, color: theme.colors.accentStrong}]}>{pressed.subtitle}</Text>
        : pressed.subtitle}</View>}
      <Lockup title={pressed.title} long={pressed.longTitle} style={{marginBottom: 22}} />
      {show && <>
        <SkeletonBlock width={420} height={24} radius={12} />
        <SkeletonBlock width={780} height={22} radius={11} style={{marginTop: 30}} />
        <SkeletonBlock width={620} height={22} radius={11} style={{marginTop: 14}} />
      </>}
    </View> : show && <View style={{position: 'absolute', left: tv ? gutter : 0, right: tv ? undefined : 0, top: tv ? 220 : 330, alignItems: tv ? 'flex-start' : 'center', gap: 12}}>
      <SkeletonBlock width={tv ? 340 : 170} height={tv ? 22 : 11} radius={tv ? 11 : 6} accent />
      <SkeletonBlock width={tv ? 780 : 270} height={tv ? 100 : 44} radius={12} shine style={{marginTop: 4}} />
      <SkeletonBlock width={tv ? 440 : 180} height={tv ? 24 : 11} radius={tv ? 12 : 6} style={{marginTop: 4}} />
      <View style={{flexDirection: 'row', gap: tv ? 18 : 10, marginTop: 10}}>
        <SkeletonBlock width={tv ? 196 : 98} height={tv ? 72 : 46} />
        <SkeletonBlock width={tv ? 236 : 118} height={tv ? 72 : 46} />
      </View>
    </View>}
    <View style={{position: 'absolute', left: 0, right: 0, top: rowTop}}>
      <View style={{paddingLeft: gutter, marginBottom: tv ? 22 : 4}}>
        {rowTitle != null ? <Text style={[theme.type.rowHeading, {color: theme.colors.text}]}>{rowTitle}</Text> : show ? <SkeletonBlock width={tv ? 300 : 170} height={tv ? 30 : 18} radius={theme.platform === 'phone' ? 7 : undefined} /> : null}
      </View>
      <View style={{flexDirection: 'row', gap: wideGap, paddingTop: tv ? 0 : 8, paddingLeft: gutter}}>
        {Array.from({length: wideCount}, (_, index) => {
          const arrived = renderArrived?.(index);
          return <View key={index} style={{width: wideWidth}}>{arrived ?? (show ? <SkeletonBlock width={wideWidth} height={wideWidth * 9 / 16} shine={index === 2} /> : null)}</View>;
        })}
      </View>
      {show && <View style={{marginTop: tv ? 44 : 26, paddingLeft: gutter}}>
        <SkeletonBlock width={tv ? 300 : 170} height={tv ? 30 : 18} radius={theme.platform === 'phone' ? 7 : undefined} style={{marginBottom: tv ? 22 : 12}} />
        <GridSkeleton elapsedMs={elapsedMs} width={width - gutter} viewportHeight={Math.max(0, height - rowTop - wideWidth * 9 / 16 - 120)} scrollOffset={0}
          cardWidth={tv ? 214 : 118} columns={Math.ceil((width - gutter) / ((tv ? 214 : 118) + wideGap))} gap={wideGap} captions={false} />
      </View>}
    </View>
    {isSlowLoading(elapsedMs) && slowNotice}
  </View>;
}
