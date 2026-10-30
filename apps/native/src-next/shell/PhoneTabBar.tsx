import React from 'react';
import {Text, View, useWindowDimensions} from 'react-native';
import {useSafeAreaInsets} from 'react-native-safe-area-context';
import {defaultI18n} from '../../../../packages/i18n/src';
import {BlurView, phoneTabBarRecipe} from '../platform/native-blur';
import {Focusable} from '../kit/controls';
import {Icon} from '../kit/Icon';
import {useTheme} from '../kit/theme';
import {landscapePhoneBarInset, landscapePhoneBarMetrics, phoneBarHeight, phoneTabs, selectedTab, type ShellDestination, type ShellTab} from './model';

/** phone-guide 2: landscape phones keep the same five tabs, drawn as the compact `.tabbar.cmp`
 * (icon and label in a row) instead of the portrait stacked bar. */
export function PhoneTabBar({destination, hasLiveTV, onSelect, landscape = false}: {destination: ShellDestination; hasLiveTV: boolean; onSelect: (tab: ShellTab['id']) => void; landscape?: boolean}) {
  const {colors, type, layers, textScale} = useTheme(), insets = useSafeAreaInsets(), window = useWindowDimensions();
  const recipe = phoneTabBarRecipe(), compact = landscape ? landscapePhoneBarMetrics : undefined, tabs = phoneTabs(hasLiveTV);
  const sideInset = compact ? landscapePhoneBarInset(window.width, tabs.length, insets.left ?? 0, insets.right ?? 0) : recipe.paddingX;
  return <View accessibilityLabel={defaultI18n.t('next.home.native.mainNavigation')} testID="phone-tab-bar"
    style={{position: 'absolute', left: 0, right: 0, bottom: 0, height: phoneBarHeight(insets.bottom, landscape), zIndex: layers.sticky, overflow: 'hidden', borderTopWidth: 1, borderTopColor: `${colors.text}14`}}>
    <BlurView pointerEvents="none" style={{position: 'absolute', inset: 0}} blurType="dark" blurAmount={recipe.blur} reducedTransparencyFallbackColor={colors.raised}/>
    <View pointerEvents="none" style={{position: 'absolute', inset: 0, backgroundColor: colors.raised, opacity: recipe.alpha}}/>
    <View style={{flexDirection: 'row', paddingHorizontal: sideInset, paddingTop: compact?.paddingTop ?? recipe.paddingTop}}>
      {tabs.map(tab => {
        const selected = selectedTab(destination) === tab.id, color = selected ? colors.text : colors.textTertiary;
        return <Focusable key={tab.id} testID={`phone-tab-${tab.id}`} accessibilityRole="tab" accessibilityLabel={defaultI18n.t(tab.message)} accessibilityState={{selected}} onPress={() => onSelect(tab.id)} focusRadius={10}
          hitSlop={compact ? {top: 6, bottom: 6, left: 0, right: 0} : undefined}
          style={compact
            ? {flex: 1, height: compact.content - compact.paddingTop, flexDirection: 'row', alignItems: 'center', justifyContent: 'center', gap: compact.gap}
            : {flex: 1, height: 44, alignItems: 'center', justifyContent: 'flex-start', gap: recipe.gap}}>
          <Icon name={tab.icon} size={compact?.iconSize ?? 22} color={color}/>
          <Text numberOfLines={1} maxFontSizeMultiplier={1.3} style={[type.tabBar, compact ? {flexShrink: 1, fontSize: compact.labelSize * textScale, lineHeight: compact.labelLineHeight * textScale} : null, {color}]}>{defaultI18n.t(tab.message)}</Text>
        </Focusable>;
      })}
    </View>
  </View>;
}
