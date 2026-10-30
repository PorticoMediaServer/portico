import React from 'react';
import {Text, View} from 'react-native';
import {defaultI18n} from '../../../../../../../packages/i18n/src';

type Props = Readonly<{active: boolean}>;

/** Vega's SDK graph has no WebView, and the Read route is never admitted on TV
 * (`nativeReadRouteAllowed`). This platform override replaces the Apple host at
 * the same path so Metro never traverses `react-native-webview`, and fails
 * closed with the shared failure state if a route ever reaches it. It is not a
 * reader and claims no Reading support. */
export function ReadNativeHost({active}: Props) {
  return <View pointerEvents={active ? 'auto' : 'none'} style={{flex: 1, alignItems: 'center', justifyContent: 'center', gap: 10,
    padding: 48, backgroundColor: '#0c151d'}}>
    <Text style={{color: '#edf5fa', fontSize: 20, fontWeight: '700', textAlign: 'center'}}>{defaultI18n.t('next.states.pageFailed')}</Text>
    <Text style={{color: '#9db0bd', fontSize: 15, lineHeight: 22, textAlign: 'center'}}>{defaultI18n.t('next.states.pageFailedBody')}</Text>
  </View>;
}
