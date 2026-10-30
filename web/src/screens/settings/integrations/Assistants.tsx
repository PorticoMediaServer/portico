import {useEffect, useRef, useState} from 'react';
import {defaultI18n} from '@i18n';
import {assistantAddress} from '@core/server-admin/api-words.ts';
import {Button} from '../../../kit/controls';
import {Icon} from '../../../kit/Icon';
import {SettingsRow} from '../../../kit/shell/SettingsSurfaces';
import styles from './assistants.module.css';

const t = defaultI18n.t;

/** The current server's MCP address and the way to the existing keys surface.
 * The drawn row is a link, never a second Create key button; no credential is
 * created, cached or inferred by this presentation. */
export function AssistantsPanel({baseUrl, isCurrent, openKeys, reportError}: Readonly<{
  baseUrl: string;
  isCurrent: () => boolean;
  openKeys: () => void | Promise<void>;
  reportError: (error: unknown) => void;
}>) {
  const address = assistantAddress(baseUrl), [copied, setCopied] = useState(false), generation = useRef({active: false});
  useEffect(() => {
    const epoch = {active: true}; generation.current = epoch; setCopied(false);
    return () => {epoch.active = false;};
  }, [baseUrl]);
  const copy = async () => {
    if (!isCurrent()) return;
    const held = generation.current;
    try {
      await navigator.clipboard.writeText(address);
      if (isCurrent() && held.active && held === generation.current) setCopied(true);
    } catch (error) { if (isCurrent() && held.active && held === generation.current) reportError(error); }
  };
  const showKeys = () => {
    if (!isCurrent()) return;
    try { void Promise.resolve(openKeys()).catch(error => { if (isCurrent()) reportError(error); }); }
    catch (error) { if (isCurrent()) reportError(error); }
  };
  return <section className={styles.section} aria-labelledby="assistants-title">
    <h3 id="assistants-title">{t('api.mcp.title')}</h3>
    <p className={styles.lede}>{t('api.mcp.lede')}</p>
    <div className={styles.box}>
      <SettingsRow stacked label={t('api.mcp.address')} help={t('api.mcp.addressHelp')}>
        <div className={styles.address}><code>{address}</code><Button size="xs" icon="i-copy" iconSize={17} onClick={() => { void copy(); }}>{t('web.twoStep.copy')}</Button>
          <span className={styles.copied} role="status">{copied && <><Icon name="i-check" size={16} />{t('api.credential.copied')}</>}</span></div>
      </SettingsRow>
      <SettingsRow label={t('api.mcp.key')} help={t('api.mcp.keyHelp')}><Button size="xs" variant="text" onClick={showKeys}>{t('next.settingsSpec.server.integrations.groups.2.rows.1.control.label')}</Button></SettingsRow>
    </div>
  </section>;
}
