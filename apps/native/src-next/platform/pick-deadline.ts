/** No step between a profile pick and Home waits without a bound (P2-74: an iPhone sat on Who's
 * watching for 8 minutes with no request open). The pick's server request, the identity check and
 * the durable save share one deadline; when it passes the person sees a timeout they can retry,
 * and one diagnostic line names the step that was still waiting. */
export const PICK_DEADLINE_MS = 20000;

export type PickTrace = {mark(step: string): void};

export function profilePickTimeout(step: string) {
  return Object.assign(new Error('profile_pick_timeout'), {code: 'timeout', retryable: true, step});
}

/** Runs `work` under the deadline. Its signal aborts on timeout or when `parent` aborts; work that
 * cannot be aborted (local storage) is left to finish, but the caller is released either way. */
export async function boundedPick<T>(work: (signal: AbortSignal, trace: PickTrace) => Promise<T>, parent: AbortSignal,
  ms = PICK_DEADLINE_MS, sink: (line: string) => void = line => console.warn(line)): Promise<T> {
  const controller = new AbortController(), started = Date.now(), marks: [string, number][] = [];
  let step = 'start';
  const trace: PickTrace = {mark(next) {step = next; marks.push([next, Date.now() - started]);}};
  const forward = () => controller.abort();
  if (parent.aborted) controller.abort(); else parent.addEventListener('abort', forward, {once: true});
  let timer: ReturnType<typeof setTimeout> | undefined;
  const deadline = new Promise<never>((_, reject) => {
    timer = setTimeout(() => {
      const waiting = step;
      // Step names and offsets only: no addresses, identifiers or credentials.
      try {sink(`portico_profile_pick_timeout ${JSON.stringify({waiting, ms, marks})}`);} catch { /* passive */ }
      controller.abort(); reject(profilePickTimeout(waiting));
    }, ms);
  });
  const running = work(controller.signal, trace);
  running.catch(() => {});
  try {return await Promise.race([running, deadline]);}
  finally {clearTimeout(timer); parent.removeEventListener('abort', forward);}
}
