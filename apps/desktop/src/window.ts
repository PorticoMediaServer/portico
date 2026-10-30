import {invoke} from '@tauri-apps/api/core';
import type {WindowChromeState} from '../../../web/src/platform/window-chrome.ts';

export type WindowChromeHostState = WindowChromeState;
export type CaptionNames = Readonly<{minimize:string;maximize:string;restore:string;close:string}>;
export type WindowSnapshot = Readonly<{os:'macos'|'windows'|'linux';fullscreen:boolean;maximized:boolean;focused:boolean;captions:CaptionNames|null;captionHover:0|1|2|3}>;
export type CaptionRect = Readonly<{x:number;y:number;width:number;height:number}>;
export type ChromeUpdate = Readonly<{enabled:boolean;surface:WindowChromeHostState['surface'];controlsVisible:boolean;fullscreen:boolean;captions:{minimize:CaptionRect;maximize:CaptionRect;close:CaptionRect}|null}>;
export type WindowAction = 'minimize'|'toggleMaximize'|'close'|'drag';
export interface WindowBridge {read():Promise<WindowSnapshot>;chrome(value:ChromeUpdate):Promise<void>;action(value:WindowAction):Promise<void>}

function snapshot(value:unknown):WindowSnapshot {
  if (!value||typeof value!=='object') throw new Error('window_unavailable');
  const wire=value as Record<string,unknown>;
  if (!['macos','windows','linux'].includes(String(wire.os))||typeof wire.fullscreen!=='boolean'||typeof wire.maximized!=='boolean'||typeof wire.focused!=='boolean'||![0,1,2,3].includes(Number(wire.captionHover))||typeof wire.captionHover!=='number') throw new Error('window_unavailable');
  let captions:CaptionNames|null=null;
  if (wire.captions!==null) {
    if (!wire.captions||typeof wire.captions!=='object') throw new Error('window_unavailable');
    const names=wire.captions as Record<string,unknown>;
    if (!['minimize','maximize','restore','close'].every(key=>typeof names[key]==='string'&&names[key].length>0&&names[key].length<=256)) throw new Error('window_unavailable');
    captions={minimize:String(names.minimize),maximize:String(names.maximize),restore:String(names.restore),close:String(names.close)};
  }
  return {os:wire.os as WindowSnapshot['os'],fullscreen:wire.fullscreen,maximized:wire.maximized,focused:wire.focused,captions,captionHover:wire.captionHover as WindowSnapshot['captionHover']};
}
export const nativeWindowBridge:WindowBridge={
  read:()=>invoke<unknown>('desktop_window_state').then(snapshot),
  chrome:chrome=>invoke<void>('desktop_window_chrome',{chrome}),
  action:action=>action==='close'?invoke<void>('desktop_app_close'):invoke<void>('desktop_window_action',{action}),
};
