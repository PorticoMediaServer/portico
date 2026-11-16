import React, {useLayoutEffect, useRef} from 'react';
import {View, type ViewProps} from 'react-native';

/**
 * `@amazon-devices/react-native-w3cmedia` in a TV browser. The SDK's player is the W3C media
 * element with three additions, which the Fire TV engine (`apps/vega/src/bridge/playback.ts`) and
 * its HLS reader (`hls.ts`) use unchanged on top of this file:
 *
 * - `initialize`/`deinitialize`: the element's lifetime. Here one `<video>` per player.
 * - a *surface*: the view that shows the picture. `KeplerVideoSurfaceView` announces one by a
 *   handle; `setSurfaceHandle` puts the element inside it and `clearSurfaceHandle` takes it out,
 *   so the engine can keep playing while no view is mounted, as it does on Fire TV.
 * - `setMediaControlFocus`: who answers the remote's transport keys (`TransportKeys` in
 *   `system-media.ts`). The keys arrive as key events; `src/shims/keys.ts` hands them over.
 *
 * `srcObject = mediaSource` is how the SDK takes a media source; browsers take it as an object
 * URL, which is made and revoked here.
 */
export const MediaSource = globalThis.MediaSource;
export type SourceBuffer = globalThis.SourceBuffer;

type DecodingConfiguration = Parameters<MediaCapabilities['decodingInfo']>[0];
export function decodingInfo(configuration: DecodingConfiguration): Promise<MediaCapabilitiesDecodingInfo> {
  return navigator.mediaCapabilities.decodingInfo(configuration);
}

/** The remote's transport keys, as the SDK's handler class names them. */
export class KeplerMediaControlHandler {
  handlePlay?(): Promise<void>;
  handlePause?(): Promise<void>;
  handleTogglePlayPause?(): Promise<void>;
  handleStop?(): Promise<void>;
  handleFastForward?(): Promise<void>;
  handleRewind?(): Promise<void>;
  handleNext?(): Promise<void>;
  handlePrevious?(): Promise<void>;
}

let transportKeys: KeplerMediaControlHandler | null = null;
/** The handler the playing engine gave, for `src/shims/keys.ts`. */
export function currentTransportKeys(): KeplerMediaControlHandler | null {
  return transportKeys;
}

const surfaces = new Map<string, HTMLElement>();
let nextSurface = 0;

export class VideoPlayer {
  private readonly element: HTMLVideoElement = document.createElement('video');
  private objectUrl: string | null = null;
  private keys: KeplerMediaControlHandler | null = null;

  constructor() {
    const video = this.element;
    video.setAttribute('playsinline', '');
    video.disableRemotePlayback = true;
    Object.assign(video.style, {position: 'absolute', left: '0', top: '0', width: '100%', height: '100%', objectFit: 'contain', background: '#000'});
  }

  initialize(): Promise<void> {return Promise.resolve();}

  deinitialize(): Promise<void> {
    if (transportKeys === this.keys) transportKeys = null;
    this.keys = null;
    this.element.removeAttribute('src');
    try {this.element.load();} catch { /* nothing was loaded */ }
    this.revoke();
    this.element.remove();
    return Promise.resolve();
  }

  addEventListener(type: string, listener: () => void): void {this.element.addEventListener(type, listener);}
  removeEventListener(type: string, listener: () => void): void {this.element.removeEventListener(type, listener);}

  get currentTime(): number {return this.element.currentTime;}
  set currentTime(value: number) {this.element.currentTime = value;}
  get paused(): boolean {return this.element.paused;}
  get buffered(): TimeRanges {return this.element.buffered;}
  get error(): MediaError | null {return this.element.error;}
  get autoplay(): boolean {return this.element.autoplay;}
  set autoplay(value: boolean) {this.element.autoplay = value;}
  play(): Promise<void> {return this.element.play();}
  pause(): void {this.element.pause();}

  set srcObject(source: globalThis.MediaSource | null) {
    this.revoke();
    if (!source) {this.element.removeAttribute('src'); return;}
    this.objectUrl = URL.createObjectURL(source);
    this.element.src = this.objectUrl;
  }

  setSurfaceHandle(handle: string): void {
    const host = surfaces.get(handle);
    if (!host) throw new Error('unknown surface');
    if (this.element.parentNode !== host) host.appendChild(this.element);
  }

  clearSurfaceHandle(handle: string): void {
    const host = surfaces.get(handle);
    if (host && this.element.parentNode === host) host.removeChild(this.element);
  }

  setMediaControlFocus(_component: unknown, handler: KeplerMediaControlHandler): Promise<void> {
    this.keys = handler;
    transportKeys = handler;
    return Promise.resolve();
  }

  private revoke(): void {
    if (this.objectUrl) URL.revokeObjectURL(this.objectUrl);
    this.objectUrl = null;
  }
}

type SurfaceProps = ViewProps & {onSurfaceViewCreated: (handle: string) => void; onSurfaceViewDestroyed: (handle: string) => void};

/** A view that shows whichever player's element is given its handle. */
export function KeplerVideoSurfaceView({onSurfaceViewCreated, onSurfaceViewDestroyed, ...view}: SurfaceProps): React.ReactElement {
  const host = useRef<View>(null);
  useLayoutEffect(() => {
    const element = host.current as unknown as HTMLElement | null;
    if (!element) return;
    const handle = `surface-${++nextSurface}`;
    surfaces.set(handle, element);
    onSurfaceViewCreated(handle);
    return () => {
      surfaces.delete(handle);
      onSurfaceViewDestroyed(handle);
    };
    // The surface is announced once per mount; the engine keeps the callbacks it was given.
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, []);
  return <View ref={host} {...view} style={[view.style, {overflow: 'hidden', backgroundColor: '#000'}]}/>;
}
