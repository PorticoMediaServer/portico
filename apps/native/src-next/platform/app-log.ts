import {AppState, NativeModules, Platform} from 'react-native';
import {appLog} from '../../../../packages/client-core/src/app-log';
import {systemScheduler} from '../../../../packages/client-core/src/timer';

let generation = 0, installed = false, timer: unknown;
let ready: Promise<void> = Promise.resolve(), tail: Promise<unknown> = Promise.resolve();
const runtime = () => NativeModules.PorticoRuntime;
/** Flushes also on background/crash. Writes are serialized, so Clear cannot be undone by an older write. */
export function flushNativeAppLog(): Promise<void> {
 if (timer !== undefined) {systemScheduler.clearTimeout(timer);timer=undefined;}
 tail = tail.catch(()=>{}).then(async()=>{await ready; await runtime().saveAppLog(appLog.snapshot() || null);});
 return tail.then(()=>{},()=>{});
}
export function clearNativeAppLog(): Promise<void> {
 generation++; appLog.clear();
 const crash = Promise.resolve().then(()=>runtime().saveQueueRecord('report.fatal:v1','null')).catch(()=>{});
 return Promise.all([flushNativeAppLog(),crash]).then(()=>{});
}
export function installNativeAppLog() {
 if (installed) return; installed=true;
 const opened = generation;
 ready = Promise.resolve().then(()=>runtime().loadAppLog()).then(raw=>{if (opened === generation && typeof raw === 'string') appLog.restore(raw);}).catch(()=>{});
 appLog.subscribe(()=>{if (timer === undefined) timer=systemScheduler.setTimeout(()=>{timer=undefined;void flushNativeAppLog();},1000);});
 AppState.addEventListener('change',state=>{if(state !== 'active') void flushNativeAppLog();});
 void ready.then(()=>flushNativeAppLog());
 appLog.write('info','app','started',{osVersion:String(Platform.Version)});
 try {void Promise.resolve(NativeModules.PorticoCapabilities?.facts?.()).then(value=>{if (opened === generation) appLog.write('info','app','started',{appVersion:value?.appVersion});},()=>{});} catch { /* facts are optional */ }
}
