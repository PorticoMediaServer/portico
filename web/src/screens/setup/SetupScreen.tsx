import {useLayoutEffect, useRef, useState, useSyncExternalStore} from 'react';
import {RadioGroup} from 'radix-ui';
import {defaultI18n} from '@i18n';
import {FolderPicker} from '../settings/libraries/FolderPicker';
import {Button, TextField} from '../../kit/controls';
import {AuthLoading, AuthStage} from '../../kit/accounts/AuthStage';
import {ServicesStrength} from '../../kit/accounts/ServicesAuth';
import {Icon} from '../../kit/Icon';
import {addressSetupFailure, notLocalSourceFailure, setupInProgressFailure, unavailableSetupFailure} from '../../platform/setup-failure.ts';
import {SetupFlow, type SetupPorts, type SetupSnapshot} from './setup-flow';
import {libraryKindDescription, localPasswordValid, setupLibraryKinds, setupNameValid, setupSteps, validLocalUsername} from './setup-model';
import styles from './setup.module.css';

const t = defaultI18n.t;
type SetupFlowLease = Readonly<{ports: SetupPorts; flow: SetupFlow; key: number; current: () => boolean}>;

/** auth1 / auth1c / auth3. The mounted port bundle supplies the verified pairing,
 * the protected draft, shell admission and the guarded hosted continuation. */
export function SetupScreen({ports}: {ports: SetupPorts}) {
  const [published, publish] = useState<SetupFlowLease>(), generation = useRef(0);
  useLayoutEffect(() => {
    let live = true;
    const flow = new SetupFlow(ports);
    publish({ports, flow, key: ++generation.current, current: () => live});
    void flow.start();
    return () => {live = false;flow.dispose();};
  }, [ports]);
  const owned = published?.ports === ports && published.current() ? published : undefined;
  return owned ? <SetupBody key={owned.key} flow={owned.flow}/> : null;
}
function SetupBody({flow}: {flow: SetupFlow}) {
  const state = useSyncExternalStore(flow.subscribe, flow.getSnapshot);
  const local = state.mode === 'local';
  const preparing = state.phase === 'loading' || state.phase === 'waiting';
  // The panel belongs to the exact failure this render shows, never a later one.
  const errorFailure = state.phase === 'error' ? state.failure : undefined;
  const notLocal = state.phase === 'error' && (notLocalSourceFailure(errorFailure) || notLocalSourceFailure(state.connectError));
  const addressFailure = errorFailure && addressSetupFailure(errorFailure) ? errorFailure : undefined;
  const servicesFailure = errorFailure && unavailableSetupFailure(errorFailure) ? errorFailure : undefined;
  const steps = setupSteps({serverName: state.serverName, mode: state.mode, phase: state.phase === 'library' ? 'library' : 'form', local});
  return <div className={styles.host}>
    {state.phase === 'library' ? <FirstLibrary flow={flow} state={state}/> : <AuthStage legal={false} shape={preparing ? 'round' : 'side'} layout={preparing ? 'center' : 'column'} contentClassName={preparing ? styles.loadingContent : state.phase === 'form' ? styles.content : styles.contentLibrary}>
      {preparing && <AuthLoading>{t('web.setup.preparing')}</AuthLoading>}
      {state.phase === 'error' && (notLocal ? <>
        <p className={styles.error} role="alert"><Icon name="i-warn" size={20}/>{t('web.setup.notLocalSource')}</p>
      </> : addressFailure ? <>
        <p className={styles.error} role="alert"><Icon name="i-warn" size={20}/>{t('web.setup.notOnNetwork')}</p>
        <div className={styles.field}><TextField variant="auth" label={t('web.general.serverName')} value={state.name || state.serverName} readOnly/></div>
        <Button variant="primary" size="hero" disabled>{t('accounts.signIn.handoff')}</Button>
      </> : servicesFailure ? <>
        <p className={styles.error} role="alert"><Icon name="i-clock" size={20}/>{t('web.setup.servicesUnavailableBody')}</p>
        <div className={styles.btnRow}>
          {servicesFailure.retry && <Button variant="primary" size="hero" loading={state.busy} disabled={state.busy}
            onClick={() => void flow.retryFailure(servicesFailure)}>{t('action.tryAgain')}</Button>}
          {servicesFailure.useLocal && <Button variant="glass" size="hero" disabled={state.busy}
            onClick={() => void flow.useLocalFailure(servicesFailure)}>{t('web.setup.localSignIn')}</Button>}
        </div>
      </> : setupInProgressFailure(errorFailure) || setupInProgressFailure(state.connectError) ? <>
        <h1>{t('web.setup.welcome')}</h1>
        {state.error && <p className={styles.error} role="alert"><Icon name="i-clock" size={20}/>{state.error}</p>}
        <Button variant="primary" size="hero" loading={state.busy} disabled={state.busy} onClick={() => void flow.retry()}>{t('action.tryAgain')}</Button>
      </> : errorFailure ? <>
        <h1>{t('web.setup.welcome')}</h1>
        {state.error && <p className={styles.error} role="alert"><Icon name="i-warn" size={20}/>{state.error}</p>}
        {(errorFailure.retry || errorFailure.useLocal) && <div className={styles.btnRow}>
          {errorFailure.retry && <Button variant="primary" size="hero" loading={state.busy} disabled={state.busy}
            onClick={() => void flow.retryFailure(errorFailure)}>{t('action.tryAgain')}</Button>}
          {errorFailure.useLocal && <Button variant="glass" size="hero" disabled={state.busy}
            onClick={() => void flow.useLocalFailure(errorFailure)}>{t('web.setup.localSignIn')}</Button>}
        </div>}
      </> : <>
        <h1>{t('web.setup.welcome')}</h1>
        {state.error && <p className={styles.error} role="alert"><Icon name="i-warn" size={20}/>{state.error}</p>}
        <Button variant="primary" size="hero" onClick={() => void flow.retry()}>{t('action.tryAgain')}</Button>
      </>)}
      {state.phase === 'form' && <SetupForm flow={flow} state={state} local={local}/>}
    </AuthStage>}
    {state.phase === 'form' || state.phase === 'library' ? <SetupSteps steps={steps} label={t('web.setup.stepsLabel')}/> : null}
  </div>;
}

