import {installPlaybackEnvironment} from '../../../web/src/platform/playback/index.ts';
import {installSavedDownloadsBinder} from '../../../web/src/screens/saved/native-binding.ts';
import {nativeBridge} from './bridge';
import {DesktopMediaPlayer} from './player';
import {installWindowChromeHost,type WindowChromeInstallation} from '../../../web/src/platform/window-chrome.ts';
import {createDesktopNativeWindowHost} from './window-native-host.ts';
import {createCaptionControls} from './caption-controls';
import {nativeWindowBridge} from './window';
import {installSettingsPlatform} from '../../../web/src/platform/settings-platform.ts';
import {DesktopSettingsEqualizer,desktopEqualizerPort} from './SettingsEqualizer';
import {createDesktopDevicePort,nativeDeviceFacts} from './device';
import {nativeCapabilities} from './capabilities';
import {installPlatformInstallationProvider} from '../../../web/src/platform/installation-provider.ts';
import {nativeInstallation} from './installation';
import {createDesktopDownloadsHost} from './downloads-host.ts';
import {createDesktopAppLifetime} from './app-lifetime.ts';
import {installDesktopCloseHost} from './close-host.ts';
import {browserViewerStorage} from '../../../web/src/platform/viewer-selection-storage.ts';
import {installOfflineDownloads} from '../../../web/src/shell/offline-downloads.ts';
import {observeBrowserSessionChanges} from '../../../web/src/platform/server-connections.ts';
import {OfflineMediaHost} from '../../../web/src/shell/offline-media.ts';
import {installPlaybackAdoptionRetirement} from '../../../web/src/shell/playback-media-owner.ts';
import type {VideoLike, WebMediaPlayerOptions, WebLocalQueueResource} from '../../../web/src/platform/playback/index.ts';
import {offlineDownloadScope} from './offline-scope.ts';

