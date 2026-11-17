import {useState} from 'react';
import {Button, Segmented} from '../../kit/controls';
import {Dialog} from '../../kit/overlays';
import type {TogetherBindings} from '../together/bindings';
import styles from './start-room.module.css';

/** desktop-player f1: More lists Watch Together only while this playback has no room.
 * The group service owns creation, membership and the clock; this draws the form beside
 * the picture so starting a room never leaves what is playing. */
export function WatchStartRoom({binding, open, onOpenChange}: {binding: TogetherBindings; open: boolean; onOpenChange(open: boolean): void}) {
  const t = binding.t, [name, setName] = useState(''), [authority, setAuthority] = useState<'host' | 'everyone'>('host'), [busy, setBusy] = useState(false);
  const room = binding.getSnapshot().room;
  const start = async () => {
    if (busy || !binding.isCurrent() || !name.trim() || !binding.profileName.trim()) return;
    setBusy(true);
    try {
      const current = binding.runtime.getSnapshot();
      if (!current.fence || current.fence.kind !== 'queue' || current.subject?.kind !== 'video') return;
      const itemId = current.fence.itemId;
      if (await binding.service.create({name, displayName: binding.profileName, control: authority})) {
        await binding.service.watch(itemId);onOpenChange(false);
      }
    } finally {setBusy(false);}
  };
  return <Dialog open={open} onOpenChange={onOpenChange} title={t('web.together.startTitle')} size="sm" accessibilityLabel={t('web.settings.together')}
    footer={<Button variant="primary" size="small" disabled={busy || room.busy || !name.trim() || !binding.profileName.trim()} onClick={() => {void start().catch(binding.reportError);}}>{t('web.together.start')}</Button>}>
    <form className={styles.form} onSubmit={event => {event.preventDefault();start().catch(binding.reportError);}}>
      <p className={styles.help}>{t('web.together.startHelp')}</p>
      <label className={styles.field}>{t('builder.name')}
        <input autoComplete="off" value={name} onChange={event => setName(event.target.value)} maxLength={128} required />
      </label>
      <fieldset className={styles.field}>
        <legend>{t('web.together.whoControls')}</legend>
        <Segmented aria-label={t('web.together.whoControls')} value={authority}
          items={[{value: 'host', label: t('web.together.onlyMe')}, {value: 'everyone', label: t('web.together.everyone')}]}
          onValueChange={value => setAuthority(value === 'everyone' ? 'everyone' : 'host')}/>
      </fieldset>
      <p className={styles.help}>{t('web.together.hostIntro')}</p>
    </form>
  </Dialog>;
}
