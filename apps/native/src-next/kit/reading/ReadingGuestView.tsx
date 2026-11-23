import React from 'react';
import WebView from 'react-native-webview';
import {createReadingHost} from '../../../../../packages/client-core/src';
import {routeBase64} from '../../../../../packages/client-core/src/route-identity';
import {nativeRouteCrypto} from '../../platform/server-connections';
import {readingHighlightsSavePort} from '../../platform/reading-highlights-save';
import {readerDelivery, readerNavigationAllowed} from '../../screens/read/asset-origin';
import {readerStartupDiagnostic, readerStartupProbe} from '../../screens/read/startup-diagnostics';

type Reader = ReturnType<typeof createReadingHost>;

/** Credential-free native view. The captured reader must still own this view
 * when a message or an error arrives; an old view cannot address its successor. */
export function ReadingGuestView({url, view, reader, currentReader, backgroundColor, observeBridge, onFailure, safeTop: top = 0}: {
  url: string; view: React.RefObject<WebView | null>; reader: Reader;
  currentReader: () => Reader | undefined; backgroundColor: string;
  observeBridge: (direction: 'guest-to-host' | 'host-to-guest', message: string) => void;
  onFailure: () => void;
  /** The app's top inset in points, which the page runs under. */
  safeTop?: number;
}) {
  // The page runs under the status bar. Android's WebView gives env(safe-area-inset-top) only for a display cutout, so a tablet
  // without one drew the reader's bar under the status bar: the page is told the app's own top inset (reader.module.css).
  const safeTop = nativeSafeTop(top);
  return <WebView ref={view} source={{uri: url}} style={{flex: 1, backgroundColor}}
    originWhitelist={['*']} onShouldStartLoadWithRequest={request => readerNavigationAllowed(url, request.url, request.isTopFrame)}
    injectedJavaScriptBeforeContentLoaded={(__DEV__ ? readerStartupProbe : '') + safeTop}
    injectedJavaScriptBeforeContentLoadedForMainFrameOnly
    injectedJavaScript={safeTop}
    injectedJavaScriptForMainFrameOnly
    onMessage={event => {if (currentReader() === reader && readerNavigationAllowed(url, event.nativeEvent.url, true)) {
      if (__DEV__) {const diagnostic = readerStartupDiagnostic(event.nativeEvent.url, event.nativeEvent.data);
        if (diagnostic) {console.warn('portico_reader_startup', JSON.stringify(diagnostic)); return;}}
      observeBridge('guest-to-host', event.nativeEvent.data); reader.receive(event.nativeEvent.data);
    }}}
    renderError={() => <></>} incognito cacheEnabled={false} sharedCookiesEnabled={false} thirdPartyCookiesEnabled={false} domStorageEnabled={false}
    allowFileAccess={false} allowFileAccessFromFileURLs={false} allowUniversalAccessFromFileURLs={false} mixedContentMode="never"
    javaScriptCanOpenWindowsAutomatically={false} setSupportMultipleWindows={false} onOpenWindow={() => {}}
    onError={onFailure} onContentProcessDidTerminate={onFailure} onRenderProcessGone={onFailure}/>;
}

/** Sets the page's --native-safe-top to the app's top inset: a whole number of points, never page text. */
export function nativeSafeTop(top: number) {
  return `document.documentElement&&document.documentElement.style.setProperty('--native-safe-top','${Math.max(0, Math.round(top))}px');true;`;
}

/** Build the credential-free bridge for the exact session supplied by its owner.
 * The owner retains admission, authority, activity and session close. */
export function createReadingGuestBridge(options: Omit<Parameters<typeof createReadingHost>[0], 'channel' | 'send' | 'saveHighlights'>,
  view: React.RefObject<WebView | null>, observe: (direction: 'host-to-guest', message: string) => void) {
  return createReadingHost({...options, channel: routeBase64(nativeRouteCrypto.random(24)), saveHighlights: readingHighlightsSavePort(),
    send: message => {observe('host-to-guest', message); view.current?.injectJavaScript(readerDelivery(message));}});
}
