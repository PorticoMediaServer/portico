// The desktop app's entry: the current web app, with mpv beside the page's own player.
import {invoke} from '@tauri-apps/api/core';
import {installPlaybackEnvironment} from '../../../web/src/platform/playback/environment.ts';
import {browserClientProfile} from '../../../web/src/platform/playback/client-profile.ts';
import {MpvPlayer, withDesktopMpv} from './mpv-player.ts';
import {installPorticoCodeSignIn} from '../../../web/src/platform/portico-code-sign-in.ts';
import {signInWithCode} from './portico-code.ts';
import {installSavedDownloadsBinder} from '../../../web/src/screens/saved/native-binding.ts';
import {desktopDownloads, downloadReady, offlineDownloads} from './downloads.ts';
import {installOfflineDownloads} from '../../../web/src/shell/offline-downloads.ts';
import {browserViewerStorage} from '../../../web/src/platform/viewer-selection-storage.ts';
import {installAppKeptDownloads} from '../../../web/src/platform/browser-download.ts';

installPlaybackEnvironment({
  // mpv is declared only where it runs, so a computer without it keeps being sent what its web view plays.
  capabilities: async () => {
    const [document, mpv] = await Promise.all([browserClientProfile(true), invoke<boolean>('mpv_available').catch(() => false)]);
    return mpv ? withDesktopMpv(document) : document;
  },
  secondEngine: ({resolveUrl}) => new MpvPlayer(invoke, resolveUrl),
});
// Portico Account sign-in by a code approved in the browser; the browser opens through the window's new-window rule.
const system = /Windows/.test(navigator.userAgent) ? 'Windows' : /Mac/.test(navigator.userAgent) ? 'Mac' : 'Linux';
installPorticoCodeSignIn(signal => signInWithCode(invoke, url => { window.open(url, '_blank'); }, {name: `Portico on ${system}`, kind: 'desktop'}, signal));
// Downloads are kept in the app and play with no server (downloads rule 3).
installSavedDownloadsBinder(desktopDownloads(invoke));
installAppKeptDownloads(downloadReady);
// With no server in reach, the launch page offers the last profile's downloads that still play offline.
const last = await browserViewerStorage.read().catch(() => null);
if (last) installOfflineDownloads(offlineDownloads(invoke, last.session.viewer.serverId, last.session.viewer.profileId));
await import('../../../web/src/main.tsx');
