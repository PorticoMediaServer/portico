import {useState} from 'react';
import {defaultI18n} from '@i18n';
import type {SettingsRow} from '@core/presentation/settings-spec.ts';
import {remeasureAllSegments, retryUnreadableSegments, type SegmentApi} from '@core/segment-review.ts';
import {Button} from '../../../kit/controls';
import {Dialog} from '../../../kit/overlays';

const t = defaultI18n.t;

/**
 * Intros and credits' two bulk actions (#3255, client wrappers #3308): measure every enabled video library again, after
 * the spec's confirmation, or try the unreadable files again. The page's status is read again after either.
 */
export function SegmentsBulkAction({row, api, isCurrent, disabled, onDone}: Readonly<{row: SettingsRow; api: SegmentApi; isCurrent: () => boolean; disabled?: boolean; onDone: () => void}>) {
  const [asking, setAsking] = useState(false), [busy, setBusy] = useState(false), [failed, setFailed] = useState(false);
  const label = row.control.label ? t(row.control.label) : t(row.label);
  const run = async () => {
    setBusy(true); setFailed(false);
    try {await (row.id === 'segments.redetectAll' ? remeasureAllSegments(api) : retryUnreadableSegments(api)); if (isCurrent()) {setAsking(false); onDone();}}
    catch {if (isCurrent()) setFailed(true);}
    finally {if (isCurrent()) setBusy(false);}
  };
  return <>
    <Button size="small" variant="glass" disabled={disabled || busy} loading={busy && !asking} onClick={() => {if (row.control.confirm) setAsking(true); else void run();}}>{label}</Button>
    {failed && !asking && <span role="alert">{t('next.states.pageFailedBody')}</span>}
    {row.control.confirm && <Dialog open={asking} onOpenChange={open => {if (!open && !busy) setAsking(false);}} size="sm" role="alertdialog" title={label} description={t(row.control.confirm)}
      footer={<><Button disabled={busy} onClick={() => setAsking(false)}>{t('next.settings.cancel')}</Button><Button variant="primary" loading={busy} disabled={busy} onClick={() => {void run();}}>{label}</Button></>}>
      {failed ? <p role="alert">{t('next.states.pageFailedBody')}</p> : null}
    </Dialog>}
  </>;
}
