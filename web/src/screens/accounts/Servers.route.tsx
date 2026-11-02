import {useCallback, useEffect, useRef, useState, useSyncExternalStore} from 'react';
import type {AppSessionRecord, SignInKind} from '@core/app-sessions.ts';
import {presentError} from '@core/presentation/errors.ts';
import {defaultI18n} from '@i18n';
import {defineWebScreen} from '../../route-registry';
import {router} from '../../router';
import {createBrowserAccountEntry, type BrowserAccountEntry} from '../../platform/account-entry';
import {browserAccount} from '../../platform/account';
import {browserAccountOrigin} from '../../platform/account-origin';
import {createAccountServerDirectory} from '../../platform/account-server-directory';
import {accountIdentity} from '../../platform/account-work';
import {browserAppSessions, observeBrowserSessionChanges} from '../../platform/server-connections';
import {browserAppSessionActions, signOutBrowserPorticoAccount} from '../../platform/app-sessions';
import {browserServerDisplay} from '../../platform/server-display';
import {ColourBackdrop} from '../../kit/artwork';
import {Button} from '../../kit/controls';
import {Icon} from '../../kit/Icon';
import {ProfileAvatarFace} from '../../kit/shell/AccountSurfaces';
import {CountdownRing} from '../../kit/states';
import {LocalServerAddress, ServerSwitcher} from '../../kit/accounts/ServerSwitcher';
import wordmark from '../../shell/wordmark.svg';
import {AccountSignInView} from './AccountSignInView';
import {HostedServersScreen} from './HostedServers';
import {currentWebEnvironment} from '../../platform/environment';
import {AccountSignInForm} from './sign-in-form';
import {savedServerChoices, type SavedServerChoice} from './saved-server-choices';
import styles from './mweb-chooser.module.css';

const t = defaultI18n.t;

/** The frame's saved-server facts. Presence and ownership are optional display evidence:
 * the saved-session runtime knows the name and sign-in kind, never the account's last-seen
 * or owner facts. The DEV specimen supplies the drawn facts through this same typed row. */
export type MobileChooserRow = Readonly<{
  id: string;
  label: string;
  signIn: SignInKind;
  selected: boolean;
  status?: Readonly<{kind: 'online'}> | Readonly<{kind: 'seen'; at: string}> | Readonly<{kind: 'never'}>;
  owner?: boolean;
  administrator?: boolean;
}>;
/** The directory facts the chooser can display; the account service owns their truth. */
export type DirectoryFacts = Readonly<{
  id: string; name: string; status: string; role: string;
  presence: Readonly<{lastSeenAt: string | null}>;
  ownedByMe: boolean;
}>;
/** Genuine account-directory facts enrich saved rows. A saved row with no directory
 * entry keeps only its saved facts; a sign-in kind never stands in for presence. */
export function mobileChooserRows(choices: readonly SavedServerChoice[], directory: readonly DirectoryFacts[]): readonly MobileChooserRow[] {
  const byId = new Map(directory.map(item => [item.id, item]));
  return choices.map(choice => {
    const item = byId.get(choice.id);
    if (!item) return choice;
    const status: NonNullable<MobileChooserRow['status']> = item.status === 'active' ? {kind: 'online'}
      : item.presence.lastSeenAt ? {kind: 'seen', at: item.presence.lastSeenAt} : {kind: 'never'};
    return {...choice, label: item.name.trim() || choice.label, status, ...(item.ownedByMe ? {owner: true} : item.role === 'admin' ? {administrator: true} : {})};
  });
}
export type MobileChooserAttempt =
  | Readonly<{kind: 'loading'}>
  | Readonly<{kind: 'opening'; row: MobileChooserRow}>
  | Readonly<{kind: 'waiting'; row: MobileChooserRow; seconds: number; total: number}>
  | Readonly<{kind: 'unreachable'; row: MobileChooserRow}>
  | Readonly<{kind: 'refused'; row: MobileChooserRow}>
  | Readonly<{kind: 'unconfirmed'; row: MobileChooserRow}>
  | Readonly<{kind: 'loadFailed'}>;
