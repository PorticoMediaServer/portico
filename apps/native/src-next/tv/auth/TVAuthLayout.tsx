import React, {useEffect, useMemo} from 'react';
import {tvAccountSpec} from '@portico/design';
import Animated, {useAnimatedStyle, useSharedValue, withTiming} from 'react-native-reanimated';
import {useMotion} from '../../kit/useMotion';
import {Image, ScrollView, StyleSheet, Text, View, useWindowDimensions, type ViewProps} from 'react-native';
import {defaultI18n} from '../../../../../packages/i18n/src';
import {ColourBackdrop, ProfileFace, type ProfileOption} from '../../kit/artwork';
import {nativeType, useTheme} from '../../kit/theme';
import {shellLayout} from '../../shell/model';
import {useNativeObservation} from '../../kit/dev-observation';
import {StateActionButton} from '../../kit/states/StateActionButton';
import type {StateLayoutProps} from '../../kit/states/StateLayout';

/** The account family's one design canvas, backdrop and brand position. */
export function TVAccountCanvas({children, testID, shape = 'side', onAccessibilityEscape}: Pick<ViewProps, 'children' | 'testID' | 'onAccessibilityEscape'> & {shape?: 'side' | 'round'}) {
  const theme = useTheme(), window = useWindowDimensions(), canvas = shellLayout(window.width, window.height, true);
  const rootStyle = {flex: 1, backgroundColor: theme.colors.ink};
  const wordmarkStyle = {position: 'absolute' as const, left: theme.layout.gutter.tvAuth, top: tvAccountSpec.wordmark.top,
    width: tvAccountSpec.wordmark.width, height: tvAccountSpec.wordmark.height};
  const root = useNativeObservation<View>({kind: 'view', semanticId: testID,
    flattenedStyle: typeof __DEV__ !== 'undefined' && __DEV__ ? StyleSheet.flatten(rootStyle) : undefined,
    declaredStyleComplete: typeof __DEV__ !== 'undefined' && __DEV__ ? true : undefined});
  const wordmark = useNativeObservation<Image>({kind: 'image', semanticId: testID ? `${testID}-wordmark` : undefined,
    flattenedStyle: typeof __DEV__ !== 'undefined' && __DEV__ ? StyleSheet.flatten(wordmarkStyle) : undefined,
    declaredStyleComplete: typeof __DEV__ !== 'undefined' && __DEV__ ? true : undefined});
  return <View ref={root.ref} testID={testID} onAccessibilityEscape={onAccessibilityEscape} style={rootStyle}>
    <View style={{position: 'absolute', left: canvas.left, top: canvas.top, width: canvas.width, height: canvas.height,
      transformOrigin: 'top left', transform: [{scale: canvas.scale}]}}>
      <ColourBackdrop shape={shape} fill/>
      <Image ref={wordmark.ref} source={require('../../../assets/wordmark.png')} accessibilityLabel={defaultI18n.t('accounts.brand')}
        resizeMode="contain" style={wordmarkStyle}/>
      {children}
    </View>
  </View>;
}

type TVAuthLayoutProps = Pick<StateLayoutProps, 'title' | 'body' | 'children' | 'testID' | 'beforeActions' | 'kicker' |
  'primaryAction' | 'secondaryAction' | 'additionalActions' | 'detailsAction' | 'avoidKeyboard'> & {
  eyebrow?: string; portico?: boolean; aside?: React.ReactNode;
};

/** Account fields keep the mock's width and one shared gap before the actions. */
export function TVAccountFields({children}: {children: React.ReactNode}) {
  return <View style={{alignSelf: 'stretch', width: tvAccountSpec.fieldsWidth, gap: tvAccountSpec.fieldGap, marginBottom: tvAccountSpec.beforeActionsGap}}>{children}</View>;
}

