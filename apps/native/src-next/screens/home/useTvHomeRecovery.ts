import {useCallback, useEffect, useRef, useState} from 'react';
import {AppState} from 'react-native';
import {presentError, type PresentedError} from '../../../../../packages/client-core/src/presentation/errors';
import {sameAuthority, authorityKey, type ShellAuthority} from '../../shell/model';
import type {NativeShellRuntime} from '../../shell/runtime';

/** Only these failures are worth another automatic try. Identity, permission, sign-in and
 * contract failures never retry by themselves. */
const transient = new Set(['offline', 'unreachable', 'timeout']);
/** client-core has no server-connection backoff to borrow: the Hosted gate is account-service
 * Retry-After handling and the quiet-refresh rule stops after three list tries. These waits are
 * bounded and keep going while the TV is eligible, so a server that stays away is met at once. */
const delays = [2_000, 5_000, 10_000, 20_000, 30_000] as const;

/** One recovery opening. Retired the moment its failure is over, replaced, or out of scope, so
 * a retained action can never reach a Home that has already loaded or failed differently. */
export type TvHomeRecoveryTicket = {retired: boolean};

export type TvHomeRecoverySession = Readonly<{
  ticket: TvHomeRecoveryTicket;
  presented: PresentedError;
  attempts: number;
  firstAt?: number;
  /** The scheduled attempt, in epoch ms, and the wait it was scheduled with. */
  dueAt?: number;
  delayMs: number;
}>;

/** The session lives at HomeScreen's stable scope, so a retry that clears the error and reads
 * for a moment cannot unmount it or restart the schedule. Each opening is retired during
 * render the instant its scope is replaced, before any retained callback can run. Every
 * dispatch needs that exact ticket, one flight is shared by manual, automatic and wake
 * attempts, and each dispatch awaits the service's own promise. */
