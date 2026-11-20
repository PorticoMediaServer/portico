import React, {useEffect} from 'react';
import {Text, View} from 'react-native';
import Animated, {useAnimatedStyle, useSharedValue, withTiming} from 'react-native-reanimated';
import {useTheme} from '../theme';
import {useMotion} from '../useMotion';
import type {StateAction} from '../states/types';
import type {StateLayoutProps} from '../states/StateLayout';
import {StateActionButton} from '../states/StateActionButton';
import {SignInFrame} from '../frames';

export function AccountForm({title, body, kicker, beforeActions, primaryAction, secondaryAction, additionalActions,
  detailsAction, details, help, children, visible = true, style, testID}: StateLayoutProps) {
  const theme = useTheme(), motion = useMotion();
  const opacity = useSharedValue(visible ? 1 : 0);
  useEffect(() => {opacity.value = withTiming(visible ? 1 : 0, motion.timing(visible ? 'standard' : 'quick'));}, [visible, opacity, motion]);
  const animated = useAnimatedStyle(() => ({opacity: opacity.value}));
  const back = [secondaryAction, detailsAction, primaryAction].find(action => action?.id === 'back');
  const action = (value: StateAction, primary: boolean) => <StateActionButton key={value.id} testID={testID}
    action={visible ? value : {...value, disabled: true}} align="stretch" variant={primary ? 'primary' : 'glass'}/>;
  return <View style={[{flex: 1}, style]}>
    <Animated.View pointerEvents={visible ? 'auto' : 'none'} accessibilityElementsHidden={!visible}
      importantForAccessibility={visible ? 'auto' : 'no-hide-descendants'} style={[{flex: 1}, animated]}>
      <SignInFrame title={title} lede={body} kicker={kicker} testID={testID}
        back={back && typeof back.label === 'string' ? {label: back.label, onPress: back.onPress as () => void, disabled: !visible || back.disabled,
          testID: back.testID ?? (testID ? `${testID}-back` : undefined)} : undefined}>
        {back && typeof back.label !== 'string' && <StateActionButton testID={testID}
          action={{...back, icon: 'i-left', disabled: !visible || back.disabled}} variant="text" size="small"/>}
        {beforeActions}
        {primaryAction && primaryAction !== back && action(primaryAction, true)}
        {secondaryAction && secondaryAction !== back && action(secondaryAction, false)}
        {additionalActions?.map(value => action(value, false))}
        {detailsAction && detailsAction !== back && <StateActionButton testID={testID} action={visible ? detailsAction : {...detailsAction, disabled: true}}
          variant="text" size="small" align="center"/>}
        {details}
        {help != null && <Text style={[theme.type.caption, {color: theme.colors.textTertiary}]}>{help}</Text>}
        {children}
      </SignInFrame>
    </Animated.View>
  </View>;
}
