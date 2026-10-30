import {parseEqualizer,type EqualizerSettings} from '../../../web/src/platform/playback/audio/equalizer.ts';
import type {EqualizerPort} from '../../../web/src/kit/media/equalizer-port.ts';
import {desktopEqualizer,type EqualizerSnapshot} from './equalizer.ts';

type NativeEqualizer = typeof desktopEqualizer;

/** One app/device store for both existing EQ entry points. Only a committed native
 * acknowledgement changes the UI value; a conflict reloads the actual store. */
export function createDesktopEqualizerPort(reportError:(error:unknown)=>void,native:NativeEqualizer=desktopEqualizer):EqualizerPort {
  let saved:EqualizerSnapshot|undefined,active=0,epoch=0,writing=0;
  let serial:Promise<void>=Promise.resolve();
  const listeners=new Set<()=>void>();
  function publish(snapshot:EqualizerSnapshot) {
    if(saved&&snapshot.revision<saved.revision)return;
    saved=snapshot;
    if(active)for(const listener of listeners)listener();
  }
  async function read(expected=epoch) {
    const snapshot=await native.read();
    if(active&&expected===epoch&&!writing)publish(snapshot);
    return snapshot;
  }
  function change(commit:(revision:number)=>Promise<EqualizerSnapshot>):Promise<void> {
    ++epoch;++writing;
    const task=serial.then(async()=>{
      // Read the native revision for the transaction, including writes made through
      // the other EQ entry point. Never synthesize a revision or retry over a conflict.
      try {
        const before=await native.read();
        const acknowledged=await commit(before.revision);
        publish(acknowledged);
      } catch(error) {
        try{publish(await native.read());}catch(readError){if(active)reportError(readError);}
        throw error;
      } finally {--writing;}
    });
    serial=task.catch(()=>{});
    return task;
  }
  return Object.freeze({
    getSnapshot:()=>saved?.settings??null,
    subscribe(listener:()=>void){listeners.add(listener);return()=>{listeners.delete(listener);};},
    start(){
      if(++active===1){const current=++epoch;void read(current).catch(error=>{if(active&&epoch===current)reportError(error);});}
      let ended=false;
      return()=>{if(ended)return;ended=true;if(--active===0)++epoch;};
    },
    change(value:EqualizerSettings){const settings=parseEqualizer(value);return change(revision=>native.set(revision,settings));},
    reset(){return change(revision=>native.resetToFlat(revision));},
  });
}
