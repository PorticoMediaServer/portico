import {useEffect, useState, type ReactNode} from 'react';
import {defaultI18n} from '@i18n';
import type {MessageId} from '@i18n';
import {Segmented, Select} from '../../kit/controls';
import {Skeleton, StateLayout} from '../../kit/states';
import type {ReadState} from './use-read';
import styles from './dashboard.module.css';
export const t = defaultI18n.t;
export const mediaLabel = (kind: string) => t(({movies: 'next.dashboard.movies', tv: 'next.dashboard.tv', music: 'next.dashboard.music', audiobooks: 'next.dashboard.audiobooks', live: 'next.dashboard.liveMedia', all: 'next.dashboard.allMedia'} as Record<string, MessageId>)[kind] ?? 'next.dashboard.other');
export function Range({value, onChange, values, load = false, phone = false, label}: {load?: boolean; phone?: boolean; label?: string; value: string; onChange: (value: string) => void; values: readonly string[]}) {
  const options = values.map(value => ({value, label: t(`next.dashboard.${load && ['7d', '30d', '90d'].includes(value) ? 'load.' : ''}${value}` as MessageId)}));
  return phone
    ? <Segmented variant="phone" aria-label={label ?? t('next.dashboard.range')} value={value} onValueChange={onChange} items={options.map(option => ({value: option.value, label: option.label}))} />
    : <Select aria-label={label ?? t('next.dashboard.range')} value={value} onValueChange={onChange} options={options} />;
}
export function Section({title, extra, actions, children, id, phone = false}: {title: string; extra?: ReactNode; actions?: ReactNode; children: ReactNode; id?: string; phone?: boolean}) {
  const Heading = phone ? 'h2' : 'h3';
  return <section className={phone ? styles.phoneSection : styles.section} id={id}><div className={phone ? styles.phoneHeading : styles.heading}><Heading>{title}</Heading>{extra}<div className={styles.filters}>{actions}</div></div>{children}</section>;
}
export function ReadFeedback({state, openHome, height = 156}: {openHome: () => void; height?: number; state: ReadState<unknown> & {refresh: () => void}}) {
  const pending = state.loading && !state.value;
  const [shapes, showShapes] = useState(false);
  useEffect(() => {
    if (!pending) {showShapes(false); return;}
    const timer = setTimeout(() => showShapes(true), 300);
    return () => clearTimeout(timer);
  }, [pending]);
  return state.failed ? <div role="alert"><StateLayout className={styles.pageFailure} title={t('next.states.pageFailed')} body={t('next.states.pageFailedBody')} icon="i-alert" tone="warning" secondary={{id: 'home', label: t('next.states.goHome'), onPress: openHome}} primary={{id: 'retry', label: t('next.dashboard.retry'), icon: 'i-refresh', onPress: state.refresh}} /></div> : pending && shapes ? <div aria-busy="true"><Skeleton height={height} sheen /></div> : null;
}
