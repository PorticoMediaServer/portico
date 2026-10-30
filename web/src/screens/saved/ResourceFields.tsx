import type {Ref} from 'react';
import {defaultI18n} from '@i18n';
import {TextField} from '../../kit/controls';
import {savedText as s} from './text.ts';
import styles from './saved.module.css';

export function ResourceNameField({name, onNameChange, nameRef, nameMaxLength, disabled, placeholder}: {
  name: string; onNameChange(value: string): void; nameRef?: Ref<HTMLInputElement>;
  nameMaxLength: number; disabled?: boolean; placeholder?: string;
}) {
  return <TextField ref={nameRef} containerClassName={styles.resourceEditorField} label={defaultI18n.t('lib.saveViewName')} value={name} maxLength={nameMaxLength} disabled={disabled} placeholder={placeholder} onChange={event => onNameChange(event.currentTarget.value)} />;
}

/** The real editable draft shared by create and rename; it contains no rows,
 * permissions, transport or persistence. Those remain with the captured owner. */
export function ResourceFields({name, summary, onNameChange, onSummaryChange, nameRef, nameMaxLength, summaryMaxLength, disabled, summaryDisabled}: {
  name: string; summary: string; onNameChange(value: string): void; onSummaryChange(value: string): void;
  nameRef?: Ref<HTMLInputElement>; nameMaxLength: number; summaryMaxLength: number;
  disabled?: boolean; summaryDisabled?: boolean;
}) {
  return <div className={styles.resourceEditorFields} data-saved-resource-fields>
    <ResourceNameField nameRef={nameRef} name={name} nameMaxLength={nameMaxLength} disabled={disabled} onNameChange={onNameChange}/>
    <label className={styles.resourceSummary}><span>{defaultI18n.t('web.saved.summary')}<em>{s('next.saved.optional')}</em></span>
      <textarea aria-label={defaultI18n.t('web.saved.summary')} value={summary} maxLength={summaryMaxLength} disabled={disabled || summaryDisabled} onChange={event => onSummaryChange(event.currentTarget.value)} />
    </label>
  </div>;
}
