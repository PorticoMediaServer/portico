import {invoke} from '@tauri-apps/api/core';
import {collectDesktopMeasurement} from './measurement-transport.ts';
import {gallerySearch} from '../../../web/src/platform/gallery-search.ts';

/** DEV transport only. The shared collector and validator remain the sole
 * measurement implementation; this module never creates or alters screen DOM. */
export async function installDesktopMeasurement(signal:AbortSignal){
  if(!import.meta.env.DEV)return;
  const options=await invoke<unknown>('desktop_measurement_options');
  if(options===null||signal.aborted)return;
  const {loadGalleryFamilies}=await import('../../../web/src/dev/gallery-registry.ts');
  const query=gallerySearch(Object.fromEntries(new URLSearchParams(location.search)));
  if(!query.family||!query.case)throw new Error('desktop_measurement_unmounted');
  const families=await loadGalleryFamilies(query.family);
  // The existing gallery deliberately has a human-friendly fallback. A measured
  // run must never silently substitute that first case for a missing specimen.
  if(!families.find(family=>family.id===query.family)?.cases.some(specimen=>specimen.id===query.case))throw new Error('desktop_measurement_unmounted');
  const {measureScreen,validateMeasurement}=await import('virtual:portico-desktop-fidelity');
  const mounted=()=>Boolean(document.querySelector('[data-gallery-ready="true"] [data-gallery-capture]')?.childElementCount);
  if(!mounted())await new Promise<void>((resolve,reject)=>{
    const observer=new MutationObserver(()=>{if(mounted()){cleanup();resolve();}});
    const fail=()=>{cleanup();reject(new Error('desktop_measurement_unmounted'));};
    const timer=setTimeout(fail,15_000);
    const cleanup=()=>{clearTimeout(timer);observer.disconnect();signal.removeEventListener('abort',fail);};
    observer.observe(document.documentElement,{subtree:true,childList:true,attributes:true});
    signal.addEventListener('abort',fail,{once:true});
    if(signal.aborted)fail();else if(mounted()){cleanup();resolve();}
  });
  signal.throwIfAborted();
  // Observe focus as it exists. No focus(), fixture mutation, synthetic DPI,
  // crop, font replacement or offset is applied to make a result pass.
  await collectDesktopMeasurement(options,{measure:measureScreen,validate:validateMeasurement,focus:()=>document.activeElement,write:actual=>invoke<void>('desktop_measurement_write',{report:actual})},signal);
}
