import {NativeModules,Platform} from 'react-native';
import type {ClientReport} from '../../../../packages/client-core/src/client-reports';
import type {SupportExport} from '../../../../packages/client-core/src/console';
import {routeBase64} from '../../../../packages/client-core/src/route-identity';
import {nativeRouteCrypto} from './server-connections';
export type DiagnosticsKind='report'|'support';
const issued=new Set<string>();
let active:object|undefined;
const object=(value:unknown):value is Record<string,unknown>=>!!value&&typeof value==='object'&&!Array.isArray(value);
const retired=()=>Object.assign(new Error('diagnostics_save_retired'),{name:'AbortError'});
/** Fixed typed JSON-only OS sink. No caller URL, path, filename, MIME or upload. */
export function diagnosticsSavePort(){
  const sink:unknown=NativeModules.PorticoHighlightsSave;
  if(Platform.isTV||Platform.OS!=='android'&&Platform.OS!=='ios'||!object(sink))return;
  const save=sink.saveDiagnostics,cancel=sink.cancelDiagnostics;
  if(typeof save!=='function'||typeof cancel!=='function')return;
  return async(kind:DiagnosticsKind,document:ClientReport|SupportExport,signal:AbortSignal,isCurrent:()=>boolean):Promise<'saved'|'cancelled'>=>{
    const current=()=>!signal.aborted&&isCurrent();
    if(!current()||active||issued.size>=4096)throw retired();
    if(!['report','support'].includes(kind)||typeof document.id!=='string'||!document.id||document.id.length>160||kind==='report'&&(!('body' in document)||typeof document.body!=='string')||kind==='support'&&(!('manifest' in document)||!object(document.manifest)))throw new Error('invalid_diagnostics_document');
    // Freeze the actual checked document bytes before claiming native custody.
    const json=JSON.stringify(document);
    if(typeof json!=='string'||json.length>8*1024*1024||new TextEncoder().encode(json).byteLength>8*1024*1024)throw new Error('invalid_diagnostics_document');
    const id=routeBase64(nativeRouteCrypto.random(24));
    if(!/^[A-Za-z0-9_-]{32}$/.test(id)||issued.has(id)||!current()||active)throw retired();
    const attempt={};active=attempt;issued.add(id);
    const owns=()=>active===attempt&&current();
    const abort=()=>{try{cancel.call(sink,id);}catch{/* Retired UI never publishes a late native answer. */}};
    signal.addEventListener('abort',abort,{once:true});
    try{
      if(!owns())throw retired();
      const result:unknown=await save.call(sink,id,kind,json);
      if(!owns())throw retired();
      if(result!=='saved'&&result!=='cancelled')throw new Error('invalid_diagnostics_save_result');
      return result;
    }finally{signal.removeEventListener('abort',abort);abort();if(active===attempt)active=undefined;}
  };
}
