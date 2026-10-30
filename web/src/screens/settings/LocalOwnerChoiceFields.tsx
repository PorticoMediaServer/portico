import {defaultI18n} from '@i18n';
import {RichSelect, TextField} from '../../kit/controls';
import styles from './local-change-owner.module.css';

const t = defaultI18n.t;

export type LocalOwnerChoiceMode = 'existing' | 'new';
export type LocalOwnerChoiceMember = Readonly<{accountId: string; username: string}>;

/**
 * Root63/68: the local Change owner choice as fields only, for the guard writer
 * to mount inside its own dialog. It renders the two ways to name the new owner —
 * an existing member with no implicit selection, or a new local account — and owns
 * nothing else: no effects, mutations, API, persistence, validation authority,
 * current-password handling or dialog lifetime.
 */
export function LocalOwnerChoiceFields({mode, members, accountId, username, name, newPassword, disabled, onModeChange, onAccountIdChange, onUsernameChange, onNameChange, onNewPasswordChange}: {
  mode: LocalOwnerChoiceMode;
  members: readonly LocalOwnerChoiceMember[];
  accountId: string;
  username: string;
  name: string;
  newPassword: string;
  disabled: boolean;
  onModeChange: (mode: LocalOwnerChoiceMode) => void;
  onAccountIdChange: (accountId: string) => void;
  onUsernameChange: (username: string) => void;
  onNameChange: (name: string) => void;
  onNewPasswordChange: (password: string) => void;
}) {
  return <div className={styles.body}>
    <RichSelect label={t('accounts.settings.changeOwnerLocalTargetKind')} value={mode} disabled={disabled}
      options={[{value: 'existing', label: t('accounts.settings.changeOwnerLocalExisting')}, {value: 'new', label: t('accounts.settings.changeOwnerLocalCreate')}]}
      onValueChange={value => onModeChange(value === 'new' ? 'new' : 'existing')} />
    {mode === 'existing' ? <>
      <RichSelect label={t('accounts.settings.changeOwnerLocalExisting')} value={accountId} disabled={disabled || !members.length}
        options={members.map(member => ({value: member.accountId, label: member.username}))} onValueChange={onAccountIdChange} />
      {!members.length && <p className={styles.empty}>{t('accounts.settings.changeOwnerLocalEmptyCreate')}</p>}
    </> : <>
      <TextField label={t('accounts.settings.changeOwnerLocalUsername')} name="username" autoComplete="username" autoCapitalize="off" spellCheck={false}
        value={username} disabled={disabled} onChange={event => onUsernameChange(event.currentTarget.value)} />
      <TextField label={t('accounts.settings.changeOwnerLocalName')} name="name" autoComplete="name"
        value={name} disabled={disabled} onChange={event => onNameChange(event.currentTarget.value)} />
      <TextField label={t('accounts.settings.changeOwnerLocalPassword')} name="new-password" type="password" autoComplete="new-password"
        value={newPassword} disabled={disabled} onChange={event => onNewPasswordChange(event.currentTarget.value)} />
    </>}
  </div>;
}
