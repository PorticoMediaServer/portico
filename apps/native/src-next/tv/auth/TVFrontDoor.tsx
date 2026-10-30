import React, {useCallback, useLayoutEffect, useMemo, useRef} from 'react';
import {Image, Text, View, StyleSheet, findNodeHandle, useWindowDimensions, type ImageStyle, type TextProps, type TextStyle} from 'react-native';
import {defaultI18n} from '../../../../../packages/i18n/src';
import {ColourBackdrop} from '../../kit/artwork';
import {Button} from '../../kit/controls';
import {FocusProvider, FocusRegion} from '../../kit/focus';
import {nativeType, useTheme} from '../../kit/theme';
import {useDestinationEntryFocus} from '../../screens/destinations/useEntryFocus';
import {shellLayout} from '../../shell/model';
import type {HostRegistration} from '../../dev/layout-dump/index';
import {useNativeObservation} from '../../kit/dev-observation';
import {useTVBusyEntryRevision} from './entry-revision';

/** Only this presenter's public heading is observed; supplied error content and
 * Button internals keep their own owners. No resolved paint/font is inferred. */
function useHeadingMeasurement(text: string, style: TextStyle[], width: number, height: number) {
  const registration = useRef<HostRegistration | undefined>(undefined), native = useRef<Text | null>(null);
  const input = useRef({style});
  const ref = useCallback((host: Text | null) => {
    registration.current?.unregister(); registration.current = undefined; native.current = host;
    if (typeof __DEV__ !== 'undefined' && __DEV__ && host) {
      const {registerNativeHost} = require('../../dev/layout-dump/index') as typeof import('../../dev/layout-dump/index');
      registration.current = registerNativeHost(host, {kind: 'text', semanticId: 'tv-front-door-title', flattenedStyle: StyleSheet.flatten(input.current.style), declaredStyleComplete: true});
    }
  }, []);
  useLayoutEffect(() => {
    input.current = {style};
    if (!registration.current) return;
    registration.current.update({kind: 'text', semanticId: 'tv-front-door-title', flattenedStyle: StyleSheet.flatten(style), declaredStyleComplete: true});
    registration.current.invalidateTextLayout(); registration.current.invalidateResolvedStyle();
  }, [text, style, width, height]);
  useLayoutEffect(() => () => {registration.current?.unregister(); registration.current = undefined; native.current = null;}, []);
  const onTextLayout = useCallback<NonNullable<TextProps['onTextLayout']>>(event => {
    if (native.current && findNodeHandle(native.current) === event.nativeEvent.target) registration.current?.recordTextLayout(event.nativeEvent.lines);
  }, []);
  // Do not clear a just-delivered text event from a later generic onLayout.
  // Committed text/font/viewport changes above invalidate before fresh events.
  return typeof __DEV__ !== 'undefined' && __DEV__ ? {ref, onTextLayout} : {};
}

export type TVFrontDoorProps = Readonly<{
  busy: boolean; error?: React.ReactNode;
  onPortico: () => void; onLocal: () => void;
}>;

/** connect40's signed-out screen. The account owner alone starts either flow;
 * rendering this screen makes no Services request and installs no Back action. */
export function TVFrontDoor(props: TVFrontDoorProps) {
  return <FocusProvider><FrontDoor {...props}/></FocusProvider>;
}
function FrontDoor({busy, error, onPortico, onLocal}: TVFrontDoorProps) {
  const theme = useTheme(), window = useWindowDimensions(), canvas = shellLayout(window.width, window.height, true), id = 'tv.auth.front-door';
  const entryRevision = useTVBusyEntryRevision(busy);
  useDestinationEntryFocus({visible: !busy, intent: 'enter', revision: entryRevision}, {regionId: id, childId: 'portico'});
  const textStyle = nativeType({size: 26, weight: 700, lineHeight: 'normal', tracking: 0}, theme.textScale);
  const title = defaultI18n.t('web.signin.welcomeHome');
  const titleStyle = useMemo(() => [nativeType({size: 88, weight: 800, lineHeight: 88, tracking: -.045}, theme.textScale),
    // connect40's `.tvl h1`: text-shadow 0 2px 30px rgba(7,16,24,.5), the ink at 50%.
    {color: theme.colors.text, marginBottom: 56,
      textShadowColor: 'rgba(7, 16, 24, 0.5)', textShadowOffset: {width: 0, height: 2}, textShadowRadius: 30}],
  [theme.textScale, theme.colors.text]);
  const measurement = useHeadingMeasurement(title, titleStyle, window.width, window.height);
  const rootStyle = {flex: 1, backgroundColor: theme.colors.ink};
  const wordmarkStyle: ImageStyle = {position: 'absolute', left: 0, top: 72, width: 174, height: 40};
  const rootObservation = useNativeObservation<View>({kind: 'view', semanticId: 'tv-front-door', flattenedStyle: typeof __DEV__ !== 'undefined' && __DEV__ ? StyleSheet.flatten(rootStyle) : undefined, declaredStyleComplete: typeof __DEV__ !== 'undefined' && __DEV__ ? true : undefined});
  const wordmarkObservation = useNativeObservation<Image>({kind: 'image', semanticId: 'tv-front-door-wordmark', flattenedStyle: typeof __DEV__ !== 'undefined' && __DEV__ ? StyleSheet.flatten(wordmarkStyle) : undefined, declaredStyleComplete: typeof __DEV__ !== 'undefined' && __DEV__ ? true : undefined});
  return <View ref={rootObservation.ref} testID="tv-front-door" style={rootStyle}>
    <View style={{position: 'absolute', left: canvas.left, top: canvas.top, width: canvas.width, height: canvas.height,
      transformOrigin: 'top left', transform: [{scale: canvas.scale}]}}>
      <ColourBackdrop shape="side" fill/>
      <View style={{position: 'absolute', left: 200, top: 0, bottom: 0, width: 900, justifyContent: 'center'}}>
        <Image ref={wordmarkObservation.ref} source={require('../../../assets/wordmark.png')} accessibilityLabel={defaultI18n.t('accounts.brand')}
          resizeMode="contain" style={wordmarkStyle}/>
        <Text {...measurement} accessibilityRole="header" style={titleStyle}>{title}</Text>
        {error && <View accessibilityRole="alert" style={{marginBottom: 16}}>{error}</View>}
        <FocusRegion id={id} entry="portico" order={['portico', 'local']} orientation="horizontal" enabled={!busy} style={{flexDirection: 'row', gap: 36}}>
          <Button focusId="portico" testID="tv-front-door-portico" disabled={busy} onPress={() => {if (!busy) onPortico();}}
            style={{height: 72, paddingHorizontal: 38, borderRadius: 14}} textStyle={textStyle}>{defaultI18n.t('accounts.signIn.portico')}</Button>
          <Button focusId="local" testID="tv-front-door-local" variant="glass" disabled={busy} onPress={() => {if (!busy) onLocal();}}
            style={{height: 72, paddingHorizontal: 38, borderRadius: 14}} textStyle={textStyle}>{defaultI18n.t('accounts.signIn.local')}</Button>
        </FocusRegion>
      </View>
    </View>
  </View>;
}