export type MobileChooserProps = Readonly<{
  rows: readonly MobileChooserRow[];
  initial?: string;
  attempt?: MobileChooserAttempt;
  busy?: boolean;
  notice?: string;
  /** The frame was drawn with day-month dates; production keeps the app's locale formatter. */
  formatSeen?: (iso: string) => string;
  onChoose: (row: MobileChooserRow) => void;
  onRefresh: () => void;
  onLocal: () => void;
  onDifferentAccount?: () => void;
  onCheckNow: () => void;
  onOther: () => void;
}>;

const formatSeenDate = (iso: string) => defaultI18n.date(iso, 'dayMonth');
function statusText(row: MobileChooserRow, formatSeen: (iso: string) => string): Readonly<{text: string; online?: boolean}> | undefined {
  if (!row.status) return undefined;
  if (row.status.kind === 'online') return {text: t('web.servers.online'), online: true};
  if (row.status.kind === 'never') return {text: t('web.chooser.neverSeen')};
  return {text: t('web.chooser.lastSeen', {date: formatSeen(row.status.at)})};
}
/** One work lock per chooser attempt. A cancelled attempt may never settle a successor,
 * and a late settle may never leave the chooser disabled after the person chose another. */
export class AttemptLock {
  private generation = 0;
  private active?: number;
  begin() { const token = ++this.generation; this.active = token; return token; }
  cancel() { ++this.generation; this.active = undefined; }
  owns(token: number) { return this.active === token; }
  settle(token: number) { if (!this.owns(token)) return false; this.active = undefined; return true; }
  get running() { return this.active !== undefined; }
}
export type WaitingStep =
  | Readonly<{kind: 'hold'}>
  | Readonly<{kind: 'tick'; seconds: number}>
  | Readonly<{kind: 'retry'}>
  | Readonly<{kind: 'rearm'}>;
/** Hidden tabs never poll or retry; becoming visible re-arms a positive countdown. */
export function waitingStep(attempt: MobileChooserAttempt | undefined, event: 'timer' | 'visibility', visible: boolean): WaitingStep {
  if (attempt?.kind !== 'waiting' || !visible) return {kind: 'hold'};
  if (event === 'visibility') return attempt.seconds > 0 ? {kind: 'rearm'} : {kind: 'retry'};
  return attempt.seconds > 0 ? {kind: 'tick', seconds: attempt.seconds - 1} : {kind: 'retry'};
}
function unreachableText(row: MobileChooserRow, formatSeen: (iso: string) => string): string {
  if (row.status?.kind === 'seen') return t('web.chooser.unreachableBody', {date: formatSeen(row.status.at)});
  if (row.status?.kind === 'never') return t('web.chooser.unreachableBodyNever');
  // The saved-session runtime has no presence clock; the honest sentence names the server.
  return t('web.signIn.error.unreachable', {server: row.label});
}
function ChooserHeader({initial}: {initial?: string}) {
  return <header className={styles.header}>
    <img src={wordmark} alt={t('accounts.brand')} />
    {initial && <span className={styles.avatar}><ProfileAvatarFace initial={initial} size={30} /></span>}
  </header>;
}
/** connect.html frame 6 at the authored mobile-web viewport: the list, opening, waiting,
 * can't-reach, loading and list-failed states. It has no browser chrome of its own. */
