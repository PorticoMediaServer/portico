import {useId, useLayoutEffect, useRef, useState, type ReactNode} from 'react';
import {defaultI18n} from '@i18n';
import {Icon} from '../Icon';
import styles from './settings.module.css';

const t = defaultI18n.t;
export function SettingsGroup({title, description, actions, children, advanced = false, deviceLabel, plain = false}: {title?: ReactNode; description?: ReactNode; actions?: ReactNode; children: ReactNode; advanced?: boolean; deviceLabel?: string; plain?: boolean}) {
  return <section className={styles.group} data-advanced={advanced || undefined}>
    {(title || actions) && <div className={styles.groupHeading}><h3>{title}{deviceLabel && <small>{deviceLabel}</small>}</h3>{actions}</div>}
    {description && <p className={styles.groupDescription}>{description}</p>}
    <div className={styles.groupRows} data-plain={plain || undefined}>{children}</div>
  </section>;
}

/** The caller supplies only changed, visible rows from this exact Advanced scope. */
export function SettingsAdvanced({open, onOpenChange, summary, children}: {open: boolean; onOpenChange: (open: boolean) => void; summary: readonly string[]; children: ReactNode}) {
  const id = useId(), available = useRef<HTMLSpanElement>(null), measure = useRef<HTMLSpanElement>(null), [fit, setFit] = useState(summary.length);
  const signature = summary.join('\u0000');
  useLayoutEffect(() => {
    const element = available.current, probe = measure.current; if (!element || !probe) return;
    const read = () => {
      const parts = [...probe.querySelectorAll<HTMLElement>('[data-part]')], tails = [...probe.querySelectorAll<HTMLElement>('[data-tail]')];
      let width = 0, count = 0;
      for (let index = 0; index < parts.length; index++) {
        const next = width + parts[index].getBoundingClientRect().width;
        const tail = index < parts.length - 1 ? tails[parts.length - index - 2]?.getBoundingClientRect().width ?? 0 : 0;
        if (next + tail > element.clientWidth) break;
        width = next; count++;
      }
      setFit(count);
    };
    const observer = new ResizeObserver(read); observer.observe(element); read();
    void document.fonts?.ready.then(read);
    return () => observer.disconnect();
  }, [signature, open]);
  return <section className={styles.advanced} data-open={open || undefined}>
    <button type="button" className={styles.advancedToggle} aria-expanded={open} aria-controls={id} onClick={() => onOpenChange(!open)}><span>{t('next.settings.advanced')}</span>
      <span ref={available} className={styles.advancedSummary}>{summary.length ? <><span>{summary.slice(0, fit || 1).join(' · ')}</span>{(fit || 1) < summary.length && <small>{t('next.settings.more', {count: summary.length - (fit || 1)})}</small>}</> : t('next.settings.allDefaults')}</span><Icon name="i-down" size={18} />
    </button>
    <span ref={measure} className={styles.summaryMeasure} aria-hidden="true">{summary.map((part, index) => <span key={index} data-part>{index ? ' · ' : ''}{part}</span>)}{summary.map((_, index) => <small data-tail key={index}>{t('next.settings.more', {count: index + 1})}</small>)}</span>
    <div id={id} hidden={!open} className={styles.advancedBody}>{children}</div>
  </section>;
}
