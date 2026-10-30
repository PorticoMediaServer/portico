import {invoke} from '@tauri-apps/api/core';
import {validInstallationId,type InstallationIdentity} from '../../../packages/client-core/src/installation.ts';

/** The native store owns this secret for the lifetime of the app installation.
 * No browser ID, hardware identifier, account ID or recovery fallback is made here. */
export function desktopInstallation(raw:unknown):InstallationIdentity{
  if(!raw||typeof raw!=='object'||Array.isArray(raw))throw new Error('device_unavailable');
  const value=raw as Record<string,unknown>;
  if(!validInstallationId(value.installationId)||typeof value.name!=='string'||value.name.length>1024||/[\x00-\x1f\x7f-\x9f]/.test(value.name)||!['linux','macos','windows'].includes(String(value.platform))
    ||value.app!=='Portico'||typeof value.appVersion!=='string'||!/^\d+\.\d+\.\d+(?:[-+][A-Za-z0-9.-]+)?$/.test(value.appVersion))throw new Error('device_unavailable');
  return Object.freeze({installationId:value.installationId,name:value.name,platform:String(value.platform),app:'Portico',appVersion:value.appVersion});
}
export const nativeInstallation=()=>invoke<unknown>('desktop_installation').then(desktopInstallation);
