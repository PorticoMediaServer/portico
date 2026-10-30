import {useId} from 'react';
import {defaultI18n} from '@i18n';
import {Button} from '../../kit/controls';
import type {PlayOnLinkPreview} from '../../platform/playback/play-on';

const t = defaultI18n.t;

/** The body of Settings › This device › Link a TV, with no state of its own: the caller keeps the
 * code, the phase and the link service, and this draws the code entry, the device it reports and
 * the record of its own approval. Raw input goes back untouched; the caller owns normalisation. */
export function LinkTvForm({code, phase, preview, onCodeChange, onReview, onDecide}: {
  code: string;
  phase: 'code' | 'busy' | 'review' | 'done' | 'failed';
  preview?: PlayOnLinkPreview;
  onCodeChange(value: string): void;
  onReview(): void;
  onDecide(decision: 'approve' | 'deny'): void;
}) {
  const codeId = useId();
  if (phase === 'done') return <p role="status">{t('next.cast.linkTvApproved')}</p>;
  return <>
    {preview ? <>
      <p><b>{t('next.cast.linkTvReview', {device: preview.deviceName})}</b>{preview.platform && <small>{preview.platform}</small>}</p>
      <div style={{display: 'flex', gap: 8, marginTop: 12}}>
        <Button size="small" disabled={phase === 'busy'} onClick={() => onDecide('approve')}>{t('next.cast.linkTvApprove')}</Button>
        <Button size="small" variant="glass" disabled={phase === 'busy'} onClick={() => onDecide('deny')}>{t('next.cast.linkTvDeny')}</Button>
      </div>
    </> : <>
      <label htmlFor={codeId}>{t('next.cast.linkTvCode')}</label>
      <input id={codeId} value={code} maxLength={10} autoComplete="off" autoCapitalize="characters" spellCheck={false} disabled={phase === 'busy'}
        onChange={event => onCodeChange(event.currentTarget.value)} />
      <p>{t('next.cast.linkTvHint')}</p>
      <Button size="small" disabled={phase === 'busy' || code.trim().length < 4} onClick={onReview}>
        {phase === 'busy' ? t('next.cast.linkTvConnecting') : t('next.cast.linkTvConnect')}</Button>
    </>}
    {phase === 'failed' && <p role="status">{t('next.cast.linkTvFailed')}</p>}
  </>;
}
