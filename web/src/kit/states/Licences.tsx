import {useEffect, useState} from 'react';
import {defaultI18n} from '@i18n';
import {licenceDetail, licenceRowDetail, licenceSections, licenceSourceRows, parseLicenceData, type LicenceData} from '@core/presentation/licences.ts';
import {Button} from '../controls';
import {Dialog} from '../overlays/Dialog';
import styles from './licences.module.css';
const t = defaultI18n.t;

/** Reads the generated inventory only while open; no fabricated component or notice. */
export function Licences({open, onOpenChange}: {open: boolean; onOpenChange: (open: boolean) => void}) {
  const [data, setData] = useState<LicenceData>(), [failed, setFailed] = useState(false), [retry, setRetry] = useState(0), [selected, setSelected] = useState<string>();
  useEffect(() => {
    if (!open) return;
    let current = true; setFailed(false); setSelected(undefined);
    void import('../../generated/licences.json').then(module => {
      const next = parseLicenceData(module.default);
      if (!next) throw new Error('invalid_licences');
      if (current) setData(next);
    }).catch(() => {if (current) setFailed(true);});
    return () => {current = false;};
  }, [open, retry]);
  const detail = data && selected ? licenceDetail(data, selected) : undefined;
  return <Dialog open={open} onOpenChange={onOpenChange} size="lg" title={detail?.component.name ?? t('licenses.title')}
    footer={<Button onClick={() => onOpenChange(false)}>{t('next.overlays.close')}</Button>}>
    {failed ? <div role="alert"><p>{t('licenses.unavailable')}</p><Button onClick={() => setRetry(value => value + 1)}>{t('next.states.tryAgain')}</Button></div>
      : !data ? <p role="status">{t('licenses.loading')}</p>
      : detail ? <div className={styles.detail}>
        <Button variant="text" icon="i-left" onClick={() => setSelected(undefined)}>{t('licenses.title')}</Button>
        <p>{licenceRowDetail(detail.component)}</p>
        {detail.component.url && <a href={detail.component.url} target="_blank" rel="noopener noreferrer">{t('licenses.website')}</a>}
        {detail.copyright.length > 0 && <section><h3>{t('licenses.copyright')}</h3>{detail.copyright.map((line, index) => <p key={index}>{line}</p>)}</section>}
        <section><h3>{t('licenses.text')}</h3>{detail.text ? <pre tabIndex={0}>{detail.text}</pre> : <p>{t(detail.component.textAtUrl ? 'licenses.textAtWebsite' : 'licenses.textMissing')}</p>}</section>
      </div> : <div className={styles.list}>
        <p>{t('licenses.portico.body')}</p>
        <section><h3>{t('licenses.source.title')}</h3>{licenceSourceRows(data).map(row => <a key={row.id} href={row.url} target="_blank" rel="noopener noreferrer">{t(row.label)}</a>)}</section>
        {licenceSections(data).map(section => <section key={section.group}><h3>{t(section.title)}</h3>{section.components.map(component =>
          <button key={component.id} type="button" onClick={() => setSelected(component.id)}><b>{component.name}</b><span>{licenceRowDetail(component)}</span></button>)}</section>)}
      </div>}
  </Dialog>;
}
