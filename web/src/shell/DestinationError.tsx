import {Component, useCallback, useLayoutEffect, useMemo, useRef, useState, type ErrorInfo, type ReactNode} from 'react';
import {useRouter, useRouterState, type ErrorComponentProps} from '@tanstack/react-router';
import {defaultI18n} from '@i18n';
import {StateLayout} from '../kit/states';
import styles from './states.module.css';
import {useWebScreenContext} from '../route-registry-view';
import {router} from '../router';

/**
 * The destination recovery witness: the actual Router location object, its href, the actual history
 * state entry, and the captured owner scope. The identity ALWAYS carries all four fields: the entry
 * and scope are truthful `null` when the history supplies no entry key or the shell is signed out,
 * including keyless destinations. Identity is the recovery lifetime — a new location object
 * (including a same-href fresh entry or a same-href keyless location), a real entry change or a scope
 * replacement creates a new witness; a missing entry key never collapses the href.
 */
export type DestinationRecoveryWitness = Readonly<{
  location: unknown;
  href: string;
  entry: string | null;
  scopeKey: string | null;
}>;

/** The actual history state entry for a location, or truthful null when the history supplies none. */
export const recoveryEntryOf = (location: {state: {__TSR_key?: unknown}}): string | null => {
  const entry = location.state.__TSR_key;
  return typeof entry === 'string' && entry.length ? entry : null;
};

/** Immutable render facts for one error page; nothing here is authority. */
type Facts = Readonly<{
  location: {state: {__TSR_key?: unknown}; href: string};
  href: string;
  entry: string | null;
  scopeKey: string | null;
  error: unknown;
  matchId?: string;
  routeId?: string;
  /** The original shell currentness predicate for this render, or undefined when there is none. */
  currentness?: () => boolean;
  /** The boundary reset; the installed client CatchBoundary supplies it, SSR's direct path omits it. */
  reset?: () => void;
}>;

/** The router match fields the failure witness compares; nothing here is authority. */
type MatchWitness = Readonly<{id: string; routeId: string; status: string; error: unknown}>;

/**
 * The captured failure witness must still be the router's actual one. A render or effect fault had
 * no stored match error (the router match was ready), so any stored error means a successor took
 * over. A stored error must still be the same child with the same original error before an action;
 * after this receipt's own invalidation only that child may have recovered, and any remaining error
 * — its own or another child's — refuses the completion.
 */
function childWitnessHolds(facts: Facts, matches: readonly MatchWitness[], afterInvalidate: boolean): boolean {
  const errors = matches.filter(match => match.status === 'error');
  if (facts.matchId === undefined) return errors.length === 0;
  const match = matches.find(candidate => candidate.id === facts.matchId);
  if (!match || match.routeId !== facts.routeId) return false;
  if (afterInvalidate) return errors.length === 0 && match.status !== 'error';
  // Before an action the captured child may still carry its original error, or may already have
  // recovered (then only the reset remains). Any other stored error is a successor and refuses.
  return errors.every(errorMatch => errorMatch === match) && (match.status !== 'error' || match.error === facts.error);
}

/** A layout-owned receipt: created fresh in layout setup, dead on cleanup. */
type Receipt = Readonly<{facts: Facts; live: {current: boolean}; pending: {current: boolean}}>;

/** The exact committed action closures of one rendered failure page. */
export type DestinationErrorActions = Readonly<{
  source: 'child' | 'backstop';
  retry: () => void;
  home: () => void;
}>;

/**
 * Development-only capture port for the Vite specimen. The presenter renders the real buttons, so
 * the specimen cannot otherwise hold the exact closures it rendered; re-appending a saved DOM button
 * is not that capture (React's delegated handler is updated or absent). The specimen installs this
 * hook before mounting and clears it on teardown. The call is behind `import.meta.env.DEV`, so a
 * production build never executes it, and no recovery decision reads it.
 */
export const destinationErrorActions: {capture?: (actions: DestinationErrorActions) => void} = {};

