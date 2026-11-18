import test from 'node:test';
import assert from 'node:assert/strict';
import {readdirSync, readFileSync} from 'node:fs';

// Owner, 17 Nov: ONE backdrop everywhere. kit/shell/Layer.tsx is the only file that draws it, and only the files
// below, each of which mounts something that must hide what is beneath it, may put that thing on a Layer. A screen
// that wants a backdrop of its own is asking for a fix in its host or its frame, not for a line here.
const root = new URL('../src-next/', import.meta.url);
const HOST = 'kit/shell/Layer.tsx';
const LAYERS: Record<string, string> = {
  'App.tsx': 'the app root: boot, sign-in, the account pages, the TV shell page',
  'shell/TouchStacks.tsx': 'every native stack screen',
  'shell/assembly.tsx': 'the TV route layer',
  'shell/NativeShell.tsx': 'Settings as a page on a tablet',
  'shell/StartupBoot.tsx': 'boot, while it fades out over the shell',
  'shell/TouchPlaybackPreparation.tsx': 'the wait before playback starts',
  'shell/TVPlaybackPreparation.tsx': 'the wait before playback starts, TV',
  'shell/TVStartFailure.tsx': 'the TV page for a playback that could not start (player layer)',
  'tv/auth/TVProfileSwitchPage.tsx': 'the TV profile picker, a window of its own',
  'screens/destinations/music/NowPlayingScreen.tsx': 'the audio player (audio layer)',
  'screens/destinations/music/TVNowPlayingScreen.tsx': 'the TV audio player (audio layer) and its equaliser page',
  'screens/watch/WatchHost.tsx': 'the full-window video player (status bar only: the picture is its own native surface)',
  'screens/watch/PlayOnRemote.tsx': 'the remote control page, a window of its own',
  'screens/share/NativeContentLinks.tsx': 'the page for a link that cannot be opened',
  'screens/share/NativeShareLinks.tsx': 'the page for a share link that cannot be opened',
  'screens/settings-live/server/channels/ChannelEditor.tsx': 'the channel editor and its preview, windows of their own',
  'dev/families/artwork/artwork.gallery.tsx': 'the Kit gallery case that shows the backdrop',
};

function sources(dir = ''): string[] {
  return readdirSync(new URL(dir, root), {withFileTypes: true}).flatMap(entry => entry.isDirectory() ? sources(`${dir}${entry.name}/`)
    : /\.tsx?$/.test(entry.name) ? [`${dir}${entry.name}`] : []);
}

test('only the layer host draws the backdrop, and only named layers stand on it', () => {
  const usesLayer: string[] = [];
  for (const file of sources()) {
    const text = readFileSync(new URL(file, root), 'utf8');
    if (file !== HOST && file !== 'kit/artwork/Surfaces.tsx')
      assert.doesNotMatch(text, /StandardBackdrop|ColourBackdrop|SettingsBackdrop/, `${file} draws a backdrop of its own`);
    if (file !== HOST) assert.doesNotMatch(text, /<StatusBar\b|StatusBar\.(push|set)/, `${file} sets the status bar; that is the layer's business`);
    if (file !== HOST && /shell\/Layer'/.test(text)) usesLayer.push(file);
  }
  assert.deepEqual(usesLayer.sort(), Object.keys(LAYERS).sort());
  // The backdrop is not offered by the kit's artwork index, so nothing can pick it up by accident.
  assert.doesNotMatch(readFileSync(new URL('kit/artwork/index.ts', root), 'utf8'), /Backdrop/);
});
