import type {VideoLike} from './web-media-player.ts';

export type AirPlaySource = Readonly<{sessionId: string; generation: number; url: string}>;
export function wirelessPlayback(video: VideoLike | undefined): boolean {return video?.webkitCurrentPlaybackTargetIsWireless === true;}
const sources = new WeakMap<VideoLike, {generation: number; current(): boolean}>();
export function airPlaySourceReady(video: VideoLike, generation: number): boolean {
  const source = sources.get(video);return source?.generation === generation && source.current();
}

/** One load owns its local source and prepared receiver alternative. The receiver URL comes
 * from the server's exact generation/choice receipt, never from the page's local/blob URL.
 * Classic hls.js installs src=blob; MMS removes children and appends a blob source. Normalize
 * classic MSE during MEDIA_ATTACHING, before sourceopen: WebKit ignores alternatives while
 * a src attribute exists. Keep the engine blob first, and leave it for hls.js's own detach. */
export function airPlaySources(video: VideoLike, generation: number, current: () => boolean) {
  const host = video as VideoLike & Partial<Pick<HTMLVideoElement, 'ownerDocument' | 'appendChild' | 'querySelectorAll' | 'getAttribute' | 'disableRemotePlayback'>>;
  const enabled = typeof video.webkitShowPlaybackTargetPicker === 'function' && !!video.canPlayType('application/vnd.apple.mpegurl')
    && !!host.ownerDocument && !!host.appendChild && !!host.querySelectorAll && !!host.getAttribute;
  const wasDisabled = host.disableRemotePlayback === true;
  if (enabled && !wasDisabled && !wirelessPlayback(video)) host.disableRemotePlayback = true;
  let local: HTMLSourceElement | undefined, alternative: HTMLSourceElement | undefined, receiver: string | undefined;
  const entry = {generation, current: () => current() && !!alternative && Array.from(host.querySelectorAll!('source')).includes(alternative) && !host.getAttribute!('src') && !host.disableRemotePlayback};
  const removeReceiver = () => {alternative?.remove();alternative = undefined;if (sources.get(video) === entry) sources.delete(video);if (enabled && !wasDisabled && !wirelessPlayback(video)) host.disableRemotePlayback = true;};
  const install = () => {
    removeReceiver();
    if (!enabled || !current() || !receiver || host.getAttribute!('src') || !host.querySelectorAll!('source').length) return;
    alternative = host.ownerDocument!.createElement('source');alternative.type = 'application/vnd.apple.mpegurl';alternative.src = receiver;
    host.appendChild!(alternative);
    // MMS temporarily disables remote playback until it has a source alternative.
    if (!wasDisabled) host.disableRemotePlayback = false;
    sources.set(video, entry);
  };
  return {
    setReceiver: (url: string | undefined) => {receiver = url && /^https?:\/\//.test(url) ? url : undefined;install();},
    native: (url: string) => {
      if (!enabled) return;
      local?.remove();local = host.ownerDocument!.createElement('source');local.src = url;
      host.appendChild!(local);host.removeAttribute('src');install();
    },
    attach: () => {
      if (!enabled || !current()) return;
      removeReceiver();local?.remove();local = undefined;
      const blob = host.getAttribute!('src');
      if (blob?.startsWith('blob:')) {
        local = host.ownerDocument!.createElement('source');local.type = 'video/mp4';local.src = blob;
        host.appendChild!(local);host.removeAttribute('src');install();host.load();
      } else install();
    },
    // Called before engine destruction. Retain its blob so buffer-controller can recognize,
    // revoke and detach the actual MediaSource. Dispose our remaining nodes afterwards.
    detach: removeReceiver,
    dispose: () => {removeReceiver();local?.remove();local = undefined;if (enabled && !wasDisabled) host.disableRemotePlayback = false;},
  };
}