const lifetime=createDesktopAppLifetime();
const close=()=>{void lifetime.dispose().catch(()=>console.warn('desktop_shutdown_failed'));};
const closeHost=installDesktopCloseHost({events:window,disposeApp:()=>lifetime.dispose(),acknowledge:()=>nativeWindowBridge.action('close'),onError:()=>console.warn('desktop_shutdown_failed')});
lifetime.retain(()=>closeHost.dispose());
async function startDesktop(){
  const native=await nativeWindowBridge.read();lifetime.assertCurrent();
  lifetime.retain(installPlatformInstallationProvider(nativeInstallation));
  const facts=await nativeDeviceFacts().catch(()=>undefined);lifetime.assertCurrent();
  const device=facts?createDesktopDevicePort(facts):undefined;if(device)lifetime.retain(()=>device.dispose());
  lifetime.retain(installSettingsPlatform({environment:{platform:'desktop',os:native.os},...(device?{device}:{}),Equalizer:DesktopSettingsEqualizer,equalizerPort:desktopEqualizerPort}));
  let windowChrome:WindowChromeInstallation|undefined;
  const windowHost=createDesktopNativeWindowHost({onEnvironment:value=>windowChrome?.updateEnvironment(value),captions:createCaptionControls,prepareToClose:()=>lifetime.dispose()});
  windowChrome=installWindowChromeHost(windowHost);windowChrome.updateEnvironment({os:native.os,fullscreen:native.fullscreen});
  lifetime.retain(()=>{windowChrome?.dispose();return windowHost.dispose();});
  const downloads=createDesktopDownloadsHost({reportFailure:()=>console.warn('downloads_unavailable')});
  lifetime.retain(installSavedDownloadsBinder(downloads.bindSaved));
  // The one native media engine. The launch page's local-only player is its exclusive
  // owner; adoption of the online one awaits that owner's actual retirement first.
  let offlineMedia:OfflineMediaHost|undefined;
  const createPlayer=(video:VideoLike,options:WebMediaPlayerOptions)=>new DesktopMediaPlayer(video,options,nativeBridge,true,downloads.broker);
  lifetime.retain(installPlaybackEnvironment({
    capabilities:()=>facts?.appVersion?nativeCapabilities(native.os,facts.appVersion):Promise.reject(new Error('engine_unavailable')),
    createPlayer,
    attachLocalQueue:downloads.attachLocalQueue,
    createRender:null,subtitleSurface:false,equalizerPort:desktopEqualizerPort,
  }));
  lifetime.retain(()=>downloads.dispose());
  // The launch page offers this device's completed downloads from the persisted,
  // validated scope; no server call, no player and no second downloads owner.
  const selected=await browserViewerStorage.read().catch(()=>null);lifetime.assertCurrent();
  const offlineScope=offlineDownloadScope(selected?.session,Date.now());
  const deadline=selected?Date.parse(selected.session.authorizationHorizon||selected.session.expiresAt):NaN;
  // The exact retained selection lease, not only its horizon: a different viewer, profile,
  // server or family must never inherit this list.
  const lease=selected&&offlineScope?JSON.stringify([offlineScope.authority,offlineScope.serverId,offlineScope.accountId,offlineScope.profileId,selected.session.sessionFamilyId,selected.session.tokenGeneration]):'';
  if(offlineScope&&lease){
    const port=await downloads.bindOfflineDownloads(offlineScope,lifetime.signal).catch(()=>undefined);
    if(port){
      lifetime.retain(()=>port.dispose());
      const listeners=new Set<()=>void>();
      let stopped=false,disposing:Promise<void>|undefined,timer:ReturnType<typeof setTimeout>|undefined;
      let snapshot:{phase:'loading'|'ready'|'unavailable';items:readonly {id:string;itemId:string;revision:number;quality:string;bytes:number;title:string;artist:string;kind:string;onServer:boolean}[]}={phase:'loading',items:[]};
      const unavailable=Object.freeze({phase:'unavailable' as const,items:Object.freeze([]) as readonly never[]});
      const publish=(next:typeof snapshot)=>{if(stopped)return;snapshot=next;for(const listener of [...listeners])listener();};
      const same=(record:Awaited<ReturnType<typeof browserViewerStorage.read>>)=>{const next=record&&offlineDownloadScope(record.session,Date.now());return !!next&&JSON.stringify([next.authority,next.serverId,next.accountId,next.profileId,record!.session.sessionFamilyId,record!.session.tokenGeneration])===lease;};
      const live=()=>!stopped&&!lifetime.signal.aborted&&Number.isFinite(deadline)&&deadline>Date.now();
      // The page's one visible surface. The local-only runtime is its only player; a new
      // owner of the native device awaits this host's actual retirement before mounting.
      const mediaLife=new AbortController();
      let media:OfflineMediaHost|undefined,surfaceToken=0;
      // Preparations this launch page owns until the approved runtime takes them. A late
      // preparation or a failed no-runtime release stays here and is retried by retirement.
      const preparations=new Map<WebLocalQueueResource,()=>Promise<void>>();
      const once=(run:()=>Promise<void>)=>{let task:Promise<void>|undefined;return()=>task??=run().catch(error=>{task=undefined;throw error;});};
      const retain=(resource:WebLocalQueueResource)=>{let release=preparations.get(resource);if(!release){release=once(()=>resource.release());preparations.set(resource,release);}return release;};
      const settlePreparation=async(resource:WebLocalQueueResource)=>{const release=preparations.get(resource);if(!release)return;await release();preparations.delete(resource);};
      let preparing=0,idle:Array<()=>void>=[];
      const quiescent=()=>preparing?new Promise<void>(resolve=>{idle.push(resolve);}):Promise.resolve();
      const finishPreparing=()=>{preparing--;if(!preparing){for(const resolve of idle.splice(0))resolve();}};
      const drainPreparations=async()=>{
        for(const resource of [...preparations.keys()])try{await settlePreparation(resource);}catch(error){console.warn('offline_playback_unavailable',error);}
        if(preparations.size>0)throw new Error('offline_release_pending');
      };
      const createMedia=()=>{
        const next=new OfflineMediaHost({
          scope:offlineScope,
          environment:{createPlayer},
          isCurrent:live,
          reportError:error=>console.warn('offline_playback_unavailable',error),
        });
        next.subscribe(()=>{for(const listener of [...listeners])listener();});
        media=next;
        offlineMedia=next;
        return next;
      };
      /** Stop offering rows at once, then retire the local player before releasing the
       * native store it uses. A failed retirement keeps that custody for a later retry. */
      const retire=():Promise<void>|undefined=>{
        if(disposing)return disposing;
        if(!stopped){stopped=true;mediaLife.abort();if(timer){clearTimeout(timer);timer=undefined;}snapshot=unavailable;for(const listener of [...listeners])listener();}
        disposing=(async()=>{
          try{
            // No preparation or handoff may be in flight while the device and store retire;
            // anything that never reached the runtime is released here before the store goes.
            await quiescent();
            await media?.adoptionRetirement();
            await drainPreparations();
          }
          catch(error){disposing=undefined;throw error;}
          port.dispose();
        })();
        void disposing.catch(error=>console.warn('offline_playback_unavailable',error));
        return disposing;
      };
      // Shutdown retires through the same barrier, so tracked preparations are drained
      // before the native store is released.
      lifetime.retain(()=>retire());
      // A new device owner adopts only after the FULL barrier: no preparation or handoff
      // in flight, the local player retired, every tracked resource drained, store released.
      lifetime.retain(installPlaybackAdoptionRetirement(async()=>{await retire();}));
      // One captured deadline clears the visible list even if nothing else renders or reads.
      if(Number.isFinite(deadline))timer=setTimeout(retire,Math.max(0,deadline-Date.now()));
      const leased=async()=>live()&&same(await browserViewerStorage.read().catch(()=>null));
      const refresh=async()=>{
        if(!await leased()){retire();return;}
        const items=await port.list(new AbortController().signal).catch(()=>undefined);
        if(items===undefined||!live()){retire();return;}
        // Re-check the exact lease after the list returns, before anything is published.
        if(!await leased()){retire();return;}
        publish(items.length?{phase:'ready',items}:{phase:'unavailable',items:[]});
      };
      void refresh();
      // The canonical AppSessionStore change hint (same tab, another tab, focus/pageshow) is
      // the one selection source: re-read the exact retained lease and retire on any mismatch.
      lifetime.retain(observeBrowserSessionChanges(()=>{
        if(!live())return;
        void leased().then(ok=>{if(!ok)retire();},()=>retire());
      }));
      lifetime.retain(installOfflineDownloads({
        subscribe:listener=>{listeners.add(listener);return()=>{listeners.delete(listener);};},
        snapshot:()=>{if(!live()){void Promise.resolve().then(retire);return unavailable;}return snapshot;},
        // The page hands its one visible element here; releasing it retires the local engine.
        // The same element is a no-op; only a real attach starts or replaces an owner.
        attachSurface:element=>{
          const token=++surfaceToken,current=media;
          if(!element){current?.attach(null);return;}
          if(current?.active){current.attach(element);return;}
          // The previous owner is still leaving; this element binds once it has.
          void (current?current.adoptionRetirement():Promise.resolve()).then(()=>{
            if(token!==surfaceToken||!live())return;
            createMedia().attach(element);
          },error=>console.warn('offline_playback_unavailable',error));
        },
        playback:()=>media?.snapshot()??Object.freeze({phase:'idle' as const}),
        /** Prepare the offered row and hand it to the local-only runtime, which takes
         * custody on entry and owns every cleanup path; this host adds none. */
        play:async id=>{
          const current=media;
          // Enter the retirement barrier before the first await: once retirement has started
          // no new preparation may begin, and one already begun is counted there.
          if(!current?.mounted||stopped){retire();throw new DOMException('Aborted','AbortError');}
          ++preparing;
          const action=new AbortController(),abort=()=>action.abort();
          mediaLife.signal.addEventListener('abort',abort,{once:true});
          try{
            if(!await leased()){retire();throw new DOMException('Aborted','AbortError');}
            const resource=await port.prepare(id,action.signal,()=>live());
            // Own it before retirement can drain, even if it arrived after retirement started.
            retain(resource);
            // The exact persisted lease again after preparation and before the runtime
            // takes it; a refusal leaves the preparation tracked for the barrier to release.
            if(stopped||!await leased()){void retire();throw new DOMException('Aborted','AbortError');}
            let handed=false;
            try{handed=await current.play(resource,{signal:action.signal,isCurrent:()=>live()&&!mediaLife.signal.aborted});}
            catch(error){preparations.delete(resource);throw error;}
            if(handed)preparations.delete(resource);else try{await settlePreparation(resource);}catch(error){console.warn('offline_playback_unavailable',error);throw error;}
            if(!await leased())retire();
          }finally{finishPreparing();mediaLife.signal.removeEventListener('abort',abort);}
        },
        openFolder:async()=>{
          if(!await leased()){retire();throw new DOMException('Aborted','AbortError');}
          await port.openFolder(new AbortController().signal);
          if(!await leased())retire();else void refresh();
        },
        retire,
      }));
    }
  }
  // The original app owns all screen/router/React assembly and captured viewers. The web
  // document entry only chooses between hosted Services pages and this app; desktop is never
  // the Services origin, so it loads the app module that owns the lifetime and disposeApp.
  await lifetime.importWebApp(()=>import('../../../web/src/app-main.tsx'));
  if(import.meta.env.DEV)void import('./dev-measurement.ts').then(module=>module.installDesktopMeasurement(lifetime.signal)).catch(()=>console.warn('desktop_measurement_unavailable'));
}
void startDesktop().catch(error=>{
  if(!lifetime.signal.aborted&&!(error instanceof DOMException&&error.name==='AbortError'))console.error('desktop_start_failed');
  close();
});
