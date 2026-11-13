import {createContext, useCallback, useContext, useEffect, useId, useRef, type ReactNode, type RefObject} from 'react';
import {Icon} from '../Icon';
import {BottomSheet} from '../overlays/BottomSheet';
import {usePhone, usePresence, useSurfaceMotion} from '../overlays/shared';
import {observedProgress} from './interaction';
import {ReaderSheetClose} from './ReaderSheetClose';
import styles from './panel.module.css';

export type ReaderPanelTab = Readonly<{id: string; label: string; count?: string}>;
export type ReaderPanelProps = Readonly<{
  label: string; tabs: readonly ReaderPanelTab[]; selected: string;
  onSelect: (id: string) => void; onClose: () => void; children: ReactNode; footer?: ReactNode;
  returnFocusRef?: RefObject<HTMLElement | null>;
}>;

/** The chrome keeps a closed panel mounted for its exit; it reports whether the panel is still wanted. */
export const ReaderPanelOpenContext = createContext(true);

/** desktop-read 7/8, phone-read 6/7, tablet-read 7/7b. Copy and data stay with assembly. */
export function ReaderPanel({label, tabs, selected, onSelect, onClose, children, footer, returnFocusRef}: ReaderPanelProps) {
  const compact = usePhone(), id = useId(), region = useRef<HTMLElement | null>(null);
  const open = useContext(ReaderPanelOpenContext), present = usePresence(open, 120), motionRef = useSurfaceMotion(open, 'panel');
  const ref = useCallback((element: HTMLElement | null) => { region.current = element; motionRef(element); }, [motionRef]);
  useEffect(() => {
    if (compact || !open) return;
    const opener = returnFocusRef?.current ?? (document.activeElement instanceof HTMLElement ? document.activeElement : null);
    const current = region.current;
    current?.querySelector<HTMLButtonElement>('[aria-selected="true"]')?.focus();
    return () => { if (document.activeElement === document.body || current?.contains(document.activeElement)) opener?.focus({preventScroll: true}); };
  }, [compact, open, returnFocusRef]);
  const content = <>
    <div className={styles.tabs} role="tablist" aria-label={label} onKeyDown={event => {
      if (!['ArrowLeft', 'ArrowRight', 'Home', 'End'].includes(event.key)) return;
      const index = tabs.findIndex(tab => tab.id === selected);
      if (index < 0 || tabs.length === 0) return;
      event.preventDefault();
      const next = event.key === 'Home' ? 0 : event.key === 'End' ? tabs.length - 1 : (index + (event.key === 'ArrowRight' ? 1 : -1) + tabs.length) % tabs.length;
      onSelect(tabs[next].id); event.currentTarget.querySelectorAll<HTMLButtonElement>('[role="tab"]')[next]?.focus();
    }}>{tabs.map((tab, index) => <button key={tab.id} id={`${id}-tab-${index}`} type="button" role="tab" aria-selected={tab.id === selected}
      aria-controls={`${id}-body`} tabIndex={tab.id === selected ? 0 : -1} onClick={() => onSelect(tab.id)}>{tab.label}{tab.count !== undefined && <span>{tab.count}</span>}</button>)}</div>
    <div id={`${id}-body`} className={styles.body} role="tabpanel" aria-labelledby={`${id}-tab-${Math.max(0, tabs.findIndex(tab => tab.id === selected))}`}>{children}</div>
  </>;
  if (!present) return null;
  if (compact) return <BottomSheet open={open} onOpenChange={open => { if (!open) onClose(); }} title={label} accessibilityLabel={label} returnFocusRef={returnFocusRef}
    showCloseButton={false} className={styles.sheet} headerClassName={styles.sheetHeader} bodyClassName={styles.sheetBody} footerClassName={styles.footer} footer={footer}>
    <ReaderSheetClose onClose={onClose}/>{content}</BottomSheet>;
  return <aside ref={ref} className={styles.panel} data-reader-panel aria-label={label} data-state={open ? 'open' : 'closed'} inert={!open} aria-hidden={!open} onKeyDown={event => { if (event.key === 'Escape' && !event.defaultPrevented) { event.preventDefault(); event.stopPropagation(); onClose(); } }}>
    {content}{footer && <footer className={styles.footer}>{footer}</footer>}
  </aside>;
}

/** Presentation entries contain navigation callbacks, never a parallel annotation store. */
export type ReaderContentsEntry = Readonly<{
  id: string; label: string; page?: string; current?: boolean; finished?: boolean; progress?: number;
  /** Literal renderer-observed remaining position, never an outline estimate. */
  remaining?: string;
  onNavigate: () => void; children?: readonly ReaderContentsEntry[];
}>;
export function ReaderContents({entries}: Readonly<{entries: readonly ReaderContentsEntry[]}>) {
  return <div className={styles.contents}>{entries.map(entry => {
    const progress = observedProgress(entry.progress);
    return <div key={entry.id}><button type="button" className={styles.chapter} data-current={entry.current || undefined} data-finished={entry.finished || undefined}
      aria-current={entry.current ? 'location' : undefined} onClick={entry.onNavigate}>
      {entry.finished && <Icon name="i-check" size={18} />}<span className={styles.chapterName}>{entry.label}</span>{entry.page && <span className={styles.page}>{entry.page}</span>}
      {entry.current && (progress !== undefined || entry.remaining) && <span className={styles.chapterStatus}>
        {progress !== undefined && <span className={styles.chapterProgress} data-with-remaining={!!entry.remaining || undefined} aria-hidden="true"><i style={{width: `${progress * 100}%`}} /></span>}
        {entry.remaining && <span>{entry.remaining}</span>}
      </span>}
    </button>{entry.children?.length ? <div className={styles.nested}><ReaderContents entries={entry.children} /></div> : null}</div>;
  })}</div>;
}

export type ReaderAnnotationEntry = Readonly<{
  id: string; quote: string; note?: string; chapter?: string; page?: string; date?: string;
  colour: 'yellow' | 'blue' | 'green' | 'pink'; onNavigate: () => void;
  onMore?: (anchor: HTMLButtonElement) => void; moreLabel?: string;
}>;
export function ReaderAnnotations({entries}: Readonly<{entries: readonly ReaderAnnotationEntry[]}>) {
  return <div>{entries.map(entry => <div key={entry.id} className={styles.annotationWrap}>
    <button type="button" className={styles.annotation} data-colour={entry.colour} onClick={entry.onNavigate}
      onContextMenu={event => { if (!entry.onMore) return; event.preventDefault(); entry.onMore(event.currentTarget); }}
      onKeyDown={event => { if (entry.onMore && (event.key === 'ContextMenu' || event.shiftKey && event.key === 'F10')) { event.preventDefault(); entry.onMore(event.currentTarget); } }}>
      <q>{entry.quote}</q>{entry.note && <span className={styles.note}><Icon name="i-note" size={16} />{entry.note}</span>}
      <span className={styles.where}>{entry.chapter && <span>{entry.chapter}</span>}{entry.page && <span>{entry.page}</span>}{entry.date && <span className={styles.date}>{entry.date}</span>}</span>
    </button>
  </div>)}</div>;
}
