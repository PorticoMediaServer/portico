import type {ReaderContentsTarget} from '@portico/reader-core';
import type {ReadBindingContext} from './bindings';

// One transient intent within the captured workspace; never persisted or put in a public route.
const pending = new WeakMap<ReadBindingContext, Readonly<{itemId: string; sourceId: string; target: ReaderContentsTarget}>>();
export function stageContentsTarget(context: ReadBindingContext, itemId: string, sourceId: string, target?: ReaderContentsTarget) {
  pending.delete(context);
  if (context.isCurrent() && target) pending.set(context, {itemId, sourceId, target});
}
export function takeContentsTarget(context: ReadBindingContext, itemId: string, sourceId?: string) {
  const intent = pending.get(context);
  return context.isCurrent() && intent?.itemId === itemId && intent.sourceId === sourceId ? intent.target : undefined;
}

export function retireContentsTarget(context: ReadBindingContext, target?: ReaderContentsTarget) {
  if (pending.get(context)?.target === target) pending.delete(context);
}