function SetupForm({flow, state, local}: {flow: SetupFlow; state: SetupSnapshot; local: boolean}) {
  const hosted = state.mode === 'hosted';
  const nameOk = setupNameValid(state.name), usernameOk = validLocalUsername(state.username), match = state.confirm === state.password;
  const canSubmit = nameOk && (hosted || usernameOk && localPasswordValid(state.password, state.confirm)) && !state.busy;
  const submit = () => {if (canSubmit) void (hosted ? flow.submitHosted() : flow.submitLocal());};
  return <form onSubmit={event => {event.preventDefault(); submit();}}>
    <h1 className={styles.one}>{t('web.setup.welcome')}</h1>
    <p className={styles.lede}>{t('web.setup.welcomeLede')}</p>
    {state.earlierExpired && <p className={styles.quiet}>{t('web.setup.earlierExpired')}</p>}
    <div className={styles.field}><TextField variant="auth" autoFocus label={t('web.general.serverName')} value={state.name} maxLength={80} required
      description={t('web.setup.serverNameHelp')} onChange={event => flow.setName(event.currentTarget.value)} disabled={state.busy}/></div>
    <div className={styles.legend}>{t('web.setup.howSignIn')}</div>
    <RadioGroup.Root orientation="vertical" aria-label={t('web.setup.howSignIn')} value={hosted ? 'hosted' : 'local'} disabled={state.busy}
      onValueChange={value => {if (value === 'hosted' || value === 'local') flow.setMode(value);}}>
    <RadioGroup.Item value="hosted" className={hosted ? styles.choice : styles.qopt}>
      {hosted && <span className={styles.choiceIcon} aria-hidden="true"><Icon name="i-person" size={22}/></span>}
      <span className={styles.choiceCopy}><b>{t('accounts.settings.portico')}<span className={styles.rec}>{t('web.setup.recommended')}</span></b>
        <p>{t('web.setup.porticoBody')}</p></span>
      <span className={hosted ? styles.rad : styles.radOff} aria-hidden="true"/>
    </RadioGroup.Item>
    <RadioGroup.Item value="local" className={hosted ? styles.qopt : styles.choice}>
      {!hosted && <span className={styles.choiceIcon} aria-hidden="true"><Icon name="i-server" size={22}/></span>}
      <span className={styles.qoptCopy}><b>{t('web.setup.localSignIn')}</b><br/>{t('web.setup.localSignInBody')}</span>
      <span className={!hosted ? styles.rad : styles.radOff} aria-hidden="true"/>
    </RadioGroup.Item>
    </RadioGroup.Root>
    {local && <>
      <div className={[styles.legend, styles.formLegend].join(' ')}>{t('web.setup.ownerTitle')}</div>
      <p className={styles.para}>{t('web.setup.ownerLede')}</p>
      <div className={styles.field}><TextField variant="auth" label={t('web.direct.username')} value={state.username} autoComplete="username"
        autoCapitalize="off" spellCheck={false} maxLength={64} required disabled={state.busy} description={t('web.setup.usernameHelp')}
        onChange={event => flow.setUsername(event.currentTarget.value)}/></div>
      <div className={styles.pair}>
        <div className={styles.field}><TextField variant="auth" type="password" label={t('web.account.password')} value={state.password}
          autoComplete="new-password" required disabled={state.busy} onChange={event => flow.setPassword(event.currentTarget.value)}/>
          <ServicesStrength password={state.password} id="setup-password-strength"/></div>
        <div className={styles.field}><TextField variant="auth" type="password" label={t('web.directJoin.confirm')} value={state.confirm}
          autoComplete="new-password" required disabled={state.busy} error={Boolean(state.confirm) && !match ? t('web.setup.mismatch') : undefined}
          onChange={event => flow.setConfirm(event.currentTarget.value)}/></div>
      </div>
    </>}
    {state.error && <p className={styles.error} role="alert"><Icon name="i-warn" size={20}/>{state.error}</p>}
    <div className={[styles.btnRow, local ? styles.btnRowTight : ''].filter(Boolean).join(' ')}>
      <Button variant="primary" size="hero" type="submit" loading={state.busy} disabled={!canSubmit}>
        {hosted ? t('accounts.signIn.handoff') : t('web.setup.createOwner')}
      </Button>
    </div>
    <p className={styles.quiet}>{hosted ? t('web.setup.tabTrip') : t('web.setup.plainHttp')}</p>
  </form>;
}

