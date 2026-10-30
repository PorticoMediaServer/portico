import {KeplerMediaControlHandler} from '@amazon-devices/react-native-w3cmedia';
import {trace} from '../debug';
import {ModuleEvents, withEvents} from './events';
import type {NativeBinding} from '../../../native/src-next/platform/playback/NativePlaybackCoordinator';

/**
 * `NativeModules.PorticoSystemMedia`: the video's place in the system (the remote's transport
 * keys, voice commands, the output route). Events: `PorticoSystemMediaCommand`,
 * `PorticoSystemMediaRoute`.
 *
 * Left to itself the system acts on the media element directly when the viewer presses
 * Play/Pause or asks for something by voice, behind the player's back. `TransportKeys` is given
 * to the element in its place (`playback.ts`): each request becomes a command to the shared
 * player, as the Apple module's remote commands do, and the player decides.
 *
 * A television's own output is not an external route, so `currentRoute` always answers "none".
 */
export const systemMediaEvents = new ModuleEvents();

type Video = Readonly<{itemId: string; skipBackSeconds?: number; skipForwardSeconds?: number}>;
type Time = Readonly<{seconds: number; nanoseconds: number}>;
export type SystemMediaSource = Readonly<{instanceId: string; itemInstanceId: string; binding: NativeBinding}>;
export type SystemMediaCommand = Readonly<{command: string; itemId: string; reason: 'remote'; source: SystemMediaSource; positionSeconds?: number; deltaSeconds?: number}>;
export type SystemMediaRegistration = Readonly<{source: SystemMediaSource; current: () => boolean; retire: () => void}>;
let registered: SystemMediaRegistration | undefined;
let video: Readonly<{owner: object; binding: NativeBinding; info: Video}> | null = null;
const sameBinding = (a: NativeBinding, b: NativeBinding) => a.lease === b.lease && a.intentId === b.intentId && a.sessionId === b.sessionId && a.generation === b.generation && a.itemId === b.itemId;

/** Each SDK handler keeps this original engine registration, never whichever video is latest. */
export function registerSystemMediaSource(source: SystemMediaSource, current: () => boolean): SystemMediaRegistration {
  const original = Object.freeze({...source, binding: Object.freeze({...source.binding})});
  const registration: SystemMediaRegistration = Object.freeze({source: original,
    current: () => registered === registration && current(),
    retire: () => {if (registered === registration) registered = undefined;},
  });
  registered = registration;
  return registration;
}

/** ModuleEvents preserves this original source object; queued/retired producers are refused. */
export function systemMediaCommandCurrent(command: unknown, owner: object): command is SystemMediaCommand {
  if (!command || typeof command !== 'object') return false;
  const value = command as Partial<SystemMediaCommand>;
  return typeof value.command === 'string' && value.reason === 'remote' && !!video && video.owner === owner && !!registered && value.source === registered.source
    && registered.current() && sameBinding(video.binding, value.source.binding) && value.itemId === value.source.binding.itemId;
}

const seconds = (time: Time | undefined) => (time ? Number(time.seconds) + Number(time.nanoseconds || 0) / 1e9 : NaN);
function send(registration: SystemMediaRegistration, command: Readonly<Record<string, unknown>>): Promise<void> {
  const original = registration.source;
  if (!registration.current() || !video || !sameBinding(video.binding, original.binding) || video.info.itemId !== original.binding.itemId) return Promise.resolve();
  trace(`system media ${String(command.command)}`);
  systemMediaEvents.emit('PorticoSystemMediaCommand', {...command, itemId: original.binding.itemId, reason: 'remote', source: original});
  return Promise.resolve();
}
function jump(registration: SystemMediaRegistration, direction: 'back' | 'forward'): Promise<void> {
  const amount = direction === 'back' ? video?.info.skipBackSeconds : video?.info.skipForwardSeconds;
  if (typeof amount !== 'number' || !Number.isFinite(amount) || amount < 0) return Promise.resolve();
  return send(registration, {command: 'skip', deltaSeconds: direction === 'back' ? -amount : amount});
}

export class TransportKeys extends KeplerMediaControlHandler {
  constructor(private readonly registration: SystemMediaRegistration) {super();}
  handlePlay(): Promise<void> { return send(this.registration, {command: 'play'}); }
  handlePause(): Promise<void> { return send(this.registration, {command: 'pause'}); }
  handleTogglePlayPause(): Promise<void> { return send(this.registration, {command: 'toggle'}); }
  handleStop(): Promise<void> { return send(this.registration, {command: 'pause'}); }
  handleStartOver(): Promise<void> { return send(this.registration, {command: 'seek', positionSeconds: 0}); }
  handleFastForward(): Promise<void> { return jump(this.registration, 'forward'); }
  handleRewind(): Promise<void> { return jump(this.registration, 'back'); }
  handleSeek(position: Time): Promise<void> { return send(this.registration, {command: 'seek', positionSeconds: Math.max(0, seconds(position))}); }
  handleSkipForward(delta: Time): Promise<void> { return send(this.registration, {command: 'skip', deltaSeconds: Math.abs(seconds(delta))}); }
  handleSkipBackward(delta: Time): Promise<void> { return send(this.registration, {command: 'skip', deltaSeconds: -Math.abs(seconds(delta))}); }
  handleNext(): Promise<void> { return send(this.registration, {command: 'next'}); }
  handlePrevious(): Promise<void> { return send(this.registration, {command: 'previous'}); }
  // Speed, volume and track requests are the player screen's to offer, not the system's.
  handleSetPlaybackSpeed(): Promise<void> { return Promise.resolve(); }
  handleSetAudioVolume(): Promise<void> { return Promise.resolve(); }
  handleSetAudioTrack(): Promise<void> { return Promise.resolve(); }
  handleEnableTextTrack(): Promise<void> { return Promise.resolve(); }
  handleDisableTextTrack(): Promise<void> { return Promise.resolve(); }
}

export const PorticoSystemMedia = withEvents({
  setVideo(info: Video, owner: object, binding: NativeBinding): void {
    if (!owner || !binding || !info || typeof info.itemId !== 'string' || info.itemId !== binding.itemId) return;
    video = {owner, binding: Object.freeze({...binding}), info: Object.freeze({...info})};
  },
  clear(owner: object): void {if (video?.owner === owner) video = null;},
  currentRoute: (): Promise<{external: boolean; kind: 'none'; name: string}> => Promise.resolve({external: false, kind: 'none', name: ''}),
}, systemMediaEvents);
