import {useRef, useState, type CSSProperties} from 'react';
import {defaultI18n} from '@i18n';
import {formatClock} from '@core/presentation/content.ts';
import {Button} from '../../kit/controls';
import {Dialog} from '../../kit/overlays';
import styles from './move-to-dialog.module.css';

export type MoveToDialogProps = Readonly<{
  title: string;
  viewer: string;
  device: string;
  durationMs: number;
  positionMs: number;
  busy: boolean;
  onMove: (positionMs: number) => void;
  onCancel: () => void;
}>;

/** The parent withdraws this presenter when the original session expires. Close and reopen to retry. */
export function MoveToDialog(props: MoveToDialogProps) {
  const {busy} = props;
  const {title, viewer, device, durationMs, positionMs, onMove, onCancel} = useRef(props).current;
  const t = defaultI18n.t;
  const [preview, setPreview] = useState(positionMs);
  const submittedRef = useRef(false);
  const [submitted, setSubmitted] = useState(false);
  const valid = Number.isFinite(durationMs) && durationMs >= 0 && Number.isFinite(positionMs)
    && positionMs >= 0 && positionMs <= durationMs && Number.isFinite(preview) && preview >= 0 && preview <= durationMs;
  const position = valid ? formatClock(preview / 1000) : undefined;
  const duration = valid ? formatClock(durationMs / 1000) : undefined;
  const original = valid ? formatClock(positionMs / 1000) : undefined;
  const rangeStyle = valid && durationMs > 0 ? {'--progress': `${preview / durationMs * 100}%`, '--original': `${positionMs / durationMs * 100}%`} as CSSProperties : undefined;
  return <Dialog open onOpenChange={open => {if (!open && !busy) onCancel();}}
    title={t('web.nowPlaying.moveTitle', {title})} description={t('next.dashboard.moveBody', {viewer, device})}
    showCloseButton={false} dismissOnBack={!busy} dismissOnOutsidePress={!busy}
    headerClassName={styles.header} bodyClassName={styles.body} footerClassName={styles.footer}
    footer={<><Button variant="glass" disabled={busy} onClick={() => {if (!busy) onCancel();}}>{t('action.cancel')}</Button><Button variant="primary" disabled={busy || submitted || !valid} onClick={() => {if (busy || !valid || submittedRef.current) return; submittedRef.current = true; setSubmitted(true); onMove(preview);}}>{t('web.nowPlaying.moveConfirm')}</Button></>}>
    <div className={styles.timeline} style={rangeStyle}>
      <span className={styles.track} aria-hidden="true"><span className={styles.fill}/><span className={styles.original}/></span>
      <input type="range" min={0} max={valid ? durationMs : 0} step={1} value={valid ? preview : 0} disabled={busy || submitted || !valid || durationMs === 0}
        aria-label={t('next.dock.position')} aria-valuetext={position && duration ? t('next.dock.positionOf', {position, duration}) : undefined}
        onChange={event => {const value = event.currentTarget.valueAsNumber; if (!busy && !submittedRef.current && valid && Number.isFinite(value)) setPreview(Math.max(0, Math.min(durationMs, value)));}}/>
    </div>
    {position && duration && original && <div className={styles.clock}><span>{t('next.dock.positionOf', {position, duration})}</span><span>{t('next.dashboard.nowAt', {position: original})}</span></div>}
  </Dialog>;
}
