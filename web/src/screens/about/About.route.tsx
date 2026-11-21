import {useShellFacts} from '../../shell/fact-hooks';
import {useState} from 'react';
import {defaultI18n} from '@i18n';
import {defineWebScreen} from '../../route-registry';
import {useProtectedWebScreenContext, useWebAction} from '../../route-registry-view';
import {currentWebEnvironment} from '../../platform/environment';
import {Button} from '../../kit/controls';
import {SectionCard, SettingsPageFrame} from '../../kit/frames';
import {SettingsRow} from '../../kit/shell/SettingsSurfaces';
import {Licences} from '../../kit/states/Licences';
import {router} from '../../router';
import tmdbLogo from '../../kit/assets/tmdb.svg';
import styles from './about.module.css';
const t = defaultI18n.t;
function About() {
  const captured = useProtectedWebScreenContext(), feedback = useWebAction('feedback');
  useShellFacts(captured?.context);
  const [licenses, setLicenses] = useState(false);
  if (!captured) return null;
  const identity = captured.context.facts.identity;
  // O-169 (C7): the web bundle carries no build id. The server version comes
  // from the signed-in system read (it is never served before sign-in).
  const {appVersion: version} = currentWebEnvironment();
  const link = (href: string, label: string) => <SettingsRow label={label}><a className={styles.link} href={href} target="_blank" rel="noopener noreferrer">{href.replace(/^https:\/\//, '').replace(/\/$/, '')}</a></SettingsRow>;
  // This device's About on the settings page frame (desktop-settings, phone-app 16's pushed page): the versions, the
  // licences and source, help and the legal pages, Send feedback, then the data credits.
  return <SettingsPageFrame title={t('settings.about')} intro={t('licenses.portico.body')}
    back={{label: t('next.settingsSpec.account.device.title'), onPress: () => {void router.navigate({to: '/settings', search: {scope: 'account', section: 'device'}});}}}>
    <SectionCard>
      <SettingsRow label={t('web.about.webApp')}><span className={styles.value}>{version || t('next.states.versionNotRecorded')}</span></SettingsRow>
      <SettingsRow label={t('web.about.server')}><span className={styles.value}>{identity.serverName}</span></SettingsRow>
      <SettingsRow label={t('web.about.serverVersion')}><span className={styles.value}>{identity.serverVersion || t('next.states.versionNotRecorded')}</span></SettingsRow>
      {identity.serverApiVersion && <SettingsRow label={t('web.about.serverApi')}><span className={styles.value}>{identity.serverApiVersion}</span></SettingsRow>}
    </SectionCard>
    <SectionCard>
      <SettingsRow label={t('licenses.title')} help={t('licenses.rowHelp')}><Button variant="icon" size="small" iconOnly icon="i-right" aria-label={t('licenses.title')} onClick={() => setLicenses(true)}/></SettingsRow>
      {link('https://github.com/PorticoMediaServer/portico-server', t('licenses.source.title'))}
    </SectionCard>
    <SectionCard>
      {link('https://getportico.tv/docs/', t('web.about.help'))}
      {link('https://getportico.tv/terms', t('web.about.terms'))}
      {link('https://getportico.tv/privacy', t('web.about.privacy'))}
    </SectionCard>
    {feedback && <SectionCard><SettingsRow label={t('next.shell.feedback')}><Button variant="icon" size="small" iconOnly icon="i-right" aria-label={t('next.shell.feedback')} onClick={feedback}/></SettingsRow></SectionCard>}
    <p className={styles.legal}><img src={tmdbLogo} alt="TMDB" height={16}/>{t('web.about.legal')} {t('web.about.tmdb')}</p>
    <p className={styles.legal}>{t('about.datasetCredit')}</p>
    <Licences open={licenses} onOpenChange={setLicenses}/>
  </SettingsPageFrame>;
}
export default defineWebScreen({id: 'about', component: About});