/** The approved page failure. Both actions are receipt-bound and refuse once their receipt is dead. */
function PageFailure({source, onRetry, onHome}: {source: 'child' | 'backstop'; onRetry: () => void; onHome: () => void}) {
  const t = defaultI18n.t;
  useLayoutEffect(() => {
    if (import.meta.env.DEV) destinationErrorActions.capture?.({source, retry: onRetry, home: onHome});
  }, [source, onRetry, onHome]);
  return <StateLayout className={styles.page} title={t('next.states.pageFailed')} body={t('next.states.pageFailedBody')} icon="i-warn" tone="warning"
    primary={{id: 'retry', label: t('next.states.tryAgain'), icon: 'i-retry', onPress: onRetry}}
    secondary={{id: 'home', label: t('next.states.goHome'), onPress: onHome}} />;
}

/**
 * One child destination's error page. Render produces only immutable facts; a layout effect creates
 * and publishes a fresh receipt for those facts (and a StrictMode setup replay gets its own), while
 * cleanup kills the exact receipt and clears its pending token. Handlers act only through the
 * published receipt: they re-check its liveness, the router's actual current location object, href
 * and entry key, and the original WebScreenContext currentness predicate before acting, then repeat
 * the same checks after that predicate and again after a deferred invalidation. A stored child
 * validation/load error is reloaded only through that receipt's own failed child match, then the
 * boundary reset is called when one was supplied: the installed client CatchBoundary always passes it,
 * and only SSR's direct-error path omits it, so the port stays optional.
 */
export function ChildRouteError({error, reset}: ErrorComponentProps) {
  const current = useRouter();
  const location = useRouterState({select: state => state.location});
  const matches = useRouterState({select: state => state.matches});
  const owner = useShellBindingsScope();
  const screen = useWebScreenContext();
  const currentness = screen.context?.isCurrent;
  const resetPort: (() => void) | undefined = typeof reset === 'function' ? reset : undefined;
  const failed = matches.filter(match => match.status === 'error' && match.error === error).at(-1);
  const facts = useMemo<Facts>(() => Object.freeze({
    location, href: location.href, entry: recoveryEntryOf(location), scopeKey: owner ?? null, error,
    matchId: failed?.id, routeId: failed?.routeId, currentness, reset: resetPort,
  }), [location, owner, error, failed?.id, failed?.routeId, currentness, resetPort]);
  const [published, setPublished] = useState<Receipt | undefined>(undefined);
  const live = useRef<Receipt | undefined>(undefined);
  useLayoutEffect(() => {
    const receipt: Receipt = {facts, live: {current: true}, pending: {current: false}};
    const receiptLive = receipt.live, receiptPending = receipt.pending;
    live.current = receipt;
    setPublished(receipt);
    return () => {
      // Dead and detached before its pending token is cleared: an old completion cannot publish.
      receiptLive.current = false;
      if (live.current === receipt) live.current = undefined;
      receiptPending.current = false;
    };
  }, [facts]);
  const held = published?.facts === facts ? published : undefined;
  const holds = useCallback((receipt: Receipt, afterInvalidate = false) => {
    if (!receipt.live.current || live.current !== receipt) return false;
    const now = current.state.location;
    if (now !== receipt.facts.location || now.href !== receipt.facts.href || recoveryEntryOf(now) !== receipt.facts.entry) return false;
    if (!childWitnessHolds(receipt.facts, current.state.matches, afterInvalidate)) return false;
    if (receipt.facts.currentness && !receipt.facts.currentness()) return false;
    // Repeat the exact receipt and child checks after the currentness predicate.
    if (!receipt.live.current || live.current !== receipt) return false;
    const again = current.state.location;
    if (again !== receipt.facts.location || again.href !== receipt.facts.href || recoveryEntryOf(again) !== receipt.facts.entry) return false;
    return childWitnessHolds(receipt.facts, current.state.matches, afterInvalidate);
  }, [current]);
  const retry = useCallback(() => {
    if (!held || !holds(held) || held.pending.current) return;
    if (held.facts.matchId === undefined) { held.facts.reset?.(); return; }
    held.pending.current = true;
    void current.invalidate({filter: match => match.id === held.facts.matchId, sync: true}).then(() => {
      if (!holds(held, true)) return;
      held.facts.reset?.();
    }).catch(() => { /* The child stays on its bounded page. */ }).finally(() => { held.pending.current = false; });
  }, [held, holds, current]);
  const home = useCallback(() => {
    if (!held || !holds(held)) return;
    void current.navigate({to: '/'}).catch(() => { /* The shell reports navigation failure. */ });
  }, [held, holds, current]);
  return <PageFailure source="child" onRetry={retry} onHome={home} />;
}

