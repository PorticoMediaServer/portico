import type {DownloadPreparation} from '../../../packages/client-core/src/downloads.ts';
import {DesktopOfflineCoordinator,type DesktopOfflineTransport} from './offline.ts';
import {DesktopNetworkMonitor} from './network.ts';
import {downloadFailure} from './downloads.ts';

export type DesktopOfflineSession=Readonly<{
  saveReadyPreparation(preparation:DownloadPreparation,signal:AbortSignal):Promise<void>;
  /** Actual OS network/focus/retry/rules-change wake; never a second server list. */
  wake():void;
  dispose():void;
}>;

/** The parent supplies the same captured authenticated API/store used by Saved. This
 * owns device facts and native persisted-queue wakes, never preparation polling. The
 * caller retains the store lease for its local UI port and disposes that separately. */
export function createDesktopOfflineSession(
  transport:Omit<DesktopOfflineTransport,'networkCost'|'subscribeNetworkCost'>,
  signal:AbortSignal,
  reportFailure:(error:Error)=>void,
  network:DesktopNetworkMonitor=new DesktopNetworkMonitor(),
):DesktopOfflineSession {
  const coordinator=new DesktopOfflineCoordinator({...transport,networkCost:network.current,subscribeNetworkCost:network.subscribe});
  const lifetime=new AbortController();
  let disposed=false,running=false,requested=false;
  const wake=()=>{
    if(disposed)return;requested=true;if(running)return;running=true;
    void (async()=>{
      try{
        do{requested=false;await coordinator.resumeQueued(lifetime.signal);}while(requested&&!disposed);
      }catch(error){if(!disposed&&!lifetime.signal.aborted)reportFailure(downloadFailure(error));}
      finally{running=false;}
    })();
  };
  const off=network.subscribe(wake);
  const dispose=()=>{
    if(disposed)return;disposed=true;lifetime.abort();off();network.dispose();coordinator.dispose();signal.removeEventListener('abort',dispose);
  };
  signal.addEventListener('abort',dispose,{once:true});
  if(signal.aborted)dispose();else{network.start();wake();}
  return Object.freeze({saveReadyPreparation:(preparation,signal)=>coordinator.saveReadyPreparation(preparation,signal),wake,dispose});
}
