import React, {useRef, useState} from 'react';
import {Image, Text, View, useWindowDimensions} from 'react-native';
import Svg, {Circle, Path} from 'react-native-svg';
import {defaultI18n} from '../../../../../packages/i18n/src';
import {ColourBackdrop} from '../../kit/artwork';
import {Button} from '../../kit/controls';
import {Icon} from '../../kit/Icon';
import {Dialog, OverlaysProvider} from '../../kit/overlays';
import {nativeType, useTheme} from '../../kit/theme';
import type {NativeIdentityFailure} from '../../platform/identity-failure';
import {shellLayout} from '../../shell/model';

const t = defaultI18n.t;

/** Final69 frames1/3/4. This page receives a captured public name/family only;
 * it cannot construct an API or send a token to the address that answered. */
export function ServerIdentityFailure({failure, onRetry, onChoose, onForget, onError}: {
  failure: NativeIdentityFailure; onRetry: () => void; onChoose: () => void;
  onForget: () => Promise<void>; onError: (error: unknown) => void;
}) {
  const theme = useTheme(), dimensions = useWindowDimensions();
  const layout = shellLayout(dimensions.width, dimensions.height, theme.isTV);
  const [confirm, setConfirm] = useState(false), [busy, setBusy] = useState(false);
  const cancel = useRef<View>(null), opener = useRef<View>(null);
  const name = failure.server.name, tv = theme.isTV;
  const color = failure.changed ? theme.colors.record : theme.colors.amber;
  const forget = async () => {
    if (busy) return; setBusy(true);
    try {await onForget(); onChoose();} catch (error) {onError(error);} finally {setBusy(false);}
  };
  const page = <View style={{flex: 1, backgroundColor: theme.colors.ink}}>
    <View pointerEvents="none" style={{position: 'absolute', inset: 0}}><ColourBackdrop shape={tv ? 'side' : 'top'} fill={tv}/></View>
    {tv ? <View pointerEvents="none" accessible={false} style={{position: 'absolute', left: 72, top: 58, bottom: 56, width: 64}}>
      <View style={{width: 28, height: 40, marginLeft: 16, overflow: 'hidden'}}><Image source={require('../../../assets/wordmark.png')} style={{height: 40, width: 173}} resizeMode="contain"/></View>
      <View style={{marginTop: 64, height: 64, alignItems: 'center', justifyContent: 'center'}}><View style={{position: 'absolute', left: -16, top: 18, bottom: 18, width: 4, borderRadius: 2, backgroundColor: theme.colors.accent}}/><Icon name="i-home" size={30}/></View>
      <View style={{marginTop: 'auto', height: 64, alignItems: 'center', justifyContent: 'center'}}><Icon name="i-settings" size={30} color={theme.colors.textTertiary}/></View>
    </View> : <Image accessible accessibilityLabel={t('next.platform.appName')} source={require('../../../assets/wordmark.png')} resizeMode="contain" style={{position: 'absolute', left: 24, top: 64, width: 74, height: 17}}/>}
    <View style={{position: 'absolute', inset: 0, justifyContent: 'center', paddingHorizontal: tv ? 200 : 30}}>
      <View style={{width: tv ? 1340 : undefined, alignItems: tv ? 'flex-start' : 'center'}}>
        <View style={{width: tv ? 88 : 76, height: tv ? 88 : 76, borderRadius: tv ? 44 : 38, alignItems: 'center', justifyContent: 'center',
          marginBottom: tv ? 34 : 22, backgroundColor: tv ? `${theme.colors.text}1A` : `${color}1F`,
          ...(tv ? {borderWidth: 1, borderColor: `${theme.colors.text}1F`} : undefined)}}>{failure.changed ? <Icon name="i-shieldx" size={tv ? 40 : 34} color={color}/> :
            <Svg width={tv ? 40 : 34} height={tv ? 40 : 34} viewBox="0 0 24 24" fill="none" stroke={color} strokeWidth={2} strokeLinecap="round" strokeLinejoin="round"><Circle cx={12} cy={12} r={9}/><Path d="M9.6 9.4a2.6 2.6 0 1 1 3.6 2.4c-.8.4-1.2.9-1.2 1.8M12 16.8v.1"/></Svg>}</View>
        <Text accessibilityRole="header" style={[nativeType({size: tv ? 80 : 25, lineHeight: tv ? 80 : 28, weight: 800, tracking: tv ? -.045 : -.03}, theme.textScale),
          {color: theme.colors.text, textAlign: tv ? 'left' : 'center', marginBottom: tv ? 24 : 10,
            ...(tv ? {textShadowColor: 'rgba(7, 16, 24, 0.5)', textShadowOffset: {width: 0, height: 2}, textShadowRadius: 30} : undefined)}]}>{t('portico.failure.identity.title', {server: name})}</Text>
        <Text style={[nativeType({size: tv ? 28 : 15, lineHeight: tv ? 40.6 : 22.5, weight: 400, tracking: 0}, theme.textScale),
          {color: theme.colors.textSecondary, maxWidth: tv ? 900 : undefined, textAlign: tv ? 'left' : 'center', marginBottom: tv ? 40 : 24}]}>{t(failure.changed ? 'connection.identity.changedBody' : 'portico.failure.identity.body')}</Text>
        <View style={{alignSelf: 'stretch', flexDirection: tv ? 'row' : 'column', gap: tv ? 28 : 10}}>
          <Button disabled={busy} icon="i-retry" iconSize={tv ? 28 : undefined} hasTVPreferredFocus onPress={onRetry} style={tv ? undefined : {height: 48, borderRadius: 11}}>{t('action.tryAgain')}</Button>
          <Button disabled={busy} variant="glass" icon={tv ? 'i-server' : undefined} iconSize={tv ? 28 : undefined} onPress={onChoose} style={tv ? undefined : {height: 48, borderRadius: 11}}>{t('portico.landing.chooseAnother')}</Button>
          {failure.changed && <Button ref={opener} disabled={busy} variant={tv ? 'glass' : 'text'} onPress={() => setConfirm(true)} style={tv ? undefined : {height: 36, marginTop: 4}}>{t('connection.identity.forget', {server: name})}</Button>}
        </View>
      </View>
    </View>
    <Dialog open={confirm} onOpenChange={open => {if (!busy) setConfirm(open);}} showClose={false} title={t('connection.identity.forgetTitle', {server: name})}
      description={t('connection.identity.forgetBody')} initialFocusRef={cancel} returnFocusRef={opener} testID="identity-forget"
      width={tv ? 1000 : undefined}
      faceColor={tv ? '#10212d' : undefined} scrimColor={tv ? 'rgba(3, 8, 12, 0.66)' : undefined} footerMarginTop={tv ? 34 : undefined}
      style={tv ? {borderRadius: 30, paddingTop: 56, paddingHorizontal: 60, paddingBottom: 52, borderWidth: 1.5, borderColor: `${theme.colors.text}1F`,
        // Frame 4's .tdlg shadow 0 80px 160px -40px #000; RN boxShadow carries the negative spread.
        boxShadow: [{offsetX: 0, offsetY: 80, blurRadius: 160, spreadDistance: -40, color: '#000'}]} : undefined}
      titleType={tv ? {size: 48, weight: 800, tracking: -.035, lineHeight: 'normal'} : undefined}
      descriptionTextStyle={tv ? {fontSize: 28 * theme.textScale, lineHeight: 40.6 * theme.textScale, marginTop: 16} : undefined}
      footer={<View style={{flexDirection: tv ? 'row' : 'column', gap: tv ? 18 : 10}}>
        <Button focusId="forget" variant="text" loading={busy} disabled={busy}
          style={tv ? {backgroundColor: `${theme.colors.danger}29`} : {backgroundColor: theme.colors.record}}
          textStyle={tv ? {color: theme.colors.danger} : {color: '#2a0808'}} onPress={() => {void forget();}}>{t('connection.identity.forgetAction')}</Button>
        {/* Frame 4 draws Forget first and Cancel as the starting focus. The red verb
            leads the footer, so the confirmation's safe focus is declared here too,
            not only by the delayed native request. */}
        <Button focusId="cancel" ref={cancel} variant="glass" hasTVPreferredFocus disabled={busy} onPress={() => setConfirm(false)}>{t('action.cancel')}</Button>
      </View>}><View/></Dialog>
  </View>;
  return <OverlaysProvider><View testID="server-identity-failure" style={{flex: 1, backgroundColor: theme.colors.ink, overflow: 'hidden'}}>
    {tv ? <View style={{position: 'absolute', left: layout.left, top: layout.top, width: layout.width, height: layout.height,
      transformOrigin: 'top left', transform: [{scale: layout.scale}]}}>{page}</View> : page}
  </View></OverlaysProvider>;
}
