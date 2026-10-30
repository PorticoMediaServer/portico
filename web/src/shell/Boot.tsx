import {useEffect, useLayoutEffect, useState, type ReactNode} from 'react';
import {Link} from '@tanstack/react-router';
import {defaultI18n} from '@i18n';
import {ColourBackdrop} from '../kit/artwork';
import {Icon} from '../kit/Icon';
import {useCoarsePointer, useSizeClass} from '../kit/theme';
import {bootWaitingPhase, documentStartedAt} from './boot-state';
import {bootMotion, revealBootDestination} from './boot-handover';
import type {ShellBootstrapSnapshot} from './bootstrap';
import wordmark from './wordmark.svg';
import shell from './shell.module.css';
import styles from './boot.module.css';

const t = defaultI18n.t;

function useBootNavigation() {
  const size = useSizeClass(), coarse = useCoarsePointer();
  const [collapsed] = useState(() => {
    try { return window.localStorage.getItem('portico.next.sidebar-collapsed') === 'true'; } catch { return false; }
  });
  return {size, coarse, navigation: size === 'compact' ? 'tabs' : size === 'expanded' && !coarse ? 'topbar' : size === 'medium' || collapsed ? 'rail' : 'sidebar'} as const;
}

/** A read selected a genuine saved session. Until the live list arrives, only
 * Home and the neutral avatar slot are drawn; no cached library facts enter. */
export function BootShell({children}: {children: ReactNode}) {
  const {size, coarse, navigation} = useBootNavigation();
  const brand = <Link className={shell.brand} to="/" aria-label={t('next.platform.appName')}><img src={wordmark} alt="" /></Link>;
  const avatar = <span className={styles.avatar} aria-hidden="true" />;
  const home = <Link to="/" activeOptions={{exact: true}} activeProps={{'aria-current': 'page'}}><Icon name="i-home" size={22}/><span>{t('next.home.shell.home')}</span></Link>;
  return <div className={`${shell.shell} ${styles.shell}`} data-desktop={navigation === 'topbar'} data-size-class={size} data-pointer={coarse ? 'coarse' : 'fine'} data-navigation={navigation}>
    <ColourBackdrop />
    <div className={shell.navigation} data-shell-navigation>
      {navigation === 'topbar' ? <nav className={shell.topnav} aria-label={t('next.home.shell.main')}>
        {brand}<ul className={shell.destinations}><li><Link to="/" aria-current="page">{t('next.home.shell.home')}</Link></li></ul><div className={shell.tools}>{avatar}</div>
      </nav> : navigation === 'tabs' ? <>
        <header className={shell.phoneHeader}>{brand}{avatar}</header>
        <nav className={`${shell.tabbar} ${styles.tabs}`} aria-label={t('next.home.shell.main')}>{home}</nav>
      </> : <nav className={shell.rail} aria-label={t('next.home.shell.main')}>
        {navigation === 'rail' ? <div className={shell.nativeRailMark}><span><img src={wordmark} alt={t('next.platform.appName')} /></span></div> : <div className={shell.railBrand}>{brand}</div>}
        <div className={shell.railPrimary}><Link className={shell.railItem} to="/" aria-current="page"><Icon name="i-home" size={22}/><span data-nav-label>{t('next.home.shell.home')}</span></Link></div>
        <div className={shell.railBottom}>{avatar}</div>
      </nav>}
    </div>
    {children}
  </div>;
}

type Waiting = Readonly<{startedAt: number; serverName?: string}>;
/** Kept outside route lifetimes so its quick exit survives the real shell mounting. */
export function BootProgress({snapshot}: {snapshot?: ShellBootstrapSnapshot}) {
  const active = snapshot?.phase === 'restoring' && !!snapshot.waiting;
  const startedAt = snapshot?.startedAt ?? documentStartedAt(), serverName = snapshot?.waiting?.serverName;
  const [held, setHeld] = useState<Waiting | undefined>(() => active ? {startedAt, serverName} : undefined);
  const {navigation, size} = useBootNavigation();
  useLayoutEffect(() => {
    if (active) setHeld({startedAt, serverName});
  }, [active, startedAt, serverName]);
  useLayoutEffect(() => {
    if (active || !held) return;
    revealBootDestination(document);
    const timer = setTimeout(() => setHeld(undefined), bootMotion(document).duration);
    return () => clearTimeout(timer);
  }, [active, held]);
  const waiting = active ? {startedAt, serverName} : held;
  if (!waiting) return null;
  return <div className={styles.progressLayer} data-navigation={navigation} data-size-class={size} data-leaving={!active || undefined} aria-hidden={!active || undefined}>
    <ColourBackdrop />
    <BootWaiting startedAt={waiting.startedAt} serverName={waiting.serverName} />
  </div>;
}

function BootWaiting({startedAt, serverName}: Waiting) {
  const [now, setNow] = useState(Date.now);
  useEffect(() => {
    setNow(Date.now());
    const timers = [1000, 3000, 8000].filter(at => startedAt + at > Date.now())
      .map(at => setTimeout(() => setNow(Date.now()), Math.max(0, startedAt + at - Date.now())));
    return () => timers.forEach(clearTimeout);
  }, [startedAt]);
  const phase = bootWaitingPhase(startedAt, now), message = phase === 'still-connecting' ? t('next.boot.stillConnecting') : phase === 'connecting'
    ? serverName ? t('web.chooser.connecting', {server: serverName}) : t('next.boot.connecting') : '';
  return <div className={styles.waiting} data-phase={phase} aria-busy="true">
    <div className={styles.spinner} aria-hidden="true"><i/></div>
    <div className={styles.message} aria-hidden="true">
      <span data-shown={phase === 'connecting' || undefined}>{serverName ? t('web.chooser.connecting', {server: serverName}) : t('next.boot.connecting')}</span>
      <span data-shown={phase === 'still-connecting' || undefined}>{t('next.boot.stillConnecting')}</span>
    </div>
    <span className={styles.srOnly} role="status" aria-live="polite">{message || (phase === 'spinner' ? t('next.states.loading') : '')}</span>
  </div>;
}
