import {useEffect, useState} from 'react';
import {defaultI18n, type MessageId} from '@i18n';
import {actionNotice, type UserAction, type UserActionSnapshot} from '@core/user-action.ts';
import {Button} from '../controls/Button';
import {Icon} from '../Icon';
import styles from './action-notice.module.css';

export type ActionNoticeProps<T, A extends readonly unknown[]> = Readonly<{
  snapshot: UserActionSnapshot<T>;
  action: Pick<UserAction<T, A>, 'retry'>;
  /** The screen's drawn line, when its mock-up has one. */
  title?: string;
  body?: string;
}>;

/** The kit's inline failure for a user action (collections.html 24's notice): shown for failed and
 *  unconfirmed, nothing otherwise. Try again keeps the action's intent, and waits out a server's
 *  Retry-After before it can be pressed. */
export function ActionNotice<T, A extends readonly unknown[]>({snapshot, action, title, body}: ActionNoticeProps<T, A>) {
  const notice = actionNotice(snapshot), waitMs = notice?.retryAfterMs ?? 0;
  // Try again is held from the first frame until this snapshot's wait has passed.
  const [released, setReleased] = useState<unknown>(undefined);
  const waiting = waitMs > 0 && released !== snapshot;
  useEffect(() => {
    if (!waitMs) return;
    const timer = setTimeout(() => setReleased(snapshot), waitMs);
    return () => clearTimeout(timer);
  }, [snapshot, waitMs]);
  if (!notice) return null;
  const t = (id: MessageId) => defaultI18n.t(id);
  const line = title ?? t(notice.message), retryable = notice.retryable;
  return <div className={styles.notice} role="alert">
    <Icon name="i-warn" size={20} />
    <div>
      <b>{line}</b>
      {body && <span>{body}</span>}
      {retryable && <div className={styles.actions}>
        <Button variant="primary" size="xs" className={styles.action} disabled={waiting} onClick={() => action.retry()}>{t('next.states.tryAgain')}</Button>
      </div>}
    </div>
  </div>;
}
