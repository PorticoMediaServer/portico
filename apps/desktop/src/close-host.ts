/** OS close reaches the original app disposer before native teardown. Listener
 * removal is synchronous: the active close may itself be awaiting that disposer. */
export function installDesktopCloseHost({events,disposeApp,acknowledge,onError}:{events:Pick<EventTarget,'addEventListener'|'removeEventListener'>;disposeApp:()=>Promise<void>;acknowledge:()=>Promise<void>;onError:()=>void}){
  let retired=false,closing:Promise<void>|undefined;
  const nativeClose=()=>{
    if(retired||closing)return;
    closing=(async()=>{await disposeApp();await acknowledge();})().catch(onError);
  };
  const pagehide=()=>{if(!retired)void disposeApp().catch(onError);};
  events.addEventListener('portico-native-close',nativeClose);
  events.addEventListener('pagehide',pagehide);
  return Object.freeze({dispose(){if(retired)return;retired=true;events.removeEventListener('portico-native-close',nativeClose);events.removeEventListener('pagehide',pagehide);}});
}
