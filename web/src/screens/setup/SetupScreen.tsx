import {useEffect, useState, useSyncExternalStore, type ReactNode} from 'react';
import {RadioGroup} from 'radix-ui';
import {defaultI18n} from '@i18n';
import type {LibraryContentApi} from '@core/library-content.ts';
import type {SetupController} from '@core/setup-controller.ts';
import type {SetupView} from '@core/setup-machine.ts';
import {FolderPicker} from '../settings/libraries/FolderPicker';
import {Button, TextField} from '../../kit/controls';
import {AuthLoading, AuthStage} from '../../kit/accounts/AuthStage';
import {ServicesStrength} from '../../kit/accounts/ServicesAuth';
import {Icon} from '../../kit/Icon';
import {defaultLibraryName, defaultSetupServerName, libraryKindDescription, localPasswordValid, setupBusyMessage, setupEndedMessage, setupErrorMessage,
  setupFailureKind, setupLibraryKinds, setupNameValid, setupNewerMessage, setupSteps, validLocalUsername, type SetupLibraryKind} from './setup-model';
import styles from './setup.module.css';

const t = defaultI18n.t;
export type SetupScreenProps = Readonly<{
  controller: SetupController;
  /** The paired server's own name, once known. */
  serverName: () => string | undefined;
  /** The admitted owner's folder listing; absent until the owner is admitted. */
  libraryFolders?: LibraryContentApi;
  hostname?: string;
}>;
type Form = Readonly<{name?: string; username: string; password: string; confirm: string}>;
type Library = Readonly<{kind: SetupLibraryKind; name: string; path: string; folders?: readonly string[]; folderClasses?: Readonly<Record<string, 'local' | 'network'>>}>;

/** First run (auth frames 1, 1c, 1d and 3), drawn from the setup controller's snapshot. The server decides every phase;
 * this screen keeps only what the person is typing. */
export function SetupScreen(props: SetupScreenProps) {
  const {controller} = props;
  const view = useSyncExternalStore(controller.subscribe, controller.getSnapshot, controller.getSnapshot);
  const busy = useSyncExternalStore(controller.subscribe, controller.busy, controller.busy);
  const [form, setForm] = useState<Form>({username: '', password: '', confirm: ''});
  const [library, setLibrary] = useState<Library>({kind: 'movie', name: defaultLibraryName('movie'), path: ''});
  // Passwords never outlive the form that took them.
  useEffect(() => {if (view.kind !== 'form' && view.kind !== 'starting') setForm(old => old.password || old.confirm ? {...old, password: '', confirm: ''} : old);}, [view.kind]);
  const serverName = props.serverName() ?? '';
  const name = form.name ?? (('name' in view && view.name) || defaultSetupServerName(serverName, props.hostname ?? location.hostname));
  const mode = view.kind === 'form' && view.mode === 'local' || view.kind === 'starting' && view.mode === 'local' ? 'local' : 'hosted';
  const steps = setupSteps({serverName, mode, phase: view.kind === 'library' ? 'library' : 'form', local: mode === 'local'});
  const showSteps = view.kind === 'form' || view.kind === 'starting' || view.kind === 'library';
  return <div className={styles.host}>
    {view.kind === 'library'
      ? <FirstLibrary controller={controller} library={library} setLibrary={setLibrary} serverName={serverName} folders={props.libraryFolders} busy={busy}/>
      : <Stage view={view} controller={controller} form={form} setForm={setForm} name={name} mode={mode} library={library} setLibrary={setLibrary}
        serverName={serverName} folders={props.libraryFolders}/>}
    {showSteps ? <SetupSteps steps={steps} label={t('web.setup.stepsLabel')}/> : null}
  </div>;
}

