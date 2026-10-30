import React, {useEffect} from 'react';
import {Platform, Text, View, type StyleProp, type ViewStyle} from 'react-native';
import Animated, {useAnimatedStyle, useSharedValue, withTiming} from 'react-native-reanimated';
import {ColourBackdrop} from '../artwork';
import {Icon} from '../Icon';
import {nativeType, useTheme} from '../theme';
import {useMotion} from '../useMotion';
import {BlurView, glassRecipe} from '../../platform/native-blur';
import {useKeyboardHeight} from '../../platform/keyboard';
import {CountdownRing} from './CountdownRing';
import type {StateAction, StateContentProps} from './types';
import {StateActionButton} from './StateActionButton';

export type StateRetry = Readonly<{
  progress: number | null;
  label: React.ReactNode;
  detail?: React.ReactNode;
}>;
export type StateLayoutProps = StateContentProps & Readonly<{
  /** A containing transition may focus the real heading after its cover leaves. */
  titleRef?: React.Ref<Text>;
  backdropShape?: 'side' | 'round' | 'top' | 'none';
  backdropHeight?: number;
  shell?: React.ReactNode;
  alive?: React.ReactNode;
  retry?: StateRetry;
  beforeActions?: React.ReactNode;
  detailsAction?: StateAction;
  /** Compact centered link; actual action identity and callback remain caller-owned. */
  detailsPresentation?: 'row' | 'link';
  details?: React.ReactNode;
  help?: React.ReactNode;
  kicker?: React.ReactNode;
  visible?: boolean;
  /** Exact known content position, independent of the surrounding shell. */
  contentTop?: number;
  contentStyle?: StyleProp<ViewStyle>;
  /** A page that takes typing (the PIN page) centres in the space the keyboard leaves, so its last button stays tappable. */
  avoidKeyboard?: boolean;
}> & (Readonly<{variant?: 'page'; frozenPicture?: never; playerChrome?: never}> | Readonly<{variant: 'player'; frozenPicture: React.ReactNode; playerChrome?: React.ReactNode}>);