/** connect36 / auth8: every account form uses this column and type rhythm. */
export function TVAuthLayout({title, eyebrow, body, children, portico, aside, testID, beforeActions, kicker,
  primaryAction, secondaryAction, additionalActions, detailsAction}: TVAuthLayoutProps) {
  const theme = useTheme(), window = useWindowDimensions();
  const titleStyle = useMemo(() => [theme.type.accountTitle, {color: theme.colors.text, marginBottom: tvAccountSpec.titleGap}], [theme.type.accountTitle, theme.colors.text]);
  const revision = useMemo(() => [title, theme.textScale, theme.colors.text, window.width, window.height], [title, theme.textScale, theme.colors.text, window.width, window.height]);
  const heading = useNativeObservation<Text>({kind: 'text', semanticId: testID ? `${testID}-title` : undefined,
    flattenedStyle: typeof __DEV__ !== 'undefined' && __DEV__ ? StyleSheet.flatten(titleStyle) : undefined,
    declaredStyleComplete: typeof __DEV__ !== 'undefined' && __DEV__ ? true : undefined}, undefined, revision);
  const actions = [primaryAction, secondaryAction, ...(additionalActions ?? []), detailsAction].filter(item => item !== undefined);
  return <TVAccountCanvas testID={testID}>
    {/* The scroll view starts at the window edge and pads the column in, so a focused control's ring, scale and
        glow are never clipped at the column's own edge. */}
    <ScrollView style={{position: 'absolute', left: 0, top: 0, bottom: 0, width: theme.layout.gutter.tvAuth * 2 + tvAccountSpec.column}}
      contentContainerStyle={{flexGrow: 1, justifyContent: 'center', paddingHorizontal: theme.layout.gutter.tvAuth, paddingBottom: theme.layout.gutter.tvAuth,
        // A column taller than the window starts below the wordmark, never under it.
        paddingTop: tvAccountSpec.wordmark.top + tvAccountSpec.wordmark.height + tvAccountSpec.ledeGap}}>
      {kicker}
      {eyebrow && <Text style={[theme.type.accountEyebrow, {color: theme.colors.textTertiary, marginBottom: tvAccountSpec.eyebrowGap}]}>{eyebrow}</Text>}
      <Text {...heading.textProps} accessibilityRole="header" style={titleStyle}>{title}</Text>
      {body != null && <Text style={[theme.type.accountLede, {color: theme.colors.textSecondary, maxWidth: 760, marginBottom: tvAccountSpec.ledeGap}]}>{body}</Text>}
      {beforeActions}
      {actions.length > 0 && <View style={{flexDirection: 'row', flexWrap: 'wrap', alignItems: 'center', gap: tvAccountSpec.actionGap}}>
        {actions.map(item => <StateActionButton key={item.id} testID={testID} action={item} size="hero" variant={item === primaryAction ? 'primary' : 'glass'}/>)}
      </View>}
      {children}
    </ScrollView>
    {portico && <View style={{position: 'absolute', left: theme.layout.gutter.tvAuth, bottom: tvAccountSpec.footerBottom}}>
      <Text style={[theme.type.accountHint, {color: theme.colors.textTertiary}]}>{defaultI18n.t('activation.termsNote').split('\n')[0]}</Text>
      <View style={{flexDirection: 'row', gap: 28}}>{(['accounts.legal.termsAddress', 'accounts.legal.privacyAddress'] as const).map(key => <Text key={key}
        style={[theme.type.accountHint, {color: theme.colors.textTertiary}]}>{defaultI18n.t(key)}</Text>)}</View>
    </View>}
    {aside}
  </TVAccountCanvas>;
}

/** Auth17's profile identity column; the account owner supplies every visible profile fact. */
export function TVProfilePINLayout({profile, children, pad, hints, onBack}: {
  profile: ProfileOption; children: React.ReactNode; pad: React.ReactNode; hints?: React.ReactNode; onBack: () => void;
}) {
  const theme = useTheme();
  return <TVAccountCanvas testID="tv-profile-pin" shape="round" onAccessibilityEscape={onBack}>
      <View style={{position: 'absolute', left: 240, top: 0, bottom: 0, width: 640, alignItems: 'center', justifyContent: 'center'}}>
        <ProfileFace {...profile} size={196} scaleOnFocus={false}/>
        <Text accessibilityRole="header" style={[nativeType({size: 52, weight: 800, tracking: -.04, lineHeight: 'normal'}, theme.textScale),
          {color: theme.colors.text, marginTop: 30, marginBottom: 6}]}>{defaultI18n.t('auth.enterProfilePin')}</Text>
        <Text style={[nativeType({size: 28, weight: 400, tracking: 0, lineHeight: 'normal'}, theme.textScale), {color: theme.colors.textSecondary, marginBottom: 40}]}>{profile.name}</Text>
        {children}
      </View>
      {pad}
      {hints && <View style={{position: 'absolute', left: 0, right: 0, bottom: 72, flexDirection: 'row', justifyContent: 'center', gap: 44}}>{hints}</View>}
  </TVAccountCanvas>;
}

/** notes/motion.md S.3: the line under the boxes fades in (standard). Its room is kept whether or not it shows, so the
 * face, heading and boxes never move when a wrong PIN is said. */
export function TVCodeErrorLine({error}: {error?: React.ReactNode}) {
  const motion = useMotion(), opacity = useSharedValue(0), shown = !!error;
  useEffect(() => {opacity.value = shown ? withTiming(1, motion.timing('standard')) : 0;}, [motion, opacity, shown]);
  const style = useAnimatedStyle(() => ({opacity: opacity.value}));
  return <Animated.View accessibilityRole={shown ? 'alert' : undefined} style={[{minHeight: 34, marginTop: 16, alignItems: 'center'}, style]}>{error}</Animated.View>;
}