function Stage({view, controller, form, setForm, name, mode, library, setLibrary, serverName, folders}: {view: SetupView; controller: SetupController; form: Form;
  setForm: (update: (old: Form) => Form) => void; name: string; mode: 'hosted' | 'local'; library: Library; setLibrary: (update: (old: Library) => Library) => void;
  serverName: string; folders?: LibraryContentApi}) {
  const back = <Button variant="glass" size="hero" onClick={controller.back}>{t('action.back')}</Button>;
  switch (view.kind) {
    case 'connecting': case 'admitting': case 'renewing':
      return <Loading/>;
    case 'awaiting_approval':
      // Back here from Portico Services without Return: offer Continue again, which asks the server for the same approval.
      return <AuthStage legal={false} contentClassName={styles.content}>
        <h1 className={styles.one}>{t('web.setup.welcome')}</h1>
        {view.declined && <p className={styles.quiet} role="status">{t('web.setup.declined')}</p>}
        <div className={styles.field}><TextField variant="auth" label={t('web.general.serverName')} value={view.name || name} readOnly/></div>
        <div className={styles.btnRow}><Button variant="primary" size="hero" onClick={controller.retry}>{t('accounts.signIn.handoff')}</Button>{back}</div>
        <p className={styles.quiet}>{t('web.setup.tabTrip')}</p>
      </AuthStage>;
    case 'finishing':
      return <AuthStage legal={false} shape="round" layout="center" contentClassName={styles.loadingContent}>
        <AuthLoading>{t('web.claim.finishingOnServer')}</AuthLoading>
        {view.slow && <p className={styles.quiet}>{t('web.claim.finishingSlow')}</p>}
      </AuthStage>;
    case 'exhausted':
      return <Message icon="i-clock" text={t('web.claim.finishingSlow')} actions={<Button variant="primary" size="hero" onClick={controller.retry}>{t('action.tryAgain')}</Button>}/>;
    case 'refused':
      if (view.reason === 'address_not_allowed') return <AddressRefused name={name}/>;
      return <Message icon="i-warn" text={view.reason === 'not_local_source' ? t('web.setup.notLocalSource') : setupErrorMessage({code: 'setup_complete', retryable: false})}/>;
    case 'busy_elsewhere':
      return <Message icon="i-clock" heading text={setupBusyMessage(view.retryAt)} actions={back}/>;
    case 'ended':
      return <Message icon="i-warn" heading text={setupEndedMessage(view.outcome, view.name || name)}
        actions={<><Button variant="primary" size="hero" onClick={controller.startAgain}>{t('action.tryAgain')}</Button>{back}</>}/>;
    case 'failed': {
      const kind = setupFailureKind(view);
      if (kind === 'address') return <AddressRefused name={name}/>;
      // 1d-3 and the ordinary error: Try again where the server says it is worth it, and the ordinary Back.
      return <Message icon={kind === 'services' ? 'i-clock' : 'i-warn'} heading={kind !== 'services'} text={setupErrorMessage(view.error)}
        actions={<>{view.retryable && <Button variant="primary" size="hero" onClick={controller.retry}>{t('action.tryAgain')}</Button>}{back}</>}/>;
    }
    case 'unknown':
      return <Message icon="i-warn" heading text={setupNewerMessage()} actions={<Button variant="primary" size="hero" onClick={controller.retry}>{t('action.tryAgain')}</Button>}/>;
    case 'form': case 'starting':
      return <AuthStage legal={false} contentClassName={styles.content}>
        <SetupForm controller={controller} view={view} form={form} setForm={setForm} name={name} mode={mode}/>
      </AuthStage>;
    case 'library': case 'done':
      return <Loading/>;
  }
}

const Loading = () => <AuthStage legal={false} shape="round" layout="center" contentClassName={styles.loadingContent}><AuthLoading>{t('web.setup.preparing')}</AuthLoading></AuthStage>;

function Message({icon, text, heading, actions}: {icon: 'i-warn' | 'i-clock'; text: string; heading?: boolean; actions?: ReactNode}) {
  return <AuthStage legal={false} contentClassName={styles.content}>
    {heading && <h1>{t('web.setup.welcome')}</h1>}
    <p className={styles.error} role="alert"><Icon name={icon} size={20}/>{text}</p>
    {actions && <div className={styles.btnRow}>{actions}</div>}
  </AuthStage>;
}

/** 1d-2: setup can't start from this address; the form stays visible and Continue is off. */
function AddressRefused({name}: {name: string}) {
  return <AuthStage legal={false} contentClassName={styles.content}>
    <p className={styles.error} role="alert"><Icon name="i-warn" size={20}/>{t('web.setup.notOnNetwork')}</p>
    <div className={styles.field}><TextField variant="auth" label={t('web.general.serverName')} value={name} readOnly/></div>
    <Button variant="primary" size="hero" disabled>{t('accounts.signIn.handoff')}</Button>
  </AuthStage>;
}

