import {useId, useRef, type ReactNode, type RefObject} from 'react';
import {defaultI18n} from '@i18n';
import {Dialog} from '../overlays/Dialog';
import {Tabs} from '../controls/Choices';
import {Button} from '../controls/Button';
import {Icon} from '../Icon';
import {ServerMark} from '../shell/ServerIdentity';
import styles from './settings.module.css';

const t = defaultI18n.t;
export type SettingsPanelTab = Readonly<{id: string; title: string; content: ReactNode; keepMounted?: boolean}>;
export type SettingsPanelProps = Readonly<{
  open: boolean; onClose: () => void; title: string; description?: string; audience: string;
  tabs?: readonly SettingsPanelTab[]; selectedTab?: string; onTabChange?: (id: string) => void;
  children?: ReactNode; saveState?: 'saving' | 'saved' | 'error'; error?: ReactNode;
  returnFocusRef?: RefObject<HTMLElement | null>; onClosed?: () => void;
  dismissOnBack?: boolean; showCloseButton?: boolean;
  /** Supplying this is explicit create mode. Existing editors never have a footer. */
  create?: Readonly<{label: string; onCreate: () => void; pending: boolean; disabled?: boolean; cancelLabel?: string}>;
  /** Only a drawn consequential preview supplies this; ordinary edits save as they go. */
  confirm?: Readonly<{label: string; onConfirm: () => void; pending: boolean; disabled?: boolean; destructive?: boolean; cancelDisabled?: boolean}>;
}>;

export function SettingsPanel({open, onClose, title, description, audience, tabs, selectedTab, onTabChange, children, saveState, error, returnFocusRef, create, confirm, dismissOnBack, showCloseButton}: SettingsPanelProps) {
  const id = useId(), initial = useRef<HTMLElement | null>(null);
  return <Dialog open={open} onOpenChange={value => {if (!value) onClose();}} title={<>
    <span className={styles.panelIdentity}><ServerMark size={34} /><span><b>{t('next.shell.server')}</b><small>{audience}</small></span></span>
    <span className={styles.panelTitle}><span className={styles.panelTitleText}>{title}</span><span className={styles.saveState} role="status" data-state={saveState}>{saveState === 'saving' ? t('next.settings.saving') : saveState === 'saved' ? <><Icon name="i-check" size={15} />{t('next.settings.saved')}</> : null}</span></span>
  </>} description={description} placement="right" width={720} className={[styles.panel, tabs?.length ? styles.tabbedPanel : undefined].filter(Boolean).join(' ')} headerClassName={styles.panelHeader} bodyClassName={styles.panelBody} footerClassName={styles.panelFooter}
    returnFocusRef={returnFocusRef} initialFocusRef={tabs?.length ? initial : undefined} dismissOnOutsidePress={!create && !confirm} dismissOnBack={dismissOnBack} showCloseButton={showCloseButton} footer={create ? <><Button size="compact" disabled={create.pending} onClick={onClose}>{create.cancelLabel ?? t('next.settings.cancel')}</Button><Button size="compact" variant="primary" loading={create.pending} disabled={create.disabled} onClick={create.onCreate}>{create.label}</Button></> : confirm ? <><Button size="compact" disabled={confirm.pending || confirm.cancelDisabled} onClick={onClose}>{t('next.settings.cancel')}</Button><Button size="compact" variant="primary" className={confirm.destructive ? styles.danger : undefined} loading={confirm.pending} disabled={confirm.disabled} onClick={confirm.onConfirm}>{confirm.label}</Button></> : undefined}>
    {error && <div className={styles.error} role="alert">{error}</div>}
    {tabs?.length ? <div ref={element => {initial.current = element?.querySelector<HTMLElement>('[role="tab"][data-state="active"]') ?? null;}}><Tabs aria-label={title} value={selectedTab ?? tabs[0].id} onValueChange={onTabChange} listClassName={styles.panelTabs} items={tabs.map(tab => ({value: tab.id, label: tab.title, keepMounted: tab.keepMounted, panel: <div id={`${id}-${tab.id}`} className={styles.panelTabBody}>{tab.content}</div>}))} /></div> : children}
  </Dialog>;
}
