/** Join only this original operation's signals, using the native AbortController
 * baseline. Every registration is released with its held HTTP continuation. */
export function startSignal(controller: AbortController, parents: readonly (AbortSignal | undefined)[]) {
  const abort = () => controller.abort();
  for (const parent of parents) {parent?.addEventListener('abort', abort, {once: true});if (parent?.aborted) abort();}
  return {signal: controller.signal, close() {for (const parent of parents) parent?.removeEventListener('abort', abort);}};
}
