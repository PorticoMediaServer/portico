import {supportZipSavePort} from '../../../platform/diagnostics-save';
import {useCallback,useEffect,useLayoutEffect,useState,useSyncExternalStore} from 'react';
import {type SupportComponent,fixOwnerSupportPermissions,OWNER_SUPPORT_DEFAULT_COMPONENTS,prepareOwnerSupportExport,readOwnerSupport,type OwnerSupportFacts,type OwnerSupportOptions} from '../../../../../../packages/client-core/src/server-admin/support-next';
import {skippedParts,resetSkippedParts,skippedVersion,subscribeSkipped} from '../../../../../../packages/client-core/src/tolerant';
import {defaultI18n} from '../../../../../../packages/i18n/src';
import type {NativeShellBootstrapContext} from '../../../shell/bootstrap';
import type {NativeShellRuntime} from '../../../shell/runtime';
import type {NativeSettingsLivePorts} from '../../../screens/settings-live/ports';
import {NativeScreenLifetime,screenScopeEnded} from '../../../screens/settings-live/lifetime';
import {nativeStorageOwner} from '../../../screens/settings-live/server/storage/space-source';

type Facts=Readonly<{phase:'loading'|'ready'|'error';facts?:OwnerSupportFacts}>;

/** The owner's Support reads and the export, through the page's own lifetime (rebuilt routes in client-core support-next.ts). */
function createSupport(context:NativeShellBootstrapContext){
  const lifetime=new NativeScreenLifetime(context),current=()=>lifetime.current()&&nativeStorageOwner(context);
  const api={request:<T,>(path:string,method?:string,body?:unknown,caller?:AbortSignal):Promise<T>=>current()?lifetime.request(signal=>context.api.request<T>(path,method,body,signal),caller):Promise.reject(screenScopeEnded())};
  const exportZip=async(options:OwnerSupportOptions)=>{
    if(!current())throw screenScopeEnded();
    const ready=await prepareOwnerSupportExport(context.api,options,lifetime.signal);
    if(!current())throw screenScopeEnded();
    return ready.blob;
  };
  return {context,api,current,exportZip,signal:lifetime.signal,dispose:()=>lifetime.dispose()};
}
export type Group=ReturnType<typeof createSupport>;
export function useNativeSupport(ports:NativeSettingsLivePorts,_runtime:NativeShellRuntime){
  const context=ports.context,[group,setGroup]=useState<Group>();
  useLayoutEffect(()=>{const next=createSupport(context);setGroup(next);return next.dispose;},[context]);
  return group?.context===context?group:undefined;
}

/** Settings › Server › Troubleshooting › Support on the phone: the data folder's permissions, what this server can do
 * (each check with the server's own sentence and path, wrapped), and the support export, a ZIP saved through the
 * system's save sheet. Definition 12 rules 74 and 76. */
export function useSupportControls({group}:{group:Group}){
  const t=defaultI18n.t;
  useSyncExternalStore(subscribeSkipped,skippedVersion,skippedVersion);
  const [savePort]=useState(()=>supportZipSavePort());
  const [state,setState]=useState<Facts>({phase:'loading'}),[fixing,setFixing]=useState(false),[fixed,setFixed]=useState(false);
  const [window,setWindow]=useState<number>(1440),[parts,setParts]=useState<readonly SupportComponent[]>(OWNER_SUPPORT_DEFAULT_COMPONENTS),[paths,setPaths]=useState(false);
  const [exporting,setExporting]=useState(false),[failed,setFailed]=useState(false);
  const load=useCallback(()=>{
    setState(before=>({...before,phase:'loading'}));
    void readOwnerSupport(group.api,group.signal).then(facts=>{if(group.current())setState({phase:'ready',facts});},()=>{if(group.current())setState(before=>({...before,phase:'error'}));});
  },[group]);
  useEffect(load,[load]);
  const fix=async()=>{
    if(fixing||!group.current())return;setFixing(true);
    try{await fixOwnerSupportPermissions(group.api,group.signal);if(group.current()){setFixed(true);load();}}
    catch{if(group.current())setState(before=>({...before,phase:'error'}));}
    finally{if(group.current())setFixing(false);}
  };
  // One action: the server makes the ZIP at once and nothing is kept there (rule 76); the save sheet takes it.
  const exportNow=async()=>{
    if(exporting||!savePort||!parts.length||!group.current())return;
    setExporting(true);setFailed(false);
    try{
      const zip=await group.exportZip({windowMinutes:window as OwnerSupportOptions['windowMinutes'],components:[...parts],includePaths:paths&&parts.includes('snapshot')});
      await savePort(zip,group.signal,group.current);
    }catch{if(group.current())setFailed(true);}
    finally{if(group.current()){setExporting(false);setPaths(false);}}
  };
  const permissions=state.facts?.permissions,skipped=skippedParts();
  const clearSkipped = () => {if (group.current()) resetSkippedParts();};
  const changePart = (component: SupportComponent, checked: boolean) => {
    setParts(current => checked ? [...new Set([...current, component])] : current.filter(part => part !== component));
    if (component === 'snapshot' && !checked) setPaths(false);
  };
  return {t, state, load, permissions, fixing, fixed, fix, skipped, clearSkipped, window, setWindow, parts, changePart, paths, setPaths, exporting, failed, savePort, exportNow};
}
