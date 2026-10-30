// Vega's SDK has no WebView, and Metro replaces the shared reader host that imports one
// (src/overrides/native-next/screens/read/NativeReadingGuestHost.tsx), so none of this is
// bundled. tsc reads the shared sources without Metro's overrides; this declares the part of
// react-native-webview they use, so they are still type-checked in this program.
declare module 'react-native-webview' {
  import type {Component} from 'react';
  export type WebViewNavigation = {url: string; isTopFrame: boolean};
  export type WebViewMessageEvent = {nativeEvent: {url: string; data: string}};
  export type WebViewProps = {
    source: {uri: string};
    onShouldStartLoadWithRequest?: (request: WebViewNavigation) => boolean;
    onMessage?: (event: WebViewMessageEvent) => void;
    [prop: string]: unknown;
  };
  export default class WebView extends Component<WebViewProps> {
    injectJavaScript(script: string): void;
  }
}
