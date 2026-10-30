import {useId} from 'react';
import {defaultI18n} from '@i18n';
import type {FeedbackDraft, FeedbackSnapshot} from '@core/inbox.ts';
import {Button, Select} from '../controls';
import {Dialog} from '../overlays';
import {Icon} from '../Icon';
import styles from './feedback.module.css';

const t = defaultI18n.t;
/** Today's viewer flow, composed by ruling 35 from the approved dialog/form
 * parts. Technical report details belong only to the owner's inbox. */
export function FeedbackDialog({snapshot, title, hasPlayback = false, problem, onChange, onSubmit, onClose}: {
  snapshot: Readonly<{service: FeedbackSnapshot; draft: FeedbackDraft}>; title?: string; hasPlayback?: boolean; problem: string;
  onChange: (patch: Partial<Pick<FeedbackDraft, 'kind' | 'category' | 'message' | 'attachDiagnostics'>>) => void;
  onSubmit: () => void; onClose: () => void;
}) {
  const id = useId(), state = snapshot.service, capabilities = state.capabilities, draft = snapshot.draft;
  const kind = capabilities?.kinds.find(value => value.id === draft.kind), category = kind?.categories.find(value => value.id === draft.category);
  const sent = state.phase === 'sent', duplicate = state.result?.duplicate, sending = state.phase === 'sending';
  return <Dialog open title={t(sent ? duplicate ? 'feedback.duplicateTitle' : 'feedback.sentTitle' : 'feedback.title')}
    description={!sent && title ? t('feedback.about', {title}) : undefined}
    width={sent ? 460 : 520} onOpenChange={open => {if (!open) onClose();}}
    footer={sent ? <Button variant="primary" onClick={onClose}>{t('action.done')}</Button> : state.phase === 'unavailable' ? <Button variant="glass" onClick={onClose}>{t('action.close')}</Button> : <>
      <Button variant="glass" onClick={onClose}>{t('action.cancel')}</Button><Button variant="primary" disabled={Boolean(problem) || state.phase !== 'ready'} loading={sending} onClick={onSubmit}>{t('feedback.send')}</Button>
    </>}>
    {sent ? <p className={styles.body}>{t(duplicate ? 'feedback.duplicateBody' : 'feedback.sentBody')}</p> : state.phase === 'loading' ? <p role="status" className={styles.caption}>{t('feedback.loadingForm')}</p>
      : state.phase === 'unavailable' ? <div className={styles.notice} role="status"><Icon name="i-info" size={20} /><span>{capabilities?.submitBlockedReason || state.error}</span></div>
        : capabilities && <div className={styles.form}>
          {state.error && <div className={styles.notice} data-error role="alert"><Icon name="i-warn" size={20} /><span>{state.error}</span></div>}
          <div className={styles.pair}>
            <Select label={t('feedback.kind')} options={capabilities.kinds.map(value => ({value: value.id, label: value.label}))} value={draft.kind} disabled={sending} onValueChange={value => onChange({kind: value})} />
            <Select label={t('feedback.category')} options={(kind?.categories ?? []).map(value => ({value: value.id, label: value.label}))} value={draft.category} disabled={sending} onValueChange={value => onChange({category: value})} />
          </div>
          {category?.description && <p className={styles.caption}>{category.description}</p>}
          <div className={styles.field}><label htmlFor={`${id}-message`}>{t('feedback.message')}</label>
            <textarea id={`${id}-message`} rows={5} maxLength={capabilities.maxMessageLength * 2} value={draft.message} disabled={sending}
              onChange={event => onChange({message: event.currentTarget.value})} placeholder={t('feedback.messagePlaceholder')} aria-describedby={`${id}-length`} />
          </div>
          <div className={styles.length} id={`${id}-length`}><span data-error={Boolean(problem && draft.message) || undefined}>{draft.message ? problem : ''}</span>
            <span>{t('feedback.length', {count: Array.from(draft.message.trim()).length, max: capabilities.maxMessageLength})}</span></div>
          {capabilities.diagnosticsSupported && category?.wantsPlaybackSession && hasPlayback && <label className={styles.checkbox}>
            <input type="checkbox" checked={draft.attachDiagnostics} disabled={sending || !capabilities.diagnosticsOptional} onChange={event => onChange({attachDiagnostics: event.currentTarget.checked})} />
            <span>{t('feedback.playbackDiagnostics')}<small>{t('feedback.playbackDiagnosticsHelp')}</small></span>
          </label>}
          <p className={styles.caption}>{t('feedback.sentAs', {name: capabilities.reporterName})}</p>
        </div>}
  </Dialog>;
}