export function MobileServerChooser({rows, initial, attempt, busy = false, notice, formatSeen = formatSeenDate, onChoose, onRefresh, onLocal, onDifferentAccount, onCheckNow, onOther}: MobileChooserProps) {
  const showPortico = rows.some(row => row.signIn === 'portico');
  if (attempt) {
    const state = attempt.kind === 'loading' ? 'loading' : attempt.kind === 'loadFailed' ? 'loadFailed' : attempt.kind === 'opening' ? 'opening' : attempt.kind;
    const row = 'row' in attempt ? attempt.row : undefined;
    const title = state === 'loading' ? t('web.chooser.loading') : state === 'loadFailed' ? t('web.chooser.loadFailed')
      : state === 'opening' ? t('web.chooser.connecting', {server: row!.label})
      : state === 'waiting' ? t('web.chooser.waitingTitle', {server: row!.label})
      : state === 'refused' ? t('web.chooser.refusedTitle', {server: row!.label})
      : state === 'unconfirmed' ? t('web.chooser.unconfirmedTitle', {server: row!.label})
      : t('web.chooser.unreachableTitle', {server: row!.label});
    const body = state === 'loadFailed' ? notice : state === 'waiting' ? t('web.chooser.waitingBody')
      : state === 'refused' ? t('web.chooser.refusedBody') : state === 'unconfirmed' ? t('web.chooser.unconfirmedBody')
      : state === 'unreachable' ? unreachableText(row!, formatSeen) : undefined;
    const primaryLabel = state === 'waiting' ? t('web.chooser.checkNow') : state === 'unreachable' || state === 'unconfirmed' ? t('web.chooser.tryAgain') : state === 'loadFailed' ? t('web.chooser.tryAgain') : undefined;
    const onPrimary = state === 'loadFailed' ? onRefresh : onCheckNow;
    return <section className={styles.page} data-mweb-server-chooser data-state={state}>
      <ColourBackdrop />
      <ChooserHeader initial={initial} />
      <div className={styles.state}>
        <div className={styles.stateIcon} data-accent={state === 'opening' || state === 'loading' || undefined} data-bad={state === 'loadFailed' || state === 'refused' || undefined}>
          {state === 'opening' || state === 'loading' ? <span className={styles.spinner} aria-hidden="true" /> : <Icon name={state === 'loadFailed' ? 'i-warn' : state === 'refused' ? 'i-userx' : state === 'unconfirmed' ? 'i-question' : 'i-srvoff'} size={34} />}
        </div>
        <h2>{title}</h2>
        {body && <p>{body}</p>}
        {state === 'waiting' && <div className={styles.alive}><CountdownRing progress={attempt.kind === 'waiting' ? attempt.seconds / Math.max(1, attempt.total) : null} /><span>{t('web.chooser.checkingAgain', {seconds: attempt.kind === 'waiting' ? attempt.seconds : 0})}</span></div>}
        <div className={styles.stateActions}>
          {state === 'refused' ? <>
            <Button variant="primary" className={styles.stateButton} onClick={onOther}>{t('web.chooser.otherServer')}</Button>
            <Button variant="glass" className={styles.stateButton} onClick={onRefresh}>{t('accounts.servers.refresh')}</Button>
          </> : <>
            {primaryLabel && <Button variant="primary" className={styles.stateButton} icon={state === 'unreachable' || state === 'unconfirmed' || state === 'loadFailed' ? 'i-retry' : undefined} onClick={onPrimary}>{primaryLabel}</Button>}
            {state === 'waiting' || state === 'unreachable' || state === 'unconfirmed' ? <Button variant="glass" className={styles.stateButton} onClick={onOther}>{t('web.chooser.otherServer')}</Button>
              : state === 'opening' ? <Button variant="text" className={styles.otherLink} onClick={onOther}>{t('web.chooser.otherServer')}</Button> : null}
          </>}
        </div>
      </div>
    </section>;
  }
  const presented = notice;
  return <section className={styles.page} data-mweb-server-chooser>
    <ColourBackdrop />
    <ChooserHeader initial={initial} />
    <div className={styles.content}>
      <h1>{t('portico.landing.pickTitle')}</h1>
      <p className={styles.lede}>{t('web.chooser.lede')}</p>
      {presented && <p className={styles.notice} role="alert"><Icon name="i-warn" size={20} />{presented}</p>}
      <div className={styles.group} aria-busy={busy || undefined}>
        {rows.map(row => {
          const status = statusText(row, formatSeen);
          return <button key={row.id} type="button" className={styles.row} data-row={row.id} data-off={row.status && row.status.kind !== 'online' || undefined}
            disabled={busy} onClick={() => onChoose(row)}>
            <span className={styles.rowIcon} data-online={status?.online || undefined}><Icon name="i-server" size={18} /></span>
            <span className={styles.rowText}>{row.label}{status && <small data-online={status.online || undefined}>{status.text}</small>}</span>
            {row.owner ? <span className={styles.owner}>{t('web.hostedAccount.serverOwner')}</span>
              : row.administrator ? <span className={styles.owner}>{t('web.access.roleAdmin')}</span> : null}
            <Icon name="i-right" size={18} />
          </button>;
        })}
      </div>
      <Button variant="glass" icon="i-refresh" iconSize={18} className={styles.refresh} loading={busy} onClick={onRefresh} disabled={busy}>{t('accounts.servers.refresh')}</Button>
      <div className={styles.links}>
        {showPortico && onDifferentAccount && <button type="button" disabled={busy} onClick={onDifferentAccount}>{t('accounts.move.differentAccount')}</button>}
        <button type="button" disabled={busy} onClick={onLocal}>{t('accounts.signIn.local')}</button>
      </div>
    </div>
  </section>;
}

