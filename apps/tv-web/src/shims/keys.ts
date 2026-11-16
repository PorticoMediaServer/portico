import {currentTransportKeys} from '../sdk/w3cmedia';
import {move} from './focus';
import {pressHandlers} from './press-registry';

/**
 * The remote, in a TV browser. Every key arrives as a key event; this file is the one listener.
 *
 * - The arrows move focus (`focus.tsx`) and, as on Apple TV and Fire TV, are also reported to
 *   `useTVEventHandler` listeners (the seek bar, the guide).
 * - OK presses the focused control; held for half a second it is the control's long press
 *   (Hold OK opens a card's menu).
 * - Back runs React Native's `hardwareBackPress` handlers, as Android's Back does
 *   (`kit/navigation/back.ts` walks the app's Back ladder). When nothing takes it, the app
 *   closes, as the mock-up's Back ladder says for Home.
 * - Play, Pause, Stop, Fast-forward and Rewind go to the playing video's transport-key handler
 *   when there is one (`system-media.ts`, as on Fire TV), else to the listeners.
 *
 * Listeners get the Fire TV app's events (`apps/vega/src/shims/react-native.ts`): a direction on
 * every down the set sends while it is held (`eventKeyAction` 0) and once on release (1); any
 * other key once on release, or as a `long…` pair when held. `tv-key-source` says so.
 *
 * Key codes: arrows, Enter, Escape and Backspace in any browser; LG's Back is 461; Samsung's is
 * 10009, and its media keys must be registered first (`platform/device.ts`).
 */
export type TVRemoteEvent = {eventType: string; eventKeyAction?: number};
type Listener = (event: TVRemoteEvent) => void;

const listeners = new Set<Listener>();
const backHandlers: (() => boolean | null | undefined)[] = [];

export function addTVListener(listener: Listener): () => void {
  listeners.add(listener);
  return () => {listeners.delete(listener);};
}

export function addBackHandler(handler: () => boolean | null | undefined): {remove: () => void} {
  backHandlers.push(handler);
  return {remove: () => {const index = backHandlers.lastIndexOf(handler); if (index >= 0) backHandlers.splice(index, 1);}};
}

let exitApp: () => void = () => {};
export function setExitApp(exit: () => void): void {exitApp = exit;}

function emit(event: TVRemoteEvent): void {
  for (const listener of Array.from(listeners)) {
    try { listener(event); } catch (error) { setTimeout(() => {throw error;}); }
  }
}

const arrows: Record<number, 'up' | 'down' | 'left' | 'right'> = {38: 'up', 40: 'down', 37: 'left', 39: 'right'};
const BACK = new Set([461, 10009, 27]);
type Handler = 'handleTogglePlayPause' | 'handlePlay' | 'handlePause' | 'handleStop' | 'handleFastForward' | 'handleRewind';
const media: Record<number, {name: string; handler: Handler}> = {
  10252: {name: 'playPause', handler: 'handleTogglePlayPause'}, 179: {name: 'playPause', handler: 'handleTogglePlayPause'},
  415: {name: 'playPause', handler: 'handlePlay'}, 19: {name: 'playPause', handler: 'handlePause'}, 413: {name: 'stop', handler: 'handleStop'},
  417: {name: 'fastForward', handler: 'handleFastForward'}, 412: {name: 'rewind', handler: 'handleRewind'},
};
const channels: Record<number, string> = {427: 'channelUp', 428: 'channelDown', 33: 'channelUp', 34: 'channelDown'};

/** A text field the viewer is typing into (`components.tsx`): its keys are its own, Up and Down aside. */
const typing = (target: EventTarget | null) => (target instanceof HTMLInputElement || target instanceof HTMLTextAreaElement) && !target.readOnly;
const field = (target: EventTarget | null) => target instanceof HTMLInputElement || target instanceof HTMLTextAreaElement;

const LONG_MS = 500;
type Held = {name: string; timer: ReturnType<typeof setTimeout>; long: boolean; element?: HTMLElement};
const held = new Map<number, Held>();

/** A key other than a direction: reported on release, or as a long pair once held. */
function hold(code: number, name: string, onLong?: () => boolean): Held {
  const key: Held = {name, long: false, timer: setTimeout(() => {
    key.long = true;
    emit({eventType: 'long' + name.charAt(0).toUpperCase() + name.slice(1), eventKeyAction: 0});
    // A control with no long press keeps an ordinary press for the release.
    if (onLong && !onLong()) key.long = false;
  }, LONG_MS)};
  held.set(code, key);
  return key;
}

function release(code: number): Held | undefined {
  const key = held.get(code);
  held.delete(code);
  if (!key) return undefined;
  clearTimeout(key.timer);
  emit(key.long ? {eventType: 'long' + key.name.charAt(0).toUpperCase() + key.name.slice(1), eventKeyAction: 1} : {eventType: key.name});
  return key;
}

function back(): void {
  for (let index = backHandlers.length - 1; index >= 0; index--) {
    if (backHandlers[index]?.() === true) return;
  }
  exitApp();
}

window.addEventListener('keydown', event => {
  const code = event.keyCode;
  if (arrows[code]) {
    if (typing(event.target) && (code === 37 || code === 39)) return;
    event.preventDefault();
    emit({eventType: arrows[code], eventKeyAction: 0});
    move(arrows[code]);
    return;
  }
  if (code === 13) {
    if (field(event.target)) return;
    const element = document.activeElement instanceof HTMLElement ? document.activeElement : undefined;
    const handlers = element ? pressHandlers(element) : undefined;
    if (handlers) {event.preventDefault(); event.stopPropagation();}
    if (held.has(code)) return;
    handlers?.pressIn?.();
    hold(code, 'select', () => handlers?.longPress?.() === true).element = element;
    return;
  }
  const isBack = BACK.has(code) || code === 8 && !typing(event.target);
  const key = media[code];
  if (isBack || key || channels[code]) {
    event.preventDefault();
    if (held.has(code)) return;
    hold(code, isBack ? 'menu' : key ? key.name : channels[code]);
  }
}, true);

window.addEventListener('keyup', event => {
  const code = event.keyCode;
  if (arrows[code]) {
    if (!typing(event.target)) emit({eventType: arrows[code], eventKeyAction: 1});
    return;
  }
  if (code === 13) {
    if (field(event.target)) return;
    const key = release(code);
    if (!key?.element) return;
    event.preventDefault();
    event.stopPropagation();
    const handlers = pressHandlers(key.element);
    handlers?.pressOut?.();
    if (!key.long && document.activeElement === key.element) handlers?.press?.();
    return;
  }
  if (!held.has(code)) return;
  // Transport keys go to the playing video's handler first; listeners hear them otherwise.
  const transport = media[code] ? currentTransportKeys() : null;
  const handler = transport?.[media[code].handler];
  if (handler) {
    clearTimeout(held.get(code)!.timer);
    held.delete(code);
    void handler.call(transport);
    return;
  }
  const key = release(code);
  if (key && key.name === 'menu' && !key.long) back();
}, true);