/** Full-page state anatomy; all recovery, retry timing and navigation are supplied. */
export function StateLayout({title, body, icon, tone = 'neutral', primaryAction, secondaryAction, additionalActions, variant = 'page', frozenPicture, playerChrome,
  backdropShape, backdropHeight, shell, alive, retry, beforeActions, detailsAction, detailsPresentation = 'row', details, help, kicker, children, visible = true, contentTop, contentStyle, avoidKeyboard = false, style, testID, titleRef}: StateLayoutProps) {
  const theme = useTheme();
  const policy = useMotion();
  const tv = theme.isTV;
  const keyboardHeight = useKeyboardHeight(avoidKeyboard && !tv);
  const player = variant === 'player';
  const opacity = useSharedValue(visible ? 1 : 0);
  useEffect(() => {opacity.value = withTiming(visible ? 1 : 0, policy.timing(visible ? 'standard' : 'quick'));}, [visible, opacity, policy]);
  const animated = useAnimatedStyle(() => ({opacity: opacity.value}));
  const iconSize = tv ? 88 : player ? 42 : 54;
  const foreground = tone === 'warning' ? theme.colors.amber : tone === 'error' ? theme.colors.record : tone === 'accent' ? theme.colors.accentStrong : theme.colors.text;
  const glass = glassRecipe();
  const androidGlass = glass.nativeBlur && Platform.OS === 'android';
  const emblem = icon ? <Icon name={icon} color={foreground} size={tv ? 40 : player ? 22 : 26} /> : null;
  const headingSize = tv ? player ? 72 : 88 : player ? 26 : 30;
  // Android draws a line of exactly the font size with its descenders clipped; give TV headings room and take it back from the margin, so the layout does not move.
  const descenderRoom = tv && Platform.OS === 'android' ? Math.ceil(headingSize * .12) : 0;
  const bodySize = tv ? 28 : player ? 14 : 15;
  const stacked = !tv && !player;
  const shape = backdropShape ?? (tv ? 'side' : 'top');
  const left = tv ? player ? 96 : theme.gutter : player ? 64 : 24;
  const buttonStyle: ViewStyle | undefined = stacked ? {width: '100%', height: 50} : player && !tv ? {height: 40, paddingHorizontal: 16} : undefined;
  const action = (item: StateAction, primary: boolean) => <StateActionButton testID={testID} key={item.id} action={visible ? item : {...item, disabled: true}} size={player && !tv ? 'small' : 'hero'} variant={primary ? 'primary' : 'glass'} style={buttonStyle} textStyle={player && !tv ? {fontSize: 14.5 * theme.textScale} : undefined} />;
  return <View testID={testID} style={[{flex: 1, backgroundColor: theme.colors.ink, overflow: 'hidden'}, style]}>
    <View pointerEvents="none" style={{position: 'absolute', inset: 0}}>
      {player ? frozenPicture : shape !== 'none' ? <ColourBackdrop shape={shape} height={backdropHeight} fill={shape !== 'top'} /> : null}
    </View>
    {shell}
    <Animated.View pointerEvents={visible ? 'box-none' : 'none'} accessibilityElementsHidden={!visible} importantForAccessibility={visible ? 'auto' : 'no-hide-descendants'}
      style={[{position: 'absolute', inset: 0, justifyContent: contentTop === undefined ? 'center' : 'flex-start', paddingTop: contentTop, paddingBottom: keyboardHeight}, animated]}>
      <View style={[{marginLeft: left, marginRight: stacked ? 24 : tv ? 96 : undefined, width: stacked ? undefined : tv ? 1000 : 380, alignItems: stacked ? 'center' : 'flex-start'}, contentStyle]}>
        {kicker != null && <View style={{marginBottom: tv ? 24 : 18}}>{kicker}</View>}
        {icon && <View style={{width: iconSize, height: iconSize, borderRadius: theme.baseRadii.pill, overflow: 'hidden', alignItems: 'center', justifyContent: 'center',
          backgroundColor: androidGlass ? 'transparent' : `${theme.colors.text}1A`, borderWidth: androidGlass ? 0 : 1, borderColor: `${theme.colors.text}1F`, marginBottom: tv ? 34 : player ? 12 : 18}}>
          {androidGlass ? <BlurView pointerEvents="none" blurType="dark" blurAmount={20} overlayColor={`${theme.colors.text}1A`} reducedTransparencyFallbackColor={theme.colors.raised} style={{position: 'absolute', inset: 0}}>
            <View collapsable={false} style={{position: 'absolute', inset: 0, alignItems: 'center', justifyContent: 'center'}}>
              <View pointerEvents="none" style={{position: 'absolute', inset: 0, borderRadius: theme.baseRadii.pill, borderWidth: 1, borderColor: `${theme.colors.text}1F`}} />
              {emblem}
            </View>
          </BlurView> : <>
            {glass.nativeBlur && <BlurView pointerEvents="none" blurType="dark" blurAmount={20} reducedTransparencyFallbackColor={theme.colors.raised} style={{position: 'absolute', inset: 0}} />}
            {emblem}
          </>}
        </View>}
        <Text ref={titleRef} accessibilityRole="header" textBreakStrategy="balanced" android_hyphenationFrequency="none" style={[{includeFontPadding: false}, nativeType({size: headingSize, lineHeight: tv ? headingSize + descenderRoom : headingSize * 1.08, weight: 800, tracking: tv ? -.045 : player ? -.03 : -.035}, theme.textScale),
          {color: theme.colors.text, textAlign: stacked ? 'center' : 'left', marginBottom: tv ? 24 - descenderRoom : player ? 8 : 10}]}>{title}</Text>
        {body != null && <Text style={[{includeFontPadding: false},nativeType({size: bodySize, lineHeight: bodySize * (tv ? 1.45 : 1.5), weight: 400, tracking: 0}, theme.textScale),
          {maxWidth: tv ? 900 : undefined, color: theme.colors.textSecondary, textAlign: stacked ? 'center' : 'left', marginBottom: tv ? 28 : player ? 14 : 16}]}>{body}</Text>}
        {retry && <View style={{flexDirection: 'row', flexWrap: 'wrap', alignItems: 'center', justifyContent: stacked ? 'center' : 'flex-start', gap: tv ? 16 : 12, marginBottom: tv ? 40 : 22}}>
          <CountdownRing progress={retry.progress} />
          <Text style={[{includeFontPadding: false},nativeType({size: tv ? 24 : 13.5, lineHeight: tv ? 'normal' : 20.25, weight: 700, tracking: 0}, theme.textScale), {color: theme.colors.text}]}>{retry.label}</Text>
          {retry.detail != null && <Text style={[{includeFontPadding: false},nativeType({size: tv ? 24 : 13.5, lineHeight: tv ? 'normal' : 20.25, weight: 600, tracking: 0}, theme.textScale), {color: theme.colors.textTertiary, marginLeft: tv ? 16 : 0}]}>{retry.detail}</Text>}
        </View>}
        {alive}
        {beforeActions}
        <View style={{alignSelf: stacked ? 'stretch' : undefined, flexDirection: stacked ? 'column' : 'row', flexWrap: stacked ? 'nowrap' : 'wrap', alignItems: stacked ? 'stretch' : 'center', gap: tv ? 18 : player ? 8 : 10}}>
          {primaryAction && action(primaryAction, true)}
          {secondaryAction && action(secondaryAction, false)}
          {additionalActions?.map(item => action(item, false))}
          {detailsAction && <StateActionButton testID={testID} action={{...detailsAction, disabled: !visible || detailsAction.disabled, trailingIcon: detailsAction.trailingIcon ?? (tv || detailsPresentation === 'link' ? undefined : 'i-right')}} size={tv ? 'hero' : 'small'} variant={tv ? 'glass' : 'text'} iconSize={!tv ? 16 : undefined} iconColor={!tv ? theme.colors.textSecondary : undefined}
            style={!tv ? detailsPresentation === 'link' ? {height: 20, paddingHorizontal: 0, alignSelf: 'center'} : {height: player ? 40 : 44, paddingHorizontal: 6} : undefined}
            textStyle={!tv ? {fontSize: (player ? 14 : 14.5) * theme.textScale, color: theme.colors.textSecondary} : undefined} />}
        </View>
        {details}
        {help != null && <Text style={[{includeFontPadding: false},nativeType({size: tv ? 22 : 12.5, lineHeight: tv ? 33 : 18.75, weight: 400, tracking: 0}, theme.textScale), {maxWidth: tv ? 880 : undefined, color: theme.colors.textTertiary, textAlign: stacked ? 'center' : 'left', marginTop: tv ? 36 : 18, marginHorizontal: stacked ? 6 : 0}]}>{help}</Text>}
        {children}
      </View>
    </Animated.View>
    {playerChrome}
  </View>;
}
