import {useEffect, useMemo, useRef, useState} from 'react';
import {defaultI18n} from '@i18n';
import {PersonalSavedService, type SavedDefinition} from '@core/personal-saved.ts';
import {randomId} from '@core/random-id.ts';
import {presentError} from '@core/presentation/errors.ts';
import {Button} from '../../kit/controls';
import {Banner} from '../../kit/overlays';
import type {ScreenContext} from '../detail/context';
import {screenLifetime} from '../detail/context';
import {SavedDialog} from '../saved/SavedDialog';
import {ResourceNameField} from '../saved/ResourceFields';
import styles from '../saved/saved.module.css';
import {saveViewDraft} from './save-view';

/** A fresh dialog owner never changes Saved's or Home Customize's directory. */
export function SaveView({context, definition, onClose}: {context: ScreenContext; definition: SavedDefinition; onClose: () => void}) {
  const owner = useMemo(() => {
    const life = screenLifetime(context);
    const service = new PersonalSavedService(life.api, context.scope, async () => randomId());
    return {service, life};
  }, [context]);
  useEffect(() => () => {owner.life.dispose();owner.service.dispose();}, [owner]);
  const [name, setName] = useState(''), [busy, setBusy] = useState(false);
  const [failure, setFailure] = useState<Readonly<{error: unknown}>>();
  const pending = useRef(false), input = useRef<HTMLInputElement>(null), t = defaultI18n.t;
  const save = () => {
    if (pending.current || !name.trim() || !owner.life.current()) return;
    pending.current = true;setBusy(true);setFailure(undefined);
    void (async () => {
      await saveViewDraft(owner.service, definition, name, owner.life.current);
      owner.life.assert();setName('');onClose();
    })().catch(error => {if (owner.life.current()) setFailure({error});})
      .finally(() => {pending.current = false;if (owner.life.current()) setBusy(false);});
  };
  const error = failure ? presentError(failure.error, 'library', {operation: 'save', i18n: defaultI18n}) : undefined;
  return <SavedDialog busy={busy} open onOpenChange={open => {if (!open && !busy) onClose();}} title={t('lib.saveViewTitle')} description={t('lib.saveViewLede')} initialFocusRef={input}
    dismissOnOutsidePress={!busy} dismissOnBack={!busy}
    footer={<><Button variant="glass" size="small" disabled={busy} onClick={onClose}>{t('action.cancel')}</Button><Button variant="primary" size="small" loading={busy} disabled={!name.trim()} onClick={save}>{t('lib.saveView')}</Button></>}>
    <form onSubmit={event => {event.preventDefault();save();}}>
      {error && !error.silent && <Banner icon="i-warn">{error.title && <b>{error.title} </b>}{error.body}</Banner>}
      <ResourceNameField nameRef={input} name={name} nameMaxLength={200} disabled={busy || owner.service.getSnapshot().retryPending} placeholder={t('lib.saveViewPlaceholder')} onNameChange={setName}/>
      <p className={styles.visibilityHelp}>{t('lib.saveViewHelp')}</p>
    </form>
  </SavedDialog>;
}
