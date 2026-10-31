import {finishSetupExchange, setupExchangeCancelled, setupExchangeInvalid, type SetupExchanged, type SetupExchangePorts} from '@core/setup-exchange.ts';
import {isUnreadable, parseSetupAttempt, type SetupStore} from '@core/setup-store.ts';
import {installationIdentity} from '@core/installation.ts';
import {dispatchPorticoReturn, type CapturedPorticoReturn} from './portico-return-dispatch';
import {browserAppSessions, browserConnectionEnvironment} from './server-connections';
import {browserAttemptSlot, browserSetupStore} from './setup-store';

type Slot = ReturnType<typeof browserAttemptSlot>;
type Ports = SetupExchangePorts & Readonly<{
  attempt: Slot; store: (serverId: string) => SetupStore; installationId: () => string | undefined; pageOrigin: () => string;
}>;

/** Portico Services' Return for a Portico Account setup, in the tab that started it (B-DESIGN §6.2, web). Account entry
 * chooses it only when this tab holds a bound attempt; the callback must name that attempt's transaction, and
 * `finishSetupExchange` spends the attempt before any network I/O, so a callback can be used once. */
export class BrowserSetupReturn {
  private epoch = 0;
  constructor(private readonly ports: Ports) {}
  private now() {return (this.ports.now ?? Date.now)();}
  private async readAttempt() {
    const raw = await this.ports.attempt.read().catch(() => undefined);
    const attempt = raw === undefined ? undefined : parseSetupAttempt(raw);
    return attempt && !isUnreadable(attempt) ? attempt : undefined;
  }
  /** A read-only probe: never creates or consumes an attempt. */
  async pendingReturn() {
    this.ports.attempt.assertAvailable();
    const attempt = await this.readAttempt();
    if (!attempt?.handoff || attempt.origin !== this.ports.pageOrigin() || Date.parse(attempt.expiresAt) <= this.now()) return;
    return {kind: 'setup' as const, transactionId: attempt.handoff.transactionId};
  }
  /** This page no longer waits on a Return; the attempt stays for the setup page to read. */
  async cancel() {++this.epoch;}
  async finish(captured: CapturedPorticoReturn): Promise<SetupExchanged> {
    this.ports.attempt.assertAvailable();
    const epoch = ++this.epoch, controller = new AbortController(), signal = controller.signal;
    const current = () => {if (epoch !== this.epoch || signal.aborted) throw setupExchangeCancelled();};
    const attempt = await this.readAttempt();
    if (!attempt?.handoff) throw setupExchangeInvalid();
    const callback = dispatchPorticoReturn(captured, {kind: 'setup', transactionId: attempt.handoff.transactionId});
    const origin = this.ports.pageOrigin(), installation = this.ports.installationId();
    if (new URL(callback.href).origin !== attempt.origin || attempt.origin !== origin || installation !== attempt.installationId || Date.parse(attempt.expiresAt) <= this.now()) throw setupExchangeInvalid();
    return finishSetupExchange(this.ports, {attempt, store: this.ports.store(attempt.serverId), signal, current,
      admitted: () => epoch === this.epoch && !signal.aborted && attempt.origin === this.ports.pageOrigin() && attempt.installationId === this.ports.installationId()});
  }
}

export function createBrowserSetupReturn() {
  return new BrowserSetupReturn({attempt: browserAttemptSlot(), store: browserSetupStore, sessions: browserAppSessions(), environment: browserConnectionEnvironment,
    installationId: () => installationIdentity()?.installationId, pageOrigin: () => location.origin});
}
