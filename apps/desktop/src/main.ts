// The desktop app's entry: the current web app, with mpv beside the page's own player.
import {invoke} from '@tauri-apps/api/core';
import {installPlaybackEnvironment} from '../../../web/src/platform/playback/environment.ts';
import {browserClientProfile} from '../../../web/src/platform/playback/client-profile.ts';
import {MpvPlayer, withDesktopMpv} from './mpv-player.ts';

installPlaybackEnvironment({
  // mpv is declared only where it runs, so a computer without it keeps being sent what its web view plays.
  capabilities: async () => {
    const [document, mpv] = await Promise.all([browserClientProfile(true), invoke<boolean>('mpv_available').catch(() => false)]);
    return mpv ? withDesktopMpv(document) : document;
  },
  secondEngine: ({resolveUrl}) => new MpvPlayer(invoke, resolveUrl),
});
await import('../../../web/src/main.tsx');
