import {useLayoutEffect, useRef} from 'react';
import {defaultI18n} from '@i18n';
import {Button} from '../../kit/controls';
import {Icon} from '../../kit/Icon';
import styles from './reader-hint.module.css';

const {t} = defaultI18n;

/** phone-read 14: presentation only; the current engine supplies direction and gesture availability. */
export function ReaderFirstRunHint({onDone, direction = 'ltr', swipe = false}: {onDone: () => void; direction?: 'ltr' | 'rtl'; swipe?: boolean}) {
  const done = useRef<HTMLButtonElement>(null);
  useLayoutEffect(() => {
    const button = done.current, root = button?.closest<HTMLElement>('[data-reader-root]');
    const previous = document.activeElement instanceof HTMLElement && root?.contains(document.activeElement) ? document.activeElement : root;
    button?.focus({preventScroll: true});
    return () => {
      // The same commit removes inert from the reader. Do not restore focus into a
      // retired book or steal it from a replacement hint during a strict replay.
      queueMicrotask(() => {
        if (!root?.isConnected || root.querySelector('[data-reader-hint]')) return;
        const focused = document.activeElement;
        if (focused instanceof HTMLElement && focused !== document.body && focused !== root && focused !== button && focused.isConnected) return;
        const target = previous?.isConnected && !previous.closest('[inert]') ? previous : root;
        target.focus({preventScroll: true});
      });
    };
  }, []);
  return <div className={styles.hint} role="dialog" aria-modal="true" aria-label={t('next.reader.hintTitle')} data-reader-hint
    onKeyDown={event => {
      event.stopPropagation();
      if (event.key === 'Tab') {event.preventDefault(); done.current?.focus({preventScroll: true});}
      else if (event.key === 'Escape') {event.preventDefault(); onDone();}
    }}>
    <div className={styles.zones} dir="ltr">
      <div className={styles.zone}><Icon name="i-left" size={26}/><span>{t(direction === 'rtl' ? 'next.reader.next' : 'next.reader.previous')}</span><small>{t('next.reader.hintLeftThird')}</small></div>
      <div className={`${styles.zone} ${styles.center}`}><Icon name="i-tap" size={24}/><span>{t('next.reader.hintShowControls')}</span><small>{t('next.reader.hintMiddle')}</small></div>
      <div className={styles.zone}><Icon name="i-right" size={26}/><span>{t(direction === 'rtl' ? 'next.reader.previous' : 'next.reader.next')}</span><small>{t('next.reader.hintRightThird')}</small></div>
    </div>
    <div className={styles.foot}>
      <div className={styles.instructions}>
      {swipe && <div className={styles.instruction}><Icon name="i-swipe" size={22}/><span>{t('next.reader.hintSwipe')}</span></div>}
      <div className={styles.instruction}><Icon name="i-hold" size={22}/><span>{t('next.reader.hintSelect')}</span></div>
      </div>
      <Button ref={done} variant="primary" onClick={onDone}>{t('next.reader.hintGotIt')}</Button>
    </div>
  </div>;
}
