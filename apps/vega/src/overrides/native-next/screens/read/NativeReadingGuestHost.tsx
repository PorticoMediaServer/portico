import React from 'react';
import {ReadNativeHost} from './ReadNativeHost';

/** Vega's SDK graph has no WebView. The shared guest host is reached from the Read route and
 * from a remembered download's Reading page, so it is replaced at the same path as well: Metro
 * never traverses `react-native-webview`, and either page that reaches it fails closed with the
 * same shared failure state as the Read host override. It is not a reader. */
export function NativeReadingGuestHost({active}: Readonly<{active: boolean}>) {
  return <ReadNativeHost active={active}/>;
}