export function useTvHomeRecovery({runtime, authority, error, active, resolved, enabled}: {
  runtime: NativeShellRuntime; authority: ShellAuthority; error: unknown; active: boolean; resolved: boolean; enabled: boolean;
}): Readonly<{visible: boolean; session?: TvHomeRecoverySession; retryNow: (ticket: TvHomeRecoveryTicket) => void; switchServer?: (ticket: TvHomeRecoveryTicket) => void}> {
  const [session, setSession] = useState<TvHomeRecoverySession>();
  const [pass, setPass] = useState(0), [reopen, setReopen] = useState(0);
  const [foreground, setForeground] = useState(AppState.currentState === 'active');
  const alive = useRef(true), flight = useRef<Readonly<{ticket: TvHomeRecoveryTicket; promise: Promise<void>}> | undefined>(undefined), waits = useRef(new Set<() => void>());
  const appState = useRef(AppState.currentState), wake = useRef(false), immediate = useRef(false), step = useRef(0);
  const opening = useRef<TvHomeRecoveryTicket | undefined>(undefined), presented = useRef<PresentedError | undefined>(undefined),
    sessionRef = useRef<TvHomeRecoverySession | undefined>(undefined);
  const activeRef = useRef(active);
  activeRef.current = active;
  sessionRef.current = session;
  // The exact scope this recovery belongs to. Initialised current, so mounting never retires
  // anything; a real change of authority, runtime, enabled or resolution retires in render.
  const scope = useRef({authority, runtime, enabled, resolved});
  const retireOpening = () => {
    if (opening.current) opening.current.retired = true;
    opening.current = undefined; presented.current = undefined;
    step.current = 0; immediate.current = false;
  };
  if (!sameAuthority(scope.current.authority, authority) || scope.current.runtime !== runtime || scope.current.enabled !== enabled || scope.current.resolved !== resolved) {
    scope.current = {authority, runtime, enabled, resolved};
    retireOpening();
  }
  // A failure that must not retry is terminal for this recovery, whether it replaced the
  // transient one or arrived later.
  if (opening.current && error !== undefined) {
    const category = presentError(error, 'home').category;
    if (!transient.has(category)) retireOpening();
  }
  const live = useCallback(() => {
    try {return alive.current && sameAuthority(authority, runtime.getSnapshot().authority);} catch {return false;}
  }, [authority, runtime]);
  const sleep = useCallback((ms: number) => new Promise<void>(resolve => {
    let done = false, id: ReturnType<typeof setTimeout>;
    const finish = () => {if (done) return; done = true; clearTimeout(id); waits.current.delete(finish); resolve();};
    id = setTimeout(finish, Math.max(0, ms));
    waits.current.add(finish);
  }), []);
  const cancelWaits = useCallback(() => {for (const finish of [...waits.current]) finish();}, []);
  // One dispatch for the captured ticket. It refuses unless that exact opening is still the
  // current, unsettled one, and checks the same conditions again after the await.
  const dispatch = useCallback((ticket: TvHomeRecoveryTicket): Promise<void> => {
    if (!enabled || ticket.retired || opening.current !== ticket) return Promise.resolve();
    if (!live() || !activeRef.current || !foreground) return Promise.resolve();
    const pending = flight.current;
    if (pending && pending.ticket === ticket) return pending.promise;
    const run = (async () => {
      setSession(current => current?.ticket === ticket ? {...current, attempts: current.attempts + 1, firstAt: current.firstAt ?? Date.now()} : current);
      try {await runtime.home.retry();} catch {if (!live()) return;}
    })();
    flight.current = {ticket, promise: run};
    void run.finally(() => {if (flight.current?.promise === run) flight.current = undefined;});
    return run;
  }, [enabled, foreground, live, runtime]);
  // The rendered session is the one whose ticket is still open. A retired opening leaves its
  // state behind inert: no action can use it, and a new transient failure replaces it.
  const authorityId = authorityKey(authority);
  // A transient failure opens one opening; a further error on the same opening only refreshes
  // its copy. Creation happens here, never inside a state updater.
  useEffect(() => {
    if (!enabled || resolved || error === undefined) return;
    const next = presentError(error, 'home');
    if (!transient.has(next.category)) return;
    presented.current = next;
    const current = opening.current;
    if (current && !current.retired) {setSession(value => value?.ticket === current ? {...value, presented: next} : value); return;}
    const ticket: TvHomeRecoveryTicket = {retired: false};
    opening.current = ticket; step.current = 0; immediate.current = false;
    setSession({ticket, presented: next, attempts: 0, delayMs: 0});
  }, [enabled, error, resolved, reopen, authorityId, runtime]);
  useEffect(() => {alive.current = true; return () => {
    // Retire and clear this opening before the StrictMode setup runs again, so the next setup
    // opens a fresh one instead of inheriting a dead ticket.
    alive.current = false;
    retireOpening();
    cancelWaits();
  };}, [cancelWaits]);
  // One wait at a time. A manual press or a wake cancels it, dispatches at once and restarts
  // the sequence, so waking resumes the schedule instead of falling silent.
  useEffect(() => {
    const current = sessionRef.current;
    if (!current || current.ticket.retired || resolved || !enabled || !active || !foreground) return;
    const ticket = current.ticket;
    let cancelled = false;
    void (async () => {
      const delay = immediate.current ? 0 : delays[Math.min(step.current, delays.length - 1)];
      immediate.current = false;
      if (cancelled) return;
      setSession(value => value?.ticket === ticket ? {...value, dueAt: Date.now() + delay, delayMs: delay} : value);
      await sleep(delay);
      if (cancelled || ticket.retired || opening.current !== ticket || !live()) return;
      setSession(value => value?.ticket === ticket ? {...value, dueAt: undefined} : value);
      await dispatch(ticket);
      if (cancelled || ticket.retired || opening.current !== ticket || !live()) return;
      step.current = Math.min(step.current + 1, delays.length - 1);
      setPass(value => value + 1);
    })();
    return () => {cancelled = true; cancelWaits();};
  }, [session?.ticket, pass, resolved, enabled, active, foreground, sleep, cancelWaits, dispatch, live]);
  // Waking retries at once and resumes the schedule. The setter stays pure; the ref holds the
  // previous state, and the wake flag is consumed by the effect below.
  useEffect(() => {
    const subscription = AppState.addEventListener('change', state => {
      const was = appState.current;
      appState.current = state;
      if (was !== 'active' && state === 'active') wake.current = true;
      setForeground(state === 'active');
    });
    return () => subscription.remove();
  }, []);
  useEffect(() => {
    if (!foreground || !wake.current) return;
    wake.current = false;
    const current = opening.current;
    if (!current || current.retired) return;
    step.current = 0; immediate.current = true;
    cancelWaits(); setPass(value => value + 1);
  }, [foreground, cancelWaits]);
  const retryNow = useCallback((ticket: TvHomeRecoveryTicket) => {
    if (!opening.current || opening.current !== ticket || ticket.retired) return;
    if (!live() || !activeRef.current || !foreground) return;
    step.current = 0; immediate.current = true;
    cancelWaits(); setPass(value => value + 1);
  }, [cancelWaits, foreground, live]);
  const switchServer = runtime.openServers ? (ticket: TvHomeRecoveryTicket) => {
    if (!opening.current || opening.current !== ticket || ticket.retired) return;
    if (!live() || !activeRef.current || !foreground) return;
    retireOpening(); setSession(undefined); cancelWaits(); setReopen(value => value + 1);
    runtime.openServers?.();
  } : undefined;
  const open = session && session.ticket === opening.current && !resolved && enabled ? session : undefined;
  return {visible: !!open && !!presented.current, session: open, retryNow, switchServer};
}
