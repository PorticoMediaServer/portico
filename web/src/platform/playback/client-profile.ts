import {playbackEnvironment} from './environment';
import {configureDestination} from './audio/output';
import {browserConfigCheck, probeAudioDecode} from './audio/capabilities';
import {probeWebProfile, withViewerSettings, type CapabilityDocument, type DecodingAnswer, type WebProbeEnvironment} from '@core/client-profile.ts';
import {currentPlaybackFixes, onPlaybackFixesChanged} from './playback-fixes';
import {CapabilityPublisher} from '@core/playback-v1/options.ts';
import {localV1Http} from '@core/playback-v1/local-http.ts';
import type {HttpLocalApi} from '@core/index.ts';
import {declareSubtitleRendering} from './subtitle-declaration.ts';

/*
 * The real browser behind the shared probe. Each answer has a safe "no": a browser that cannot
 * be asked is described as one that cannot, and the server converts for it.
 */

function engine(): {engine: string; engineVersion: string; os: string} {
  const ua = navigator.userAgent;
  const firefox = /Firefox\/([\d.]+)/.exec(ua), blink = /(?:Chrome|Chromium|Edg)\/([\d.]+)/.exec(ua), webkit = /Version\/([\d.]+).*Safari/.exec(ua);
  const os = /iPhone|iPad/.test(ua) ? 'ios' : /Android/.test(ua) ? 'android' : /Mac OS X/.test(ua) ? 'macos' : /Windows/.test(ua) ? 'windows' : /CrOS/.test(ua) ? 'chromeos' : /Linux/.test(ua) ? 'linux' : '';
  if (firefox) return {engine: 'gecko', engineVersion: firefox[1], os};
  if (blink) return {engine: 'blink', engineVersion: blink[1], os};
  return {engine: 'webkit', engineVersion: webkit?.[1] ?? '', os};
}

function outputChannels(): number | undefined {
  try {
    const Context = window.AudioContext ?? (window as unknown as {webkitAudioContext?: typeof AudioContext}).webkitAudioContext;
    if (!Context) return undefined;
    const context = new Context();
    const channels = configureDestination(context.destination);
    void context.close().catch(() => {});
    return channels || undefined;
  } catch { return undefined; }
}

function environment(): WebProbeEnvironment {
  const element = document.createElement('video');
  // Safari on iPhone has ManagedMediaSource and no MediaSource.
  const Source = (window as unknown as {ManagedMediaSource?: typeof MediaSource}).ManagedMediaSource ?? (typeof MediaSource === 'undefined' ? undefined : MediaSource);
  const capabilities = navigator.mediaCapabilities;
  const browser = engine();
  return {
    canPlayType: type => { try { return element.canPlayType(type); } catch { return ''; } },
    mediaSourceSupports: Source ? type => { try { return Source.isTypeSupported(type); } catch { return false; } } : null,
    decodingInfo: capabilities?.decodingInfo
      ? async (question, type = 'media-source') => {
          const answer = await capabilities.decodingInfo({type, video: question as VideoConfiguration});
          return {supported: answer.supported, smooth: answer.smooth, powerEfficient: answer.powerEfficient} satisfies DecodingAnswer;
        }
      : undefined,
    matchMedia: query => { try { return matchMedia(query).matches; } catch { return false; } },
    screen: {width: Math.round(screen.width * devicePixelRatio), height: Math.round(screen.height * devicePixelRatio)},
    audioOutputChannels: outputChannels(),
    embeddedAudioTracks: 'audioTracks' in HTMLMediaElement.prototype,
    tonemapsHdr: browser.engine === 'webkit' || (browser.engine === 'blink' && browser.os === 'macos'),
    identity: {platform: 'web', os: browser.os, app: 'Portico Web', appVersion: import.meta.env.VITE_APP_VERSION ?? '', engineVersion: browser.engineVersion, platformVersion: browser.engine},
  };
}

let asked: Promise<CapabilityDocument> | undefined;
/** The capability document for this browser. Asked once, and again when the screen or the audio device changes.
 * It carries what the music engine decodes on the device (`audioDecode`, §18.1): pairs that passed
 * the §18.8 fixtures on this engine and that `AudioDecoder` confirms here. */
export function browserClientProfile(fresh = false): Promise<CapabilityDocument> {
  // The viewer's own switches for when detection is wrong travel with the document.
  return probedProfile(fresh).then(profile => withViewerSettings(profile, {overrides: currentPlaybackFixes()}));
}
function probedProfile(fresh: boolean): Promise<CapabilityDocument> {
  if (fresh || !asked) { const env = environment(); asked = Promise.all([probeWebProfile(env), probeAudioDecode(browserConfigCheck(), undefined, env.audioOutputChannels).catch(() => [])])
    // Declare only the subtitle surface actually mounted by this platform runtime.
    .then(([profile, audioDecode]): CapabilityDocument => declareSubtitleRendering({...profile, audioDecode: audioDecode.map(({sampleRates, containers, ...a}) => ({...a, containers: [...containers], ...(sampleRates ? {sampleRates: [...sampleRates]} : {})}))})); }
  return asked;
}

type Api = Pick<HttpLocalApi, 'requestRaw'>;

/**
 * Keeps the server told what this browser plays (its capability document, kept per device): once after sign-in, and again if the
 * answer changes (a window dragged to an HDR display, headphones swapped for a surround
 * receiver). It never throws and nothing waits for it. Returns a function that stops watching.
 */
export function keepBrowserProfilePublished(api: Api, signIn: string): () => void {
  const publisher = new CapabilityPublisher(localV1Http(api), () => signIn);
  let stopped = false;
  const publish = (fresh: boolean) => { void Promise.resolve().then(() => {
    const capabilities = playbackEnvironment().capabilities;
    // An installed engine owns its facts even when it cannot report them. Never replace an
    // absent or failed native answer with this WebView/browser's decoder or output claims.
    return capabilities ? capabilities() : browserClientProfile(fresh);
  }).then(profile => { if (!stopped && profile) void publisher.publish(profile); }).catch(() => {}); };
  publish(false);
  const changed = () => publish(true);
  const hdr = (() => { try { return matchMedia('(dynamic-range: high)'); } catch { return undefined; } })();
  hdr?.addEventListener?.('change', changed);
  navigator.mediaDevices?.addEventListener?.('devicechange', changed);
  // A switch changes only what is stamped on the document, so the probe is not asked again.
  const offFixes = onPlaybackFixesChanged(() => publish(false));
  return () => { stopped = true; offFixes(); hdr?.removeEventListener?.('change', changed); navigator.mediaDevices?.removeEventListener?.('devicechange', changed); };
}
