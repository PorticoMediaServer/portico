// The desktop app's entry: the current web app, with mpv beside the page's own player.
import {invoke} from '@tauri-apps/api/core';
import {installPlaybackEnvironment} from '../../../web/src/platform/playback/environment.ts';
import {browserClientProfile} from '../../../web/src/platform/playback/client-profile.ts';
import {MpvPlayer, withDesktopMpv} from './mpv-player.ts';

installPlaybackEnvironment({
  capabilities: async () => withDesktopMpv(await browserClientProfile(true)),
  secondEngine: ({resolveUrl}) => new MpvPlayer(invoke, resolveUrl),
});
await import('../../../web/src/main.tsx');
