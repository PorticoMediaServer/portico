import {useEffect, useState} from 'react';
import {defaultI18n} from '@i18n';
import {ownFeedbackReportView, readOwnFeedbackReport} from '@core/notification-actions.ts';
import {presentError} from '@core/presentation/errors.ts';
import {Dialog} from '../../kit/overlays/Dialog';
import styles from './notifications.module.css';

type Api = Parameters<typeof readOwnFeedbackReport>[0];
type Loaded = Readonly<{phase: 'loading'}> | Readonly<{phase: 'ready'; view: ReturnType<typeof ownFeedbackReportView>}> | Readonly<{phase: 'failed'; error: unknown}>;

/**
 * "Your report has an update" → Open report: the person's own report, read only. No frame draws an editor for a
 * reporter (desktop-search.html draws only the notice's Open report), so this shows what the owner sees and replied:
 * the category, when it was reported and its status, the message, and each reply.
 */
export function OwnFeedbackReport({api, reportId, onClose}: Readonly<{api: Api; reportId: string; onClose: () => void}>) {
  const [loaded, setLoaded] = useState<Loaded>({phase: 'loading'});
  useEffect(() => {
    const controller = new AbortController();
    setLoaded({phase: 'loading'});
    void readOwnFeedbackReport(api, reportId, controller.signal).then(
      report => {if (!controller.signal.aborted) setLoaded({phase: 'ready', view: ownFeedbackReportView(report, defaultI18n)});},
      error => {if (!controller.signal.aborted) setLoaded({phase: 'failed', error});});
    return () => controller.abort();
  }, [api, reportId]);
  const failed = loaded.phase === 'failed' ? presentError(loaded.error, 'feedback') : undefined;
  return <Dialog open onOpenChange={value => {if (!value) onClose();}} size="md"
    title={loaded.phase === 'ready' ? loaded.view.title : failed && !failed.silent ? failed.title : defaultI18n.t('status.loading')}>
    {loaded.phase === 'loading' && <p role="status">{defaultI18n.t('status.loading')}</p>}
    {failed && !failed.silent && <p role="alert">{failed.body}</p>}
    {loaded.phase === 'ready' && <>
      <p className={styles.reportMeta}>{loaded.view.meta}</p>
      <p className={styles.reportMessage}>{loaded.view.message}</p>
      {loaded.view.replies.map((reply, index) => <blockquote key={index} className={styles.reportReply}><p>{reply.text}</p><footer>{reply.at}</footer></blockquote>)}
    </>}
  </Dialog>;
}