function SetupForm({controller, view, form, setForm, name, mode}: {controller: SetupController; view: Extract<SetupView, {kind: 'form' | 'starting'}>; form: Form;
  setForm: (update: (old: Form) => Form) => void; name: string; mode: 'hosted' | 'local'}) {
  const busy = view.kind === 'starting', hosted = mode === 'hosted', local = !hosted;
  const nameOk = setupNameValid(name), usernameOk = validLocalUsername(form.username), match = form.confirm === form.password;
  const canSubmit = nameOk && (hosted || usernameOk && localPasswordValid(form.password, form.confirm)) && !busy;
  const submit = () => {
    if (!canSubmit) return;
    if (hosted) controller.submitHosted(name.trim(), {navigate: true});
    else controller.submitLocal(name.trim(), form.username.trim(), form.password);
  };
  return <form onSubmit={event => {event.preventDefault(); submit();}}>
    <h1 className={styles.one}>{t('web.setup.welcome')}</h1>
    <p className={styles.lede}>{t('web.setup.welcomeLede')}</p>
    {view.kind === 'form' && view.earlierExpired && <p className={styles.quiet}>{t('web.setup.earlierExpired')}</p>}
    <div className={styles.field}><TextField variant="auth" autoFocus label={t('web.general.serverName')} value={name} maxLength={80} required
      description={t('web.setup.serverNameHelp')} onChange={event => {const value = event.currentTarget.value; setForm(old => ({...old, name: value}));}} disabled={busy}/></div>
    <div className={styles.legend}>{t('web.setup.howSignIn')}</div>
    <RadioGroup.Root orientation="vertical" aria-label={t('web.setup.howSignIn')} value={mode} disabled={busy}
      onValueChange={value => {if (value === 'hosted' || value === 'local') controller.chooseKind(value);}}>
    <RadioGroup.Item value="hosted" className={hosted ? styles.choice : styles.qopt}>
      {hosted && <span className={styles.choiceIcon} aria-hidden="true"><Icon name="i-person" size={22}/></span>}
      <span className={styles.choiceCopy}><b>{t('accounts.settings.portico')}<span className={styles.rec}>{t('web.setup.recommended')}</span></b>
        <p>{t('web.setup.porticoBody')}</p></span>
      <span className={hosted ? styles.rad : styles.radOff} aria-hidden="true"/>
    </RadioGroup.Item>
    <RadioGroup.Item value="local" className={hosted ? styles.qopt : styles.choice}>
      {local && <span className={styles.choiceIcon} aria-hidden="true"><Icon name="i-server" size={22}/></span>}
      <span className={styles.qoptCopy}><b>{t('web.setup.localSignIn')}</b><br/>{t('web.setup.localSignInBody')}</span>
      <span className={local ? styles.rad : styles.radOff} aria-hidden="true"/>
    </RadioGroup.Item>
    </RadioGroup.Root>
    {local && <>
      <div className={[styles.legend, styles.formLegend].join(' ')}>{t('web.setup.ownerTitle')}</div>
      <p className={styles.para}>{t('web.setup.ownerLede')}</p>
      <div className={styles.field}><TextField variant="auth" label={t('web.direct.username')} value={form.username} autoComplete="username"
        autoCapitalize="off" spellCheck={false} maxLength={64} required disabled={busy} description={t('web.setup.usernameHelp')}
        onChange={event => {const value = event.currentTarget.value; setForm(old => ({...old, username: value}));}}/></div>
      <div className={styles.pair}>
        <div className={styles.field}><TextField variant="auth" type="password" label={t('web.account.password')} value={form.password}
          autoComplete="new-password" required disabled={busy} onChange={event => {const value = event.currentTarget.value; setForm(old => ({...old, password: value}));}}/>
          <ServicesStrength password={form.password} id="setup-password-strength"/></div>
        <div className={styles.field}><TextField variant="auth" type="password" label={t('web.directJoin.confirm')} value={form.confirm}
          autoComplete="new-password" required disabled={busy} error={Boolean(form.confirm) && !match ? t('web.setup.mismatch') : undefined}
          onChange={event => {const value = event.currentTarget.value; setForm(old => ({...old, confirm: value}));}}/></div>
      </div>
    </>}
    <div className={[styles.btnRow, local ? styles.btnRowTight : ''].filter(Boolean).join(' ')}>
      <Button variant="primary" size="hero" type="submit" loading={busy} disabled={!canSubmit}>
        {hosted ? t('accounts.signIn.handoff') : t('web.setup.createOwner')}
      </Button>
      {busy && <Button variant="glass" size="hero" onClick={controller.back}>{t('action.back')}</Button>}
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

function FirstLibrary({controller, library, setLibrary, serverName, folders, busy}: {controller: SetupController; library: Library;
  setLibrary: (update: (old: Library) => Library) => void; serverName: string; folders?: LibraryContentApi; busy: boolean}) {
  const [picking, setPicking] = useState(false);
  const ready = setupNameValid(library.name) && (!!library.folders?.length || library.path.trim().length > 0) && !busy;
  const setFolders = (chosen: readonly string[], classifications?: Readonly<Record<string, 'local' | 'network'>>) => {
    if (chosen.length > 64 || chosen.some(path => !path || path.length > 8192)) return;
    const unique = Object.freeze([...new Set(chosen)]), classes: Record<string, 'local' | 'network'> = {};
    for (const path of unique) {const value = classifications?.[path]; if (value === 'local' || value === 'network') classes[path] = value;}
    setLibrary(old => ({...old, folders: unique, folderClasses: Object.freeze(classes), path: ''}));
  };
  const setPath = (path: string) => setLibrary(old => ({...old, path, folders: undefined, folderClasses: undefined}));
  return <AuthStage legal={false} contentClassName={styles.contentLibrary}>
    <p className={styles.okline}><Icon name="i-okc" size={20}/>{t('web.setup.youOwn', {server: serverName})}</p>
    <h1>{t('web.setup.firstLibrary')}</h1>
    <p className={styles.lede}>{t('web.setup.firstLibraryLede')}</p>
    <div className={styles.kinds} role="group" aria-label={t('web.setup.firstLibrary')}>
      {setupLibraryKinds.map(kind => <button key={kind.id} type="button" className={styles.kind} aria-pressed={library.kind === kind.id}
        onClick={() => setLibrary(old => ({...old, kind: kind.id, name: defaultLibraryName(kind.id)}))} disabled={busy}>
        <Icon name={kind.icon} size={26}/>{t(kind.label)}
      </button>)}
    </div>
    <p className={styles.kindDescription}><b>{t(setupLibraryKinds.find(kind => kind.id === library.kind)!.label)}.</b> {libraryKindDescription(library.kind)}</p>
    <div className={styles.field}><TextField variant="auth" label={t('web.setup.folderName')} value={library.name} maxLength={80}
      required disabled={busy} onChange={event => {const value = event.currentTarget.value; setLibrary(old => ({...old, name: value}));}}/></div>
    <div className={styles.legend}>{t('web.setup.folders')}</div>
    {folders ? <>
      <div className={styles.setupFolders}>{library.folders?.map(path => {
        const folderClass = library.folderClasses?.[path];
        return <div key={path} className={styles.setupFolder}><Icon name="i-folder" size={20}/><span>{path}</span>
          {folderClass ? <small className={styles.folderClass}>{t(folderClass === 'network' ? 'web.libraries.networkShare' : 'web.libraries.localDisk')}</small> : null}
          <Button variant="text" size="small" icon="i-x" aria-label={`${t('next.ownerLibraries.explorer.removeChosen')} ${path}`} disabled={busy}
            onClick={() => setFolders(library.folders?.filter(value => value !== path) ?? [], library.folderClasses)}/></div>;
      })}</div>
      <div className={styles.folderChoose}>
        <Button variant="glass" disabled={busy} onClick={() => setPicking(true)}>{t('next.ownerLibraries.explorer.choose')}</Button>
        <small>{t('web.setup.chooseFoldersHelp')}</small>
      </div>
      <FolderPicker open={picking && !busy} onOpenChange={setPicking} api={folders} onChoose={setPath} onChooseMany={setFolders} initialPaths={library.folders} serverName={serverName}/>
    </> : <div className={styles.field}><TextField variant="auth" label={t('web.setup.orTypePath')} value={library.path} required disabled={busy}
      autoComplete="off" spellCheck={false} onChange={event => setPath(event.currentTarget.value)}/></div>}
    <div className={styles.btnRow}>
      <Button variant="primary" size="hero" loading={busy} disabled={!ready} onClick={() => controller.addLibrary({kind: library.kind, name: library.name.trim(), path: library.path.trim(),
        ...(library.folders?.length ? {folders: library.folders} : {})})}>{t('web.setup.addLibraryScan')}</Button>
      <Button variant="glass" size="hero" disabled={busy} onClick={controller.skip}>{t('web.setup.skip')}</Button>
    </div>
  </AuthStage>;
}
