import {invoke} from '@tauri-apps/api/core';
import type {SettingsDeviceFacts,SettingsDevicePort} from '../../../web/src/platform/settings-platform.ts';

/** Explicit projection: native diagnostics, paths, document defaults and update guesses
 * cannot become Settings facts. A facts-only port has no write method. */
export function parseDeviceFacts(raw:unknown):SettingsDeviceFacts {
  if(!raw||typeof raw!=='object'||Array.isArray(raw))throw new Error('device_unavailable');
  const value=raw as Record<string,unknown>;
  if(typeof value.appVersion!=='string'||!/^\d+\.\d+\.\d+(?:[-+][A-Za-z0-9.-]+)?$/.test(value.appVersion))throw new Error('device_unavailable');
  let name:SettingsDeviceFacts['name'];
  if(value.name!==undefined){
    if(!value.name||typeof value.name!=='object'||Array.isArray(value.name))throw new Error('device_unavailable');
    const native=value.name as Record<string,unknown>;
    if(typeof native.value!=='string'||!native.value.trim()||[...native.value].length>255||new TextEncoder().encode(native.value).length>1024||/[\x00-\x1f\x7f-\x9f]/.test(native.value)||native.readOnly!==true)throw new Error('device_unavailable');
    name=Object.freeze({value:native.value,readOnly:true});
  }
  return Object.freeze({appVersion:value.appVersion,...(name?{name}:{})});
}
export type DesktopDevicePort=SettingsDevicePort&Readonly<{dispose():void}>;
export function createDesktopDevicePort(facts:SettingsDeviceFacts):DesktopDevicePort {
  const snapshot=Object.freeze({facts});
  return Object.freeze({getSnapshot:()=>snapshot,subscribe:()=>()=>{},dispose(){}});
}
export const nativeDeviceFacts=()=>invoke<unknown>('desktop_device_facts').then(parseDeviceFacts);
