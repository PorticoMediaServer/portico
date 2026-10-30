import {ReaderError} from './errors.ts';

/** iOS15.1 has AbortController, but not any/timeout/throwIfAborted or reason.
 * Keep compatibility inside the reader. Every operation releases its links;
 * no global constructors/prototypes or caller-owned signals are changed. */
export function abortReason(signal:AbortSignal):unknown {
  return signal.reason ?? new ReaderError('aborted');
}
/** Preserve a provider's AbortError/custom reason when the runtime has it. */
export function throwSignalAborted(signal:AbortSignal):void {
  if(signal.aborted)throw abortReason(signal);
}
export function abortScope(signals:readonly AbortSignal[]):Readonly<{signal:AbortSignal;dispose():void}> {
  const controller=new AbortController(),listeners=new Map<AbortSignal,()=>void>();
  const dispose=()=>{for(const [signal,listener]of listeners)signal.removeEventListener('abort',listener);listeners.clear();};
  for(const signal of new Set(signals)) {
    if(signal.aborted){dispose();controller.abort(abortReason(signal));break;}
    const listener=()=>{dispose();controller.abort(abortReason(signal));};
    listeners.set(signal,listener);signal.addEventListener('abort',listener,{once:true});
  }
  return {signal:controller.signal,dispose};
}
