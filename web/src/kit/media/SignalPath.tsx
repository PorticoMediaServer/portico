import {useOverlayPortalTarget} from '../overlays/portal-target';
import {useRef, useState, type RefObject} from 'react';
import {Dialog, Popover} from 'radix-ui';
import {formatSignalText} from '@core/music-signal.ts';
import {defaultI18n} from '@i18n';
import {Icon} from '../Icon';
import {SignalLine} from './SignalLine';
import type {MediaLayout, SignalPathModel, SignalTranslate} from './types';
import styles from './media.module.css';

export type SignalPathProps = Readonly<{
  model: SignalPathModel; translate: SignalTranslate; layout?: MediaLayout; locale?: string;
  open: boolean; onOpenChange(open: boolean): void;
  anchorRef: RefObject<HTMLButtonElement | null>;
}>;

function SignalSteps({model, translate, locale = defaultI18n.locale}: Pick<SignalPathProps, 'model' | 'translate' | 'locale'>) {
  return <ol className={styles.signalSteps}>{model.steps.map(step => <li key={step.id} data-changed={step.changed || undefined}>
    <span className={styles.stepLabel}>{typeof step.label === 'string' ? step.label : formatSignalText(step.label, translate)}</span>
    <span className={styles.stepValue}>{step.percent !== undefined && Number.isFinite(step.percent) && step.percent >= 0 && step.percent <= 1 ? new Intl.NumberFormat(locale, {style: 'percent', maximumFractionDigits: 0}).format(step.percent) : typeof step.value === 'string' ? step.value : step.value ? formatSignalText(step.value, translate) : undefined}{step.note && <small>{formatSignalText(step.note, translate)}</small>}</span>
  </li>)}</ol>;
}

/** Facts and localized decisions come from the canonical Music formatter, never persisted intent. */
export function SignalPath({model, translate, locale, layout = 'desktop', open, onOpenChange, anchorRef}: SignalPathProps) {
  const portalTarget = useOverlayPortalTarget();
  const done = useRef<HTMLButtonElement>(null);
  const title = translate('player.signal.title');
  const pill = <span className={styles.signalPill} data-healthy={model.healthy || undefined}>{formatSignalText(model.pill, translate)}</span>;
  const close = () => onOpenChange(false);
  const restore = (event: Event) => { event.preventDefault(); anchorRef.current?.focus({preventScroll: true}); };
  const content = <>
    {layout === 'tv' ? <><p className={styles.signalContext}>{translate('player.queueNowPlaying')}</p><h2>{title}</h2>{pill}</> : <header className={styles.signalHeader}><b>{title}</b>{pill}{layout === 'desktop' && <button type="button" className={styles.close} onClick={close} aria-label={translate('next.states.close')}><Icon name="i-x" size={18} /></button>}</header>}
    <SignalSteps model={model} translate={translate} locale={locale} />
    {layout === 'tv' && <footer className={styles.signalFooter}><button type="button" ref={done} onClick={close}>{translate('action.done')}</button></footer>}
  </>;
  if (layout === 'desktop') return <Popover.Root open={open} onOpenChange={onOpenChange}>
    <Popover.Anchor virtualRef={{current: {getBoundingClientRect: () => anchorRef.current?.getBoundingClientRect() ?? new DOMRect()}}} />
    <Popover.Portal container={portalTarget}><Popover.Content className={styles.signalPopover} side="top" align="start" sideOffset={4} alignOffset={-12} collisionPadding={8} role="dialog" aria-label={title} onCloseAutoFocus={restore}>{content}</Popover.Content></Popover.Portal>
  </Popover.Root>;
  return <Dialog.Root open={open} onOpenChange={onOpenChange}><Dialog.Portal container={portalTarget}>
    <Dialog.Overlay className={styles.signalScrim} />
    <Dialog.Content className={styles.signalDialog} data-layout={layout} aria-describedby={undefined} onCloseAutoFocus={restore} onOpenAutoFocus={event => { if (layout === 'tv') { event.preventDefault(); done.current?.focus(); } }}>
      <Dialog.Title className={styles.srOnly}>{title}</Dialog.Title>
      {layout === 'phone' && <button type="button" className={styles.grabber} onClick={close} aria-label={translate('next.states.close')} onPointerDown={event => { event.currentTarget.dataset.startY = String(event.clientY); event.currentTarget.setPointerCapture(event.pointerId); }} onPointerUp={event => { if (event.clientY - Number(event.currentTarget.dataset.startY) > 40) close(); }}><i /></button>}
      {content}
    </Dialog.Content>
  </Dialog.Portal></Dialog.Root>;
}

/** Optional independent composition for an existing Now playing title column. */
export function MusicSignal({model, translate, locale, layout = 'desktop'}: Pick<SignalPathProps, 'model' | 'translate' | 'layout' | 'locale'>) {
  const [open, setOpen] = useState(false), anchor = useRef<HTMLButtonElement>(null);
  return <><SignalLine model={model} translate={translate} layout={layout} ref={anchor} aria-haspopup="dialog" aria-expanded={open} onClick={() => setOpen(true)} /><SignalPath model={model} translate={translate} locale={locale} layout={layout} open={open} onOpenChange={setOpen} anchorRef={anchor} /></>;
}
