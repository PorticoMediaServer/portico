import {useEffect} from 'react';
import {DeviceEqualizer} from '../../../web/src/kit/media/Equalizer';
import {createDesktopEqualizerPort} from './equalizer-port';

const errors=new Set<(error:unknown)=>void>();
/** The player shortcut and Settings receive this same app/device-native store. */
export const desktopEqualizerPort=createDesktopEqualizerPort(error=>{for(const listener of errors)listener(error);});

export function DesktopSettingsEqualizer({layout,onError}:Readonly<{layout:'settings';onError:(error:unknown)=>void}>) {
  useEffect(()=>{errors.add(onError);return()=>{errors.delete(onError);};},[onError]);
  // No browser AudioContext, guessed device rate or inferred bit-perfect offer.
  return <DeviceEqualizer port={desktopEqualizerPort} layout={layout} reportError={onError}/>;
}
