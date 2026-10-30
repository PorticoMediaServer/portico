import React, {useEffect} from 'react';
import {Text, View} from 'react-native';
import {FormScrollView} from '../../platform/keyboard';
import Animated, {useAnimatedStyle, useSharedValue, withTiming} from 'react-native-reanimated';
import {useSafeAreaInsets} from 'react-native-safe-area-context';
import {ColourBackdrop} from '../../kit/artwork';
import {nativeType, useTheme} from '../../kit/theme';
import {useMotion} from '../../kit/useMotion';
import type {StateAction} from '../../kit/states/types';
import type {StateLayoutProps} from '../../kit/states/StateLayout';
import {StateActionButton} from '../../kit/states/StateActionButton';

/** The compact auth form in auth.html/account.html: top Back, then a left-aligned
 * form. A scrollable keyboard-avoiding body keeps the same editor mounted. */
export function PhoneAccountForm({title, body, beforeActions, primaryAction, secondaryAction, additionalActions,
  detailsAction, details, help, kicker, children, visible = true, style, testID}: StateLayoutProps) {
  const theme = useTheme(), insets = useSafeAreaInsets(), motion = useMotion();
  const opacity = useSharedValue(visible ? 1 : 0);
  useEffect(() => {opacity.value = withTiming(visible ? 1 : 0, motion.timing(visible ? 'standard' : 'quick'));}, [visible, opacity, motion]);
  const animated = useAnimatedStyle(() => ({opacity: opacity.value}));
  const back = [secondaryAction, detailsAction, primaryAction].find(value => value?.id === 'back');
  const action = (value: StateAction, primary: boolean) => <StateActionButton key={value.id} testID={testID}
    action={visible ? value : {...value, disabled: true}} variant={primary ? 'primary' : 'glass'}
    style={{alignSelf: 'stretch', width: '100%', minHeight: 48, height: undefined, paddingVertical: 12, paddingHorizontal: 20, borderRadius: 11}}
    textStyle={{fontSize: 16 * theme.textScale}}/>;
  return <View testID={testID} style={[{flex: 1, backgroundColor: theme.colors.ink}, style]}>
    <ColourBackdrop shape="top"/>
    <Animated.View pointerEvents={visible ? 'auto' : 'none'} accessibilityElementsHidden={!visible}
      importantForAccessibility={visible ? 'auto' : 'no-hide-descendants'} style={[{flex: 1}, animated]}>
        <FormScrollView bottomOffset={80} keyboardShouldPersistTaps="handled" keyboardDismissMode="on-drag"
          contentContainerStyle={{flexGrow: 1, paddingHorizontal: 22, paddingTop: Math.max(50, insets.top), paddingBottom: Math.max(42, insets.bottom + 16)}}>
          <View style={{width: '100%', maxWidth: 520, alignSelf: 'center', flexGrow: 1}}>
            {back ? <StateActionButton testID={testID} action={visible ? {...back, icon: 'i-left'} : {...back, icon: 'i-left', disabled: true}}
              variant="text" size="small" iconSize={22} iconColor={theme.colors.textSecondary}
              style={{alignSelf: 'flex-start', minHeight: 48, height: undefined, paddingVertical: 8, paddingHorizontal: 0, marginLeft: -10, marginBottom: 6}}
              textStyle={{fontSize: 16 * theme.textScale, color: theme.colors.textSecondary}}/> : <View style={{height: 54}}/>}
            {kicker != null && <View style={{marginBottom: 6}}>{kicker}</View>}
            <Text accessibilityRole="header" textBreakStrategy="balanced" android_hyphenationFrequency="none"
              style={[nativeType({size: 34, lineHeight: 35.36, weight: 800, tracking: -.04}, theme.textScale),
                {includeFontPadding: false, color: theme.colors.text, textAlign: 'left', marginBottom: 8}]}>{title}</Text>
            {body != null && <Text style={[nativeType({size: 14.5, lineHeight: 21.75, weight: 400, tracking: 0}, theme.textScale),
              {color: theme.colors.textSecondary, textAlign: 'left', marginBottom: 20}]}>{body}</Text>}
            {beforeActions}
            <View style={{alignSelf: 'stretch', gap: 10}}>
              {primaryAction && primaryAction !== back && action(primaryAction, true)}
              {secondaryAction && secondaryAction !== back && action(secondaryAction, false)}
              {additionalActions?.map(value => action(value, false))}
              {detailsAction && detailsAction !== back && <StateActionButton testID={testID} action={visible ? detailsAction : {...detailsAction, disabled: true}}
                variant="text" size="small" style={{minHeight: 48, height: undefined, alignSelf: 'center'}}/>}
            </View>
            {details}
            {help != null && <Text style={[theme.type.caption, {color: theme.colors.textTertiary, marginTop: 14}]}>{help}</Text>}
            {children}
          </View>
        </FormScrollView>
    </Animated.View>
  </View>;
}
