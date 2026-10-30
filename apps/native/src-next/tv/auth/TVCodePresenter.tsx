import React, {useEffect, useState} from 'react';
import {Text, View} from 'react-native';
import QRCode from 'react-native-qrcode-svg';
import {defaultI18n} from '../../../../../packages/i18n/src';
import {Button} from '../../kit/controls';
import {FocusProvider, FocusRegion} from '../../kit/focus';
import {nativeType, useTheme} from '../../kit/theme';
import {useDestinationEntryFocus} from '../../screens/destinations/useEntryFocus';
import {BlurView, glassRecipe} from '../../platform/native-blur';
import {useTVBusyEntryRevision} from './entry-revision';
import {TVAuthLayout} from './TVAuthLayout';

export type TVCodePresenterProps = Readonly<{
  body: React.ReactNode;
  verificationUri?: string; verificationUriComplete?: string; userCode?: string;
  status?: React.ReactNode; busy: boolean; onTypeCredentials: () => void; onBack: () => void;
  /** An explicit terminal retry, already bound to the rendered original opening. */
  onNewCode?: () => void;
  onRetry?: () => void; retryAt?: number; credentialsDisabled?: boolean;
}> & Readonly<{mode: 'portico'; serverName?: string} | {mode: 'local'; serverName: string}>;

/** Auth8/9 route buttons call the real transaction owner; this view never contacts either authority. */
export function TVCodePresenter(props: TVCodePresenterProps) {
  return <FocusProvider><CodePresenter {...props}/></FocusProvider>;
}
function CodePresenter(props: TVCodePresenterProps) {
  const theme = useTheme(), id = `tv.auth.${props.mode}.code`, t = defaultI18n.t, material = glassRecipe();
  const address = (() => {
    if (!props.verificationUri) return undefined;
    try {const url = new URL(props.verificationUri); return `${url.host}${url.pathname}`;} catch {return props.verificationUri;}
  })();
  const entryRevision = useTVBusyEntryRevision(props.busy);
  const [, updateDeadline] = useState(0);
  useEffect(() => {
    const remaining = (props.retryAt ?? 0) - Date.now();
    if (!props.onRetry || remaining <= 0) return;
    const timer = setTimeout(() => updateDeadline(value => value + 1), remaining);
    return () => clearTimeout(timer);
  }, [props.onRetry, props.retryAt]);
  const retryDisabled = props.busy || (props.retryAt ?? 0) > Date.now();
  const entry = props.onRetry ? 'retry-approval' : props.onNewCode ? 'new-code' : 'credentials';
  const order = ['credentials', ...(props.onRetry ? ['retry-approval'] : []), ...(props.onNewCode ? ['new-code'] : []), ...(props.mode === 'portico' ? ['back'] : [])];
  useDestinationEntryFocus({visible: !props.busy, intent: 'enter', revision: entryRevision * 3 + (props.onRetry ? 2 : props.onNewCode ? 1 : 0)}, {regionId: id, childId: entry});
  const type = (size: number, weight: 400 | 600 | 700 | 800 = 400, lineHeight = 1.45) => nativeType({size, weight, tracking: 0, lineHeight: size * lineHeight}, theme.textScale);
  const aside = <View style={{position: 'absolute', right: 140, top: 0, bottom: 0, width: 720, justifyContent: 'center'}}>
    <View style={{paddingTop: 52, paddingHorizontal: 56, paddingBottom: 48, borderRadius: 28, backgroundColor: '#0d1b25C7',
      borderWidth: 1.5, borderColor: `${theme.colors.text}1A`, alignItems: 'center',
      // auth8/9 .codecard shadow 0 60px 120px -40px rgba(0,0,0,.9).
      boxShadow: [{offsetX: 0, offsetY: 60, blurRadius: 120, spreadDistance: -40, color: 'rgba(0, 0, 0, 0.9)'}]}}>
      {material.nativeBlur && <View pointerEvents="none" style={{position: 'absolute', inset: 0, borderRadius: 28, overflow: 'hidden'}}>
        <BlurView style={{position: 'absolute', inset: 0}} blurType="dark" blurAmount={30} reducedTransparencyFallbackColor="#0d1b25"/>
      </View>}
      {props.verificationUriComplete && <View style={{width: 300, height: 300, padding: 20, marginBottom: 34, borderRadius: 20, backgroundColor: theme.colors.white}}>
        <QRCode value={props.verificationUriComplete} size={260}/>
      </View>}
      <Text selectable style={[type(30, 700), {color: theme.colors.textSecondary}]}>{address}</Text>
      {(props.userCode || !props.onNewCode) && <Text selectable style={[type(90, 800, 1.05), {color: theme.colors.text, letterSpacing: 3.6, fontVariant: ['tabular-nums'], marginTop: 10, marginBottom: 16}]}>{props.userCode ? props.userCode.split('-').flatMap((part, index) => index ? [<Text key={index} style={{color: theme.colors.textTertiary}}>-</Text>, part] : [part]) : t('activation.preparingCode')}</Text>}
      {!props.onNewCode && <Text style={[type(21), {color: theme.colors.textTertiary, textAlign: 'center'}]}>{t('activation.codeRefreshes')}</Text>}
    </View>
  </View>;
  return <TVAuthLayout testID={`tv-${props.mode}-code`} portico={props.mode === 'portico'} aside={aside}
    heading={props.mode === 'portico' ? t('activation.title') : props.serverName}
    eyebrow={props.mode === 'local' ? t('accounts.signIn.to') : undefined} body={props.body}>
    {props.status}
    <FocusRegion id={id} entry={entry} order={order} enabled={!props.busy} style={{flexDirection: 'row', gap: 20}}>
      <Button focusId="credentials" disabled={props.busy || props.credentialsDisabled} onPress={props.onTypeCredentials} style={{height: 72, paddingHorizontal: 38, borderRadius: 14}}>{t(props.mode === 'portico' ? 'accounts.signIn.onTV' : 'quickConnect.usePassword')}</Button>
      {props.onRetry && <Button focusId="retry-approval" focusableWhenDisabled disabled={retryDisabled} onPress={props.onRetry} style={{height: 72, paddingHorizontal: 38, borderRadius: 14}}>{t('action.tryAgain')}</Button>}
      {props.onNewCode && <Button focusId="new-code" disabled={props.busy} onPress={props.onNewCode} style={{height: 72, paddingHorizontal: 38, borderRadius: 14}}>{t('activation.getNewCode')}</Button>}
      {props.mode === 'portico' && <Button focusId="back" variant="glass" disabled={props.busy} onPress={props.onBack} style={{height: 72, paddingHorizontal: 38, borderRadius: 14}}>{t('action.back')}</Button>}
    </FocusRegion>
  </TVAuthLayout>;
}