function SetupSteps({steps, label}: {steps: ReturnType<typeof setupSteps>; label: string}) {
  return <div className={styles.steps} aria-label={label}>
    {steps.map((step, index) => <div key={step.id} className={styles.step} data-state={step.state}>
      <span className={styles.stepNumber}>{step.state === 'done' ? <Icon name="i-check" size={18}/> : index + 1}</span>
      <span>{step.title}<small>{step.body}</small></span>
    </div>)}
  </div>;
}

function FirstLibrary({flow, state}: {flow: SetupFlow; state: SetupSnapshot}) {
  const [picking, setPicking] = useState(false);
  const folderApi = flow.libraryFolderApi();
  const ready = setupNameValid(state.libraryName) && (!!state.folders?.length || state.path.trim().length > 0) && !state.busy;
  return <AuthStage legal={false} contentClassName={styles.contentLibrary}>
    <p className={styles.okline}><Icon name="i-okc" size={20}/>{t('web.setup.youOwn', {server: state.serverName})}</p>
    <h1>{t('web.setup.firstLibrary')}</h1>
    <p className={styles.lede}>{t('web.setup.firstLibraryLede')}</p>
    <div className={styles.kinds} role="group" aria-label={t('web.setup.firstLibrary')}>
      {setupLibraryKinds.map(kind => <button key={kind.id} type="button" className={styles.kind} aria-pressed={state.kind === kind.id}
        onClick={() => flow.setKind(kind.id)} disabled={state.busy}>
        <Icon name={kind.icon} size={26}/>{t(kind.label)}
      </button>)}
    </div>
    <p className={styles.kindDescription}><b>{t(setupLibraryKinds.find(kind => kind.id === state.kind)!.label)}.</b> {libraryKindDescription(state.kind)}</p>
    <div className={styles.field}><TextField variant="auth" label={t('web.setup.folderName')} value={state.libraryName} maxLength={80}
      required disabled={state.busy} onChange={event => flow.setLibraryName(event.currentTarget.value)}/></div>
    <div className={styles.legend}>{t('web.setup.folders')}</div>
    {folderApi ? <>
      <div className={styles.setupFolders}>{state.folders?.map(path => {
        const folderClass = state.folderClasses?.[path];
        return <div key={path} className={styles.setupFolder}><Icon name="i-folder" size={20}/><span>{path}</span>
          {folderClass ? <small className={styles.folderClass}>{t(folderClass === 'network' ? 'web.libraries.networkShare' : 'web.libraries.localDisk')}</small> : null}
          <Button variant="text" size="small" icon="i-x" aria-label={`${t('next.ownerLibraries.explorer.removeChosen')} ${path}`} disabled={state.busy} onClick={() => flow.setFolders(state.folders?.filter(value => value !== path) ?? [], state.folderClasses)}/></div>;
      })}</div>
      <div className={styles.folderChoose}>
        <Button variant="glass" disabled={state.busy} onClick={() => setPicking(true)}>{t('next.ownerLibraries.explorer.choose')}</Button>
        <small>{t('web.setup.chooseFoldersHelp')}</small>
      </div>
      <FolderPicker open={picking && !state.busy} onOpenChange={setPicking} api={folderApi} onChoose={flow.setPath} onChooseMany={flow.setFolders} initialPaths={state.folders} serverName={state.serverName}/>
    </> : <div className={styles.field}><TextField variant="auth" label={t('web.setup.orTypePath')} value={state.path} required disabled={state.busy}
      autoComplete="off" spellCheck={false} onChange={event => flow.setPath(event.currentTarget.value)}/></div>}
    {state.error && <p className={styles.error} role="alert"><Icon name="i-warn" size={20}/>{state.error}</p>}
    <div className={styles.btnRow}>
      <Button variant="primary" size="hero" loading={state.busy} disabled={!ready} onClick={() => void flow.addLibrary()}>{t('web.setup.addLibraryScan')}</Button>
      <Button variant="glass" size="hero" disabled={state.busy} onClick={flow.skip}>{t('web.setup.skip')}</Button>
    </div>
  </AuthStage>;
}
