import {useLayoutEffect, useState} from 'react';
import {LocalServerAddress} from '../kit/accounts/ServerSwitcher';
import {servicesLocalOrigin} from './directory-connection';

/** A local-account server keeps its own web app at its own address: the typed address is
 * proved to be a Portico server, then the person goes there, as they asked to. */
export function ServicesLocalServerPage({current,onBack}: {current: () => boolean; onBack: () => void}) {
  const [instance,setInstance]=useState<{live:boolean;controller?:AbortController}>();
  const [busy,setBusy]=useState(false),[error,setError]=useState<unknown>();
  useLayoutEffect(()=>{const lease={live:true,controller:undefined as AbortController|undefined};setInstance(lease);return()=>{lease.live=false;lease.controller?.abort();};},[current]);
  return <LocalServerAddress busy={busy} error={error} onBack={()=>{if(instance?.live&&current())onBack();}} onContinue={address=>{
    const lease=instance;if(!lease?.live||lease.controller||!current())return;
    const controller=new AbortController();lease.controller=controller;setBusy(true);setError(undefined);
    const live=()=>lease.live&&lease.controller===controller&&!controller.signal.aborted&&current();
    void servicesLocalOrigin(address,controller.signal).then(origin=>{if(live())location.assign(origin+'/');},error=>{if(live())setError(error);}).finally(()=>{if(live()){lease.controller=undefined;setBusy(false);}});
  }} />;
}
