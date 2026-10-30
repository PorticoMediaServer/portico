import React, {useLayoutEffect, useMemo, useRef} from 'react';
import {Image, ScrollView, StyleSheet, Text, View, type LayoutChangeEvent, type NativeSyntheticEvent, type TextLayoutEventData} from 'react-native';
import {useSafeAreaInsets} from 'react-native-safe-area-context';
import {defaultI18n} from '../../../../../packages/i18n/src';
import {ColourBackdrop} from '../../kit/artwork';
import {Button} from '../../kit/controls';
import {nativeType, useTheme} from '../../kit/theme';
import {useNativeObservation} from '../../kit/dev-observation';
import type {HostRegistration} from '../../dev/layout-dump/index';

const registerNativeHost: typeof import('../../dev/layout-dump/index').registerNativeHost | undefined =
  __DEV__ ? require('../../dev/layout-dump/index').registerNativeHost : undefined;

/** Connect 39a. The account owner supplies the two genuine entry commands. */
export function PhoneFrontDoor({busy, error, onPortico, onLocal, onInvitation}: {
  busy: boolean; error?: React.ReactNode; onPortico: () => void; onLocal: () => void; onInvitation?: () => void;
}) {
  const theme = useTheme(), insets = useSafeAreaInsets(), t = defaultI18n.t;
  const welcomeStyle = {flex: 1, backgroundColor: theme.colors.ink};
  const wordmarkStyle = {width: 17 * 512 / 117, height: 17, alignSelf: 'flex-start' as const};
  const welcomeObservation = useNativeObservation<View>({kind: 'view', semanticId: 'accounts-welcome', flattenedStyle: __DEV__ ? StyleSheet.flatten(welcomeStyle) : undefined, declaredStyleComplete: true});
  const wordmarkObservation = useNativeObservation<Image>({kind: 'image', semanticId: 'accounts-welcome-wordmark', flattenedStyle: __DEV__ ? StyleSheet.flatten(wordmarkStyle) : undefined, declaredStyleComplete: true});
  const title = t('web.signin.welcomeHome'), committedTitle = useRef(title);
  useLayoutEffect(() => {committedTitle.current = title;}, [title]);
  const titleStyle = useMemo(() => [nativeType({size: 42, lineHeight: 43.68, weight: 800, tracking: -.04}, theme.textScale),
    {includeFontPadding: false, color: theme.colors.text, marginBottom: 22}], [theme.textScale, theme.colors.text]);
  const titleMeasurement = useMemo(() => {
    if (!registerNativeHost) return;
    let lease: HostRegistration | undefined, current: React.ElementRef<typeof Text> | null = null, previousLayout: string | undefined;
    const facts = {kind: 'text' as const, semanticId: 'accounts-welcome-title', flattenedStyle: StyleSheet.flatten(titleStyle), declaredStyleComplete: true};
    const unregister = () => {lease?.unregister(); lease = undefined; current = null; previousLayout = undefined;};
    return {
      ref: (host: React.ElementRef<typeof Text> | null) => {
        if (host === current) return;
        unregister();
        if (host) {current = host; lease = registerNativeHost(host, facts);}
      },
      onTextLayout: (event: NativeSyntheticEvent<TextLayoutEventData>) => {if (committedTitle.current === title) lease?.recordTextLayout(event.nativeEvent.lines);},
      onLayout: (event: LayoutChangeEvent) => {
        const {x, y, width, height} = event.nativeEvent.layout, next = [x, y, width, height].join(':');
        if (previousLayout !== undefined && next !== previousLayout) {lease?.invalidateTextLayout(); lease?.invalidateResolvedStyle();}
        previousLayout = next;
      },
      unregister,
    };
  }, [title, titleStyle]);
  useLayoutEffect(() => () => {titleMeasurement?.unregister();}, [titleMeasurement]);
  return <View ref={welcomeObservation.ref} testID="accounts-welcome" style={welcomeStyle}>
    <ColourBackdrop shape="top"/>
    <ScrollView style={{flex: 1}} contentContainerStyle={{flexGrow: 1, paddingHorizontal: 22, paddingTop: Math.max(70, insets.top)}}>
      <Image ref={wordmarkObservation.ref} source={require('../../../assets/wordmark.png')} accessibilityLabel={t('accounts.brand')} resizeMode="contain"
        style={wordmarkStyle}/>
      <View style={{marginTop: 'auto', paddingTop: 32, paddingBottom: Math.max(66, insets.bottom + 32), gap: 10}}>
        <Text accessibilityRole="header" ref={titleMeasurement?.ref} onLayout={titleMeasurement?.onLayout} onTextLayout={titleMeasurement?.onTextLayout}
          style={titleStyle}>{title}</Text>
        {error}
        <Button testID="accounts-welcome-portico" disabled={busy} loading={busy || undefined} onPress={onPortico}
          style={{alignSelf: 'stretch', height: 48, borderRadius: 11, paddingHorizontal: 20}} textStyle={{fontSize: 16 * theme.textScale}}>{t('accounts.signIn.portico')}</Button>
        <Button testID="accounts-welcome-local" variant="glass" disabled={busy} onPress={onLocal}
          style={{alignSelf: 'stretch', height: 48, borderRadius: 11, paddingHorizontal: 20}} textStyle={{fontSize: 16 * theme.textScale}}>{t('accounts.signIn.local')}</Button>
        {onInvitation && <Button testID="accounts-welcome-invitation" variant="text" disabled={busy} onPress={onInvitation}>{t('services.start.join')}</Button>}
      </View>
    </ScrollView>
  </View>;
}
