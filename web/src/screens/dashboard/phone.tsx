import {useEffect, useRef, useState, type ReactNode} from 'react';
import {defaultI18n} from '@i18n';
import type {PlayHistoryPeriod} from '@core/play-history.ts';
import {accountAvatarStyle} from '../../kit/settings/account-avatar';
import {Icon} from '../../kit/Icon';
import type {DashboardBindings, DashboardServerHeader} from './bindings';
import type {DashboardReportController} from './use-report';
import {ReadFeedback, Range, Section} from './shared';
import styles from './dashboard.module.css';

const t = defaultI18n.t;
export type DashboardPhoneChrome = Readonly<{serverName: string; audience: 'owner' | 'administrators'}>;

/** The drawn mobile-web Dashboard opens as its own page: its bar, band, server header and sections. */
export function PhonePage({chrome, server, updatedAt, onBack, onRefresh, initialSection, children}: {
  chrome: DashboardPhoneChrome; server?: DashboardServerHeader; updatedAt?: number;
  onBack: () => void; onRefresh?: () => void; initialSection?: 'streams' | 'activity' | 'updates'; children: ReactNode;
}) {
  const scroll = useRef<HTMLDivElement>(null), [solid, setSolid] = useState(false);
  useEffect(() => {
    if (!initialSection) return;
    const frame = requestAnimationFrame(() => scroll.current?.querySelector(`[data-dashboard-section="${initialSection}"]`)?.scrollIntoView({block: 'start'}));
    return () => cancelAnimationFrame(frame);
  }, [initialSection]);
  // The page is full screen over the shell; its hidden tab bar must not take focus behind it.
  useEffect(() => {
    const navigation = document.querySelector<HTMLElement>('[data-shell-navigation]');
    if (!navigation) return;
    navigation.setAttribute('inert', ''); navigation.setAttribute('aria-hidden', 'true');
    return () => {navigation.removeAttribute('inert'); navigation.removeAttribute('aria-hidden');};
  }, []);
  return <div className={styles.phonePage} data-dashboard-page>
    <header className={styles.phoneBar} data-solid={solid || undefined}>
      <button type="button" className={styles.phoneBack} onClick={onBack} aria-label={t('next.states.back')}><Icon name="i-left" size={22} /></button>
      <span className={styles.phoneBarTitle}><b>{t('next.dashboard.title')}</b><small>{chrome.serverName}</small></span>
    </header>
    <div className={styles.phoneScroll} ref={scroll} onScroll={event => setSolid(event.currentTarget.scrollTop > 8)}>
      <header className={styles.phoneHead}>
        <div className={styles.phonePills}><span className={styles.phonePill}><Icon name="i-server" size={15} />{t('next.shell.server')}<span>{t(chrome.audience === 'owner' ? 'next.shell.serverVisibility.ownerShared' : 'next.shell.serverVisibility.administrator')}</span></span></div>
        <h1>{t('next.dashboard.title')}</h1>
        <div className={styles.phoneName}><b>{chrome.serverName}</b>{server?.health && <span className={styles.phoneHealth} data-health={server.health}>{t(server.health === 'normal' ? 'next.shell.health.normal' : 'next.shell.health.attention')}</span>}</div>
        {!!server?.facts?.length && <div className={styles.phoneFacts}>{server.facts.map(fact => <span key={fact.id}>{fact.text}</span>)}</div>}
        {onRefresh && updatedAt !== undefined && <div className={styles.phoneUpdated}><span>{updatedAge(updatedAt)}</span><button type="button" className={styles.phoneRefresh} onClick={onRefresh}><Icon name="i-refresh" size={16} />{t('next.dashboard.refresh')}</button></div>}
      </header>
      {children}
    </div>
  </div>;
}

/** "Updated a few seconds ago" is the drawn wording; a page paused in a hidden tab keeps the honest seconds. */
function updatedAge(at: number): string {
  const seconds = Math.max(0, Math.round((Date.now() - at) / 1000));
  return seconds < 60 ? t('next.dashboard.updatedNow') : t('next.dashboard.updatedWhen', {when: t('next.dashboard.secondsAgo', {seconds})});
}


/** The drawn phone Viewing statistics: four real figures over the two bar lists, with Play history as its own page. */
export function PhoneStatistics({bindings: b, report}: {bindings: DashboardBindings; report: DashboardReportController}) {
  const state = report.state, value = state.value?.summary, period = report.query.period;
  const periods = ['today', '7d', '30d', 'all'] as const;
  const number = (amount: number) => defaultI18n.number(amount);
  const bars = (items: readonly {name: string; plays: number}[], people: boolean) => {
    const max = Math.max(1, ...items.map(item => item.plays));
    return items.map(item => <div className={styles.phoneBarRow} key={item.name}>
      <div className={styles.phoneBarLine}><span className={styles.phoneBarName}>{people && <i className={styles.phoneBarAvatar} style={accountAvatarStyle(item.name)} aria-hidden="true">{item.name.slice(0, 1).toUpperCase()}</i>}{item.name}</span><b>{number(item.plays)}</b></div>
      <span className={styles.phoneBarTrack} aria-hidden="true"><i style={{width: `${item.plays / max * 100}%`}} /></span>
    </div>);
  };
  return <Section phone title={t('settings.server.statistics')}>
    <div className={styles.phoneRange}><Range phone load label={t('settings.server.statistics')} value={period} values={periods} onChange={next => report.change({period: next as PlayHistoryPeriod})} /></div>
    <ReadFeedback height={180} openHome={b.effects.openHome} state={{...state, value}} />
    {value && (value.plays ? <div className={styles.phoneStats}>
      <div className={styles.phoneFigures}>
        <div><small>{t('server.viewing.plays')}</small><b>{number(value.plays)}</b></div>
        <div><small>{t('server.viewing.people')}</small><b>{number(value.people)}</b></div>
        <div><small>{t('server.viewing.hours')}</small><b>{number(Math.round(value.watchedMs / 3_600_000))}</b></div>
        <div><small>{t('server.viewing.converted')}</small><b>{number(value.converted)}</b><em>{t('next.dashboard.convertedShare', {percent: Math.round(value.converted / value.plays * 100)})}</em></div>
      </div>
      {!!value.mostPlayed.length && <div className={styles.phoneBars}><h3>{t('server.viewing.mostPlayed')}</h3>{bars(value.mostPlayed, false)}</div>}
      {!!value.mostActive.length && <div className={styles.phoneBars}><h3>{t('server.viewing.mostActive')}</h3>{bars(value.mostActive, true)}</div>}
      <button type="button" className={styles.phoneHistoryLink} onClick={b.effects.openHistory}><Icon name="i-clock" size={20} />{t('next.dashboard.history')}<Icon name="i-right" size={18} /></button>
    </div> : <div className={styles.phoneCalm}><span className={styles.phoneCalmLead}><Icon name="i-clock" size={18} /></span><b>{t('web.telemetry.noViewing')}</b></div>)}
  </Section>;
}
