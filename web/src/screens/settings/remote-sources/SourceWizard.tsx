import {useId, useLayoutEffect, useRef, type ReactNode, type RefObject} from 'react';
import {defaultI18n} from '@i18n';
import {Button} from '../../../kit/controls';
import {Dialog} from '../../../kit/overlays';
import {useWindowWidth} from '../../../kit/theme';
import {Icon} from '../../../kit/Icon';
import {WizardStepHeader} from './WizardStepHeader';
import styles from './wizard-step-header.module.css';

export type SourceWizardStep = Readonly<{title: string; detail?: string}>;

/** Desktop keeps the drawn page and completed-choice rail; compact windows
 * retain the same wizard owner and the five-segment phone header. */
export function SourceWizard({open, title, currentTitle, step, steps, onClose, footer, children, returnFocusRef}: Readonly<{
  open: boolean; title: string; currentTitle: string; step: number; steps: readonly SourceWizardStep[];
  onClose: () => void; footer: ReactNode; children: ReactNode;
  returnFocusRef?: RefObject<HTMLButtonElement | null>;
}>) {
  const width = useWindowWidth(), phone = width < 600, medium = width >= 600 && width < 1024, id = useId();
  const heading = useRef<HTMLHeadingElement>(null), wasOpen = useRef(false);
  useLayoutEffect(() => {
    if (open && !phone && !medium) heading.current?.focus();
    else if (!open && wasOpen.current && returnFocusRef?.current?.isConnected) returnFocusRef.current.focus();
    wasOpen.current = open;
  }, [open, phone, medium, returnFocusRef]);
  if (phone) return <Dialog open={open} returnFocusRef={returnFocusRef} onOpenChange={value => {if (!value) onClose();}} title={title} description={currentTitle || undefined} footer={footer}>
    {step < steps.length && <WizardStepHeader step={step} count={steps.length} title={currentTitle}/>}
    {children}
  </Dialog>;
  const wizard = <div className={styles.wizard}>
      <ol className={styles.rail} aria-label={title}>{steps.map((item, index) => <li key={index} aria-current={index + 1 === step ? 'step' : undefined} data-complete={index + 1 < step || undefined}>
        <span className={styles.number} aria-hidden="true">{index + 1 < step ? <Icon name="i-check" size={16}/> : index + 1}</span>
        <span className={styles.stepText}><b>{item.title}</b>{index + 1 < step && item.detail && <small>{item.detail}</small>}</span>
      </li>)}</ol>
      <div className={styles.main}><div className={styles.body}>{currentTitle && <h3>{currentTitle}</h3>}{children}</div><footer className={styles.footer}>{footer}</footer></div>
  </div>;
  // The Settings master pane is 280px on medium windows. Give this page the
  // window rather than squeezing a second 252px rail into the detail pane.
  if (medium) return <Dialog responsive={false} open={open} returnFocusRef={returnFocusRef} onOpenChange={value => {if (!value) onClose();}}
    title={title} className={styles.tabletPage} bodyClassName={styles.tabletBody}>{wizard}</Dialog>;
  if (!open) return null;
  return <section className={styles.page} aria-labelledby={id}>
    <header className={styles.pageHeader}><h2 ref={heading} tabIndex={-1} id={id}>{title}</h2>
      <Button size="small" variant="icon" iconOnly icon="i-x" aria-label={defaultI18n.t('next.overlays.close')} onClick={onClose}/>
    </header>
    {wizard}
  </section>;
}
