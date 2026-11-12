import {useId} from 'react';
import {defaultI18n} from '@i18n';
import {FEEDBACK_MAX_MESSAGE, feedbackKinds, type FeedbackDraft, type FeedbackSnapshot} from '@core/feedback.ts';
import {Button, Select} from '../controls';
import {Dialog} from '../overlays';
import {Icon} from '../Icon';
import styles from './feedback.module.css';

const t = defaultI18n.t;
/** Today's viewer flow, composed by ruling 35 from the approved dialog/form
 * parts. Technical report details belong only to the owner's inbox. */
export function FeedbackDialog({snapshot, title, problem, onChange, onSubmit, onClose}: {
  snapshot: Readonly<{service: FeedbackSnapshot; draft: FeedbackDraft}>; title?: string; problem: string;
  onChange: (patch: Partial<Pick<FeedbackDraft, 'kind' | 'category' | 'message'>>) => void;
  onSubmit: () => void; onClose: () => void;
}) {
  const id = useId(), state = snapshot.service, draft = snapshot.draft, kinds = feedbackKinds();
  const kind = kinds.find(value => value.id === draft.kind);
  const sent = state.phase === 'sent', sending = state.phase === 'sending';
  return <Dialog open title={t(sent ? 'feedback.sentTitle' : 'feedback.title')}
    description={!sent && title ? t('feedback.about', {title}) : undefined}
    width={sent ? 460 : 520} onOpenChange={open => {if (!open) onClose();}}
    footer={sent ? <Button variant="primary" onClick={onClose}>{t('action.done')}</Button> : <>
      <Button variant="glass" onClick={onClose}>{t('action.cancel')}</Button><Button variant="primary" disabled={Boolean(problem) || state.phase !== 'ready'} loading={sending} onClick={onSubmit}>{t('feedback.send')}</Button>
    </>}>
    {sent ? <p className={styles.body}>{t('feedback.sentBody')}</p>
      : <div className={styles.form}>
          {state.error && <div className={styles.notice} data-error role="alert"><Icon name="i-warn" size={20} /><span>{state.error}</span></div>}
          <div className={styles.pair}>
            <Select label={t('feedback.kind')} options={kinds.map(value => ({value: value.id, label: value.label}))} value={draft.kind} disabled={sending} onValueChange={value => onChange({kind: value})} />
            <Select label={t('feedback.category')} options={(kind?.categories ?? []).map(value => ({value: value.id, label: value.label}))} value={draft.category} disabled={sending} onValueChange={value => onChange({category: value})} />
          </div>
          <div className={styles.field}><label htmlFor={`${id}-message`}>{t('feedback.message')}</label>
            <textarea id={`${id}-message`} rows={5} maxLength={FEEDBACK_MAX_MESSAGE * 2} value={draft.message} disabled={sending}
              onChange={event => onChange({message: event.currentTarget.value})} placeholder={t('feedback.messagePlaceholder')} aria-describedby={`${id}-length`} />
          </div>
          <div className={styles.length} id={`${id}-length`}><span data-error={Boolean(problem && draft.message) || undefined}>{draft.message ? problem : ''}</span>
            <span>{t('feedback.length', {count: Array.from(draft.message.trim()).length, max: FEEDBACK_MAX_MESSAGE})}</span></div>
          <p className={styles.caption}>{t(title ? 'feedback.sentWithTitle' : 'feedback.sentToOwner')}</p>
        </div>}
  </Dialog>;
}
