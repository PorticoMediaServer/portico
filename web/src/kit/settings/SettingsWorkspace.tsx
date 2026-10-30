import type {ReactNode} from 'react';
import {defaultI18n} from '@i18n';
import {Icon} from '../Icon';
import {PushedNavBar} from '../shell/SettingsSurfaces';
import styles from './settings.module.css';

export function SettingsWorkspace({navigation, children, scope, compactPage = false, onBack}: {navigation: ReactNode; children: ReactNode; scope: 'account' | 'server'; compactPage?: boolean; onBack: () => void}) {
  return <div className={styles.workspace} data-scope={scope} data-compact-page={compactPage || undefined}>
    <div className={styles.master}>{navigation}</div>
    <div className={styles.detail}><div className={styles.compactBack}><PushedNavBar backLabel={defaultI18n.t('next.shell.settings')} onBack={onBack} safeArea={false} /></div>{children}</div>
  </div>;
}
export function SettingsPageHeading({title, saveState, actions}: {title: string; saveState?: 'saving' | 'saved'; actions?: ReactNode}) {
  return <header className={styles.pageHeading}><h1>{title}</h1><span className={styles.pageSaved} role="status">{saveState === 'saving' ? defaultI18n.t('next.settings.saving') : saveState === 'saved' ? <><Icon name="i-check" size={16} />{defaultI18n.t('next.settings.saved')}</> : null}</span>{actions}</header>;
}
