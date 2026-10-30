import {useLayoutEffect, useRef, useState, type RefObject} from 'react';
import {Icon} from '../../kit/Icon';
import {BottomSheet} from '../../kit/overlays/BottomSheet';
import {Dialog} from '../../kit/overlays/Dialog';
import type {OverlayChange} from '../../kit/overlays/types';
import {dispatchWatchVersion, watchVersionRows, type WatchVersionBinding, type WatchVersionSelection} from './versions';
import styles from './version-chooser.module.css';

export type WatchVersionChooserProps<Choices extends WatchVersionSelection = WatchVersionSelection> = Readonly<{
  binding: WatchVersionBinding<Choices>;
  open: boolean;
  onOpenChange: OverlayChange;
  /** Canonical localized §21 title, without the menu action's ellipsis. */
  title: string;
  subject: string;
  layout: 'desktop' | 'phone';
  returnFocusRef?: RefObject<HTMLElement | null>;
}>;

/** desktop-detail 9b / phone-detail 9b. One modal; row activation starts immediately. */
export function WatchVersionChooser<Choices extends WatchVersionSelection>({binding, open, onOpenChange, title, subject, layout, returnFocusRef}: WatchVersionChooserProps<Choices>) {
  const rows = watchVersionRows(binding), first = useRef<HTMLButtonElement | null>(null);
  const pending = useRef<AbortController | null>(null), [busy, setBusy] = useState(false);
  const active = useRef({open, choices: binding.choices}); active.current = {open, choices: binding.choices};
  useLayoutEffect(() => {
    setBusy(false);
    return () => { pending.current?.abort(); pending.current = null; };
  }, [open, binding.choices]);
  const choose = async (versionId: string) => {
    if (pending.current || !active.current.open) return;
    const choices = binding.choices, abort = new AbortController(); pending.current = abort; setBusy(true);
    try {
      const started = await dispatchWatchVersion(binding, versionId, abort.signal);
      if (started && active.current.open && active.current.choices === choices) onOpenChange(false, 'action');
    } catch (error) {
      if (!abort.signal.aborted && active.current.open && active.current.choices === choices) binding.reportError(error);
    } finally {
      if (pending.current === abort) { pending.current = null; setBusy(false); }
    }
  };
  if (!rows?.length || !title || !subject) return null;
  const initialId = rows.find(row => row.id === binding.choices.selectedVersionId)?.id ?? rows[0]!.id;
  const body = <div className={styles.list} data-layout={layout} aria-busy={busy || undefined}>{rows.map(row => <button key={row.id} ref={row.id === initialId ? first : undefined} type="button" className={styles.row} data-version-id={row.id} aria-disabled={busy || undefined} onClick={() => { void choose(row.id); }}>
    <span className={styles.text}><b>{row.heading}</b>{row.detail && <small>{row.detail}</small>}</span>
    {layout === 'desktop' && <span className={styles.play} aria-hidden="true"><Icon name="i-play" size={18} /></span>}
  </button>)}</div>;
  const surface = {open, onOpenChange, initialFocusRef: first, returnFocusRef};
  if (layout === 'phone') return <BottomSheet {...surface} title={<>{title}<small className={styles.subject}>{subject}</small></>} fitContent headerClassName={styles.sheetHeader} bodyClassName={styles.sheetBody}>{body}</BottomSheet>;
  return <Dialog {...surface} title={title} description={subject} responsive={false} width={520} className={styles.dialog} headerClassName={styles.dialogHeader} bodyClassName={styles.dialogBody}>{body}</Dialog>;
}