function useNarrowViewport() {
  const subscribe = useCallback((listener: () => void) => {
    const query = matchMedia('(max-width: 700px)');
    query.addEventListener('change', listener);
    return () => query.removeEventListener('change', listener);
  }, []);
  return useSyncExternalStore(subscribe, () => matchMedia('(max-width: 700px)').matches, () => false);
}
function initialOf(name: string | undefined) {
  const trimmed = name?.trim(), first = trimmed ? Array.from(trimmed)[0] : undefined;
  return first?.toLocaleUpperCase();
}
export {resumeAfterProfiles, chooserFailureState, type ChooserFailureState, type ProfileResume} from './chooser-resume';
import {resumeAfterProfiles, chooserFailureState} from './chooser-resume';

/** At web.getportico.tv the chooser is the Portico Account's own server list (P2-66);
 * a server's own address keeps the chooser of sign-ins saved in this browser. */
function ServersRoute() {
  return currentWebEnvironment().hostedWeb ? <HostedServersScreen /> : <ServersScreen />;
}
function ServersScreen() {
  const [instance, setInstance] = useState<Readonly<{entry: BrowserAccountEntry; form: AccountSignInForm}>>();
  useEffect(() => {
    const entry = createBrowserAccountEntry(), form = new AccountSignInForm(entry);
    setInstance({entry, form});
    return () => {form.dispose(); entry.dispose();};
  }, []);
  return instance ? <BoundServers {...instance} /> : null;
}
function BoundServers({entry, form}: {entry: BrowserAccountEntry; form: AccountSignInForm}) {
  const state = useSyncExternalStore(entry.subscribe, entry.getSnapshot);
  const account = browserAccount(), accountState = useSyncExternalStore(account.service.subscribe, account.service.getSnapshot);
  const narrow = useNarrowViewport();
  const [choices, setChoices] = useState<readonly SavedServerChoice[]>([]), [busy, setBusy] = useState(false), [working, setWorking] = useState(false), [error, setError] = useState<unknown>();
  const [mode, setMode] = useState<'chooser' | 'address' | 'entry'>(() => router.state.location.search.local === true ? 'address' : 'chooser'), [origin, setOrigin] = useState<string>();
  const [attempt, setAttempt] = useState<MobileChooserAttempt>();
  const [directory, setDirectory] = useState<readonly DirectoryFacts[]>([]);
  const alive = useRef(true), loading = useRef<AbortController>(undefined), record = useRef<AppSessionRecord>(undefined), currentChoices = useRef(choices);
  const lockRef = useRef<AttemptLock | undefined>(undefined), attemptRef = useRef<MobileChooserAttempt | undefined>(attempt), retryDelay = useRef(20);
  lockRef.current ??= new AttemptLock();
  currentChoices.current = choices; attemptRef.current = attempt;
  const accountId = accountState.session?.account.id, familyId = accountState.session?.familyId;
  useEffect(() => {
    setDirectory([]);
    if (!accountId || !familyId) return;
    const controller = new AbortController();
    let active = true;
    const account = browserAccount();
    void (async () => {
      try {
        await account.service.synchronize();
        const session = await account.service.accessSession();
        if (!active) return;
        const scope = accountIdentity(session);
        const list = await createAccountServerDirectory(account, scope).read(controller.signal);
        if (!active || controller.signal.aborted) return;
        const current = account.service.getSnapshot().session;
        if (!current || current.account.id !== scope.accountId || current.familyId !== scope.familyId) return;
        setDirectory(list.items);
      } catch { /* The saved chooser keeps working without account presence. */ }
    })();
    return () => {active = false; controller.abort();};
  }, [accountId, familyId]);
  const refresh = useCallback(async () => {
    loading.current?.abort(); const controller = loading.current = new AbortController();
    if (!alive.current) return;
    setBusy(true); setError(undefined);
    try {
      const saved = await browserAppSessions().read();
      if (!alive.current || controller.signal.aborted) return;
      const names = await browserServerDisplay().choices(saved.servers.map(({signIn, connection}) => ({signIn, pin: connection.pin, origin: connection.logicalOrigin})), controller.signal);
      if (!alive.current || controller.signal.aborted) return;
      record.current = saved; setChoices(savedServerChoices(saved, names));
      if (!saved.servers.length) setMode(value => value === 'chooser' ? 'address' : value);
    } catch (failure) {if (alive.current && !controller.signal.aborted) setError(failure);}
    finally {if (alive.current && !controller.signal.aborted) setBusy(false);}
  }, []);
  useEffect(() => {
    alive.current = true; void refresh();
    const off = observeBrowserSessionChanges(() => {void refresh();});
    return () => {alive.current = false; lockRef.current?.cancel(); loading.current?.abort(); record.current = undefined; off();};
  }, [refresh]);
  const other = useCallback(() => {lockRef.current?.cancel(); setWorking(false); entry.cancel(); setAttempt(undefined); setError(undefined);}, [entry]);
  const openServer = useCallback(async (row: MobileChooserRow) => {
    const lock = lockRef.current!;
    const saved = record.current?.servers.find(value => value.connection.pin.serverId === row.id);
    if (!alive.current || lock.running || !saved || !currentChoices.current.some(value => value.id === row.id)) return;
    const token = lock.begin();
    setWorking(true); setError(undefined); setAttempt({kind: 'opening', row}); form.clear();
    const fail = (failure: unknown) => {
      if (!alive.current || !lock.owns(token)) return;
      setError(failure);
      const state = chooserFailureState(failure);
      if (state === 'waiting') {
        const seconds = retryDelay.current;
        setAttempt({kind: 'waiting', row, seconds, total: seconds});
        retryDelay.current = Math.min(seconds * 2, 300);
      } else if (state === 'refused') setAttempt({kind: 'refused', row});
      else setAttempt({kind: 'unreachable', row});
    };
    try {
      await browserAppSessionActions().switchServer(row.id, () => alive.current && lock.owns(token));
      if (!alive.current || !lock.owns(token)) return;
      // All restoration/profile/factor checks remain in the one real Entry.
      await entry.loadProfiles();
      if (!alive.current || !lock.owns(token)) return;
      setOrigin(saved.connection.logicalOrigin);
      const plan = await resumeAfterProfiles(entry, () => browserAppSessions().selected(), row.id, saved.connection.logicalOrigin,
        () => alive.current && lock.owns(token));
      if (!alive.current || !lock.owns(token)) return;
      if (plan === 'entry') {setAttempt(undefined); setMode('entry'); return;}
      if (plan === 'inspected') {
        const result = entry.getSnapshot();
        if (result.snapshot || result.server) {setAttempt(undefined); setMode('entry');} else fail(result.error);
        return;
      }
      const error = entry.getSnapshot().error;
      if (error) fail(error); else other();
    } catch (failure) {fail(failure);}
    finally {if (lock.settle(token) && alive.current) setWorking(false);}
  }, [entry, form, other]);
  useEffect(() => {
    if (attempt?.kind !== 'waiting') return;
    const step = waitingStep(attempt, 'timer', document.visibilityState === 'visible');
    if (step.kind === 'retry') {void openServer(attempt.row); return;}
    if (step.kind !== 'tick') return;
    const timer = setTimeout(() => {
      const current = attemptRef.current;
      const next = waitingStep(current, 'timer', document.visibilityState === 'visible');
      if (next.kind === 'tick') setAttempt(value => value?.kind === 'waiting' ? {...value, seconds: next.seconds} : value);
      else if (next.kind === 'retry' && current?.kind === 'waiting') void openServer(current.row);
    }, 1000);
    return () => clearTimeout(timer);
  }, [attempt, openServer]);
  useEffect(() => {
    const wake = () => {
      const current = attemptRef.current;
      const step = waitingStep(current, 'visibility', document.visibilityState === 'visible');
      if (step.kind === 'rearm') setAttempt(value => value?.kind === 'waiting' ? {...value} : value);
      else if (step.kind === 'retry' && current?.kind === 'waiting') void openServer(current.row);
    };
    document.addEventListener('visibilitychange', wake);
    return () => document.removeEventListener('visibilitychange', wake);
  }, [openServer]);
  const close = () => {form.clear(); entry.cancel(); void router.navigate({to: '/'});};
  const checkNow = () => {
    if (attempt?.kind === 'waiting' || attempt?.kind === 'unreachable') {retryDelay.current = 20; void openServer(attempt.row);}
  };
  const differentAccount = () => {
    if (busy || lockRef.current?.running || !alive.current) return;
    lockRef.current?.cancel(); setAttempt(undefined); form.clear(); entry.cancel(); setWorking(true); setError(undefined);
    void signOutBrowserPorticoAccount().then(() => {
      if (!alive.current) return;
      location.assign(new URL('/', browserAccountOrigin(import.meta.env, location)).href);
    }).catch(failure => {if (alive.current) setError(failure);}).finally(() => {if (alive.current) setWorking(false);});
  };
  if (mode === 'entry') return <AccountSignInView entry={entry} form={form} initialize={false} initialOrigin={origin}
    onCancel={() => {setMode('chooser'); void refresh();}} />;
  if (mode === 'address') return <LocalServerAddress busy={state.phase === 'checking'} error={state.error ?? error} onBack={() => {if (choices.length) setMode('chooser'); else close();}}
    onContinue={address => {if (state.phase === 'checking') return; form.clear(); setError(undefined); void entry.inspect(address).then(() => {
      if (!alive.current) return;
      const result = entry.getSnapshot();
      if (result.server && (result.phase === 'password' || result.phase === 'portico')) {setOrigin(result.origin); setMode('entry');}
    });}} />;
  if (narrow) {
    const rows = mobileChooserRows(choices, directory);
    const presented = error ? presentError(error, 'sign-in', {operation: 'load'}) : undefined;
    const notice = presented && !presented.silent ? presented.body : undefined;
    const mobileAttempt = attempt ?? (busy && !rows.length ? {kind: 'loading'} as const : error && !rows.length ? {kind: 'loadFailed'} as const : undefined);
    return <MobileServerChooser rows={rows} initial={initialOf(accountState.session?.account.displayName)}
      attempt={mobileAttempt} busy={busy || working} notice={notice}
      onChoose={row => {retryDelay.current = 20; void openServer(row);}} onRefresh={() => {setAttempt(undefined); setError(undefined); void refresh();}}
      onLocal={() => {form.clear(); entry.cancel(); setError(undefined); setMode('address');}} onDifferentAccount={rows.some(row => row.signIn === 'portico') ? differentAccount : undefined}
      onCheckNow={checkNow} onOther={other} />;
  }
  return <ServerSwitcher choices={choices} busy={busy || working} error={error} onChoose={choice => void openServer(choice)} onRefresh={() => void refresh()}
    onLocal={() => {form.clear(); entry.cancel(); setError(undefined); setMode('address');}} onClose={close} />;
}
export default defineWebScreen({id: 'servers', component: ServersRoute, chrome: 'standalone', validateSearch: raw => ({...(raw.local === true || raw.local === 'true' ? {local: true} : {}),
  // O-161: the server a shared link names; the Hosted chooser opens it when this account can.
  ...(typeof raw.link === 'string' && /^[A-Za-z0-9._~-]{1,128}$/.test(raw.link) ? {link: raw.link} : {})})});
