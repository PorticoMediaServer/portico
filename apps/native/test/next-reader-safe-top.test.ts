import test from 'node:test';
import assert from 'node:assert/strict';
import {readFileSync} from 'node:fs';
import {screenModule} from './helpers/screen-harness.mjs';

// A tablet without a display cutout drew the reader's bar under the status bar: Android's WebView reports
// env(safe-area-inset-top) only for a cutout. The app hands the page its own top inset, and the page takes the larger.
test('the reader page clears the status bar with the app\'s own top inset where the WebView reports none', () => {
  const css = readFileSync(new URL('../../../web/src/kit/reader/reader.module.css', import.meta.url), 'utf8');
  assert.match(css, /--reader-safe-top:\s*max\(env\(safe-area-inset-top,\s*0px\),\s*var\(--native-safe-top,\s*0px\)\)/);
  const view = readFileSync(new URL('../src-next/kit/reading/ReadingGuestView.tsx', import.meta.url), 'utf8');
  assert.match(view, /setProperty\('--native-safe-top'/);
  assert.match(view, /injectedJavaScriptBeforeContentLoaded=\{[^}]*safeTop\}/);
  assert.match(view, /injectedJavaScript=\{safeTop\}/);
  const page = readFileSync(new URL('../src-next/pages/touch/reading/ReaderGuestPage.tsx', import.meta.url), 'utf8');
  assert.match(page, /safeTop=\{safeTop\}/);
});

test('the injected script carries the inset as whole points, and nothing else', async () => {
  const {nativeSafeTop} = await screenModule(new URL('../src-next/kit/reading/ReadingGuestView.tsx', import.meta.url), new Proxy({react: {default: {}}, 'react-native-webview': {default: 'WebView'}} as Record<string, unknown>, {get: (all, name) => all[name as string] ?? {}}));
  assert.equal(nativeSafeTop(47.4), "document.documentElement&&document.documentElement.style.setProperty('--native-safe-top','47px');true;");
  assert.match(nativeSafeTop(-3), /'0px'/);
});
