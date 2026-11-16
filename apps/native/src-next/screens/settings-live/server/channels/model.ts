import {withRequestRaw} from '../../../../../../../packages/client-core/src/home-selection/list-destinations';
import {OwnerChannelSource} from './source';
import type {NativeShellBootstrapContext} from '../../../../shell/bootstrap';
import type {NativeShellRuntime} from '../../../../shell/runtime';
import {nativeRouteCrypto} from '../../../../platform/server-connections';
import {NativeScreenLifetime, screenScopeEnded} from '../../lifetime';
import {nativeStorageOwner} from '../storage/space-source';
// Retained for the Support tab on this base.
export const nativeChannelOperationId=()=>Array.from(nativeRouteCrypto.random(24),byte=>byte.toString(16).padStart(2,'0')).join('');
/** The page and its sheets share one admitted owner lifetime. */
export function createNativeChannels(context:NativeShellBootstrapContext,reportError:NativeShellRuntime['reportError']) {
 const lifetime=new NativeScreenLifetime(context),current=()=>lifetime.current()&&nativeStorageOwner(context);
 const reads={request:<T,>(path:string,method?:string,body?:unknown,caller?:AbortSignal):Promise<T>=>current()?lifetime.request(signal=>context.api.request<T>(path,method,body,signal),caller):Promise.reject(screenScopeEnded())};
 const api=withRequestRaw(reads,context.api.requestRaw?.bind(context.api),()=>{if(!current())throw screenScopeEnded();});
 const source=new OwnerChannelSource(api,current,reportError);
 const off=()=>source.dispose();lifetime.signal.addEventListener('abort',off,{once:true});
 const libraries=context.libraries().filter(library=>['movie','tv','anime'].includes(library.kind));
 const lookup={api,current,report:reportError,get moodLabels(){return source.getSnapshot().choices?.moodLabels;},get moodAvailable(){return source.getSnapshot().choices?.moodAvailable??false;}};
 return {context,source,lookup,libraries,current,dispose:()=>{lifetime.signal.removeEventListener('abort',off);source.dispose();lifetime.dispose();}};
}
export type NativeChannelsGroup=ReturnType<typeof createNativeChannels>;