/** The captured owner scope of the current shell binding, or undefined when signed out. */
function useShellBindingsScope(): string | undefined {
  const screen = useWebScreenContext();
  return screen.binding?.scopeKey;
}

/**
 * The root route's error component: the app itself failed, so the actual error is rethrown rather
 * than drawn as a page failure. With `disableGlobalCatchBoundary` the throw leaves the router and
 * reaches the existing App last resort; its text is never logged or displayed here.
 */
export function RootErrorRethrow({error}: ErrorComponentProps): ReactNode {
  throw error;
}

export type DestinationErrorProps = Readonly<{
  witness: DestinationRecoveryWitness;
  children: ReactNode;
  /** Owner/test port for Home; the real shell passes its own navigation. */
  onHome?: () => void;
}>;

type DestinationErrorState = Readonly<{failed: boolean; failure?: {current: boolean}}>;

/**
 * The outlet backstop for a failure in the route content the boundary wraps (a child destination
 * has its own error component and catches first). Each failed render captures that render's own
 * witness, failure token and Home port in its closures; a saved old callback compares its captured
 * witness and failure before acting, so a successor fault cannot be reset or navigated by it. A real
 * unmount synchronously retires the mounted opening, and StrictMode's class mount replay gets a fresh
 * admission, so no closure captured before either can act.
 */
export class DestinationErrorBoundary extends Component<DestinationErrorProps, DestinationErrorState> {
  state: DestinationErrorState = {failed: false};
  /** Mount liveness: false before mount and after unmount, including StrictMode's replay gap. */
  private mounted = false;
  static getDerivedStateFromError(): DestinationErrorState { return {failed: true, failure: {current: true}}; }
  componentDidMount() {
    this.mounted = true;
    // StrictMode's simulated unmount retires the first mount's token. The replay gets its own fresh
    // admission here, so the committed page is live while every closure captured before it stays dead.
    if (this.state.failed && this.state.failure?.current !== true) this.setState({failed: true, failure: {current: true}});
  }
  componentWillUnmount() {
    // Synchronously retire the mounted opening. Props and state stay equal after unmount, so a saved
    // original Home or Retry must be refused by the mount flag and its dead failure token.
    this.mounted = false;
    if (this.state.failure) this.state.failure.current = false;
  }
  componentDidCatch(_error: Error, _info: ErrorInfo) { /* The presenter is the report; nothing else is replaced. */ }
  componentDidUpdate(previous: DestinationErrorProps) {
    // A new witness is a new recovery lifetime: a same-href fresh entry, an actual entry change or
    // a scope replacement must not leave the failed destination latched.
    if (previous.witness !== this.props.witness && this.state.failed) this.setState({failed: false});
  }
  render() {
    if (!this.state.failed) return this.props.children;
    const witness = this.props.witness, failure = this.state.failure, onHome = this.props.onHome;
    const current = () => this.mounted && this.state.failed && this.props.witness === witness && this.state.failure === failure && failure?.current === true;
    return <PageFailure source="backstop"
      onRetry={() => { if (!current()) return; if (failure) failure.current = false; this.setState({failed: false}); }}
      onHome={() => {
        if (!current()) return;
        if (onHome) { onHome(); return; }
        void router.navigate({to: '/'}).catch(() => { /* The existing shell reports navigation failure. */ });
      }} />;
  }
}
