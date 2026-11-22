import React from 'react';
import {defaultI18n} from '../../../../../../packages/i18n/src';
import {Dialog} from '../../../kit/overlays';
import {Button, FormError} from '../../../kit/controls';
import {PhonePINEntry} from '../../../kit/accounts/PhonePINEntry';
import {useManageUnlock} from '../../shared/accounts/useManageUnlock';

/** The PIN prompt a management refusal opens on phones and tablets (PR-1). TV manages on a phone or computer, so the
 * shell never mounts this there. */
export function ManageUnlockPage() {
  const unlock = useManageUnlock();
  if (!unlock) return null;
  return <Dialog testID="manage-unlock" open={unlock.open} onOpenChange={unlock.dismiss} title={unlock.title} description={unlock.description}
    dismissOnBack={!unlock.busy} dismissOnGesture={!unlock.busy} dismissOnOutsidePress={!unlock.busy}
    footer={<Button focusId="cancel" variant="glass" disabled={unlock.busy} onPress={unlock.close}>{defaultI18n.t('action.cancel')}</Button>}>
    <PhonePINEntry testID="manage-unlock-pin" value={unlock.pin} onChange={unlock.setPin} onComplete={unlock.submit} busy={unlock.busy} error={unlock.error}/>
    {unlock.failure ? <FormError>{unlock.failure}</FormError> : null}
  </Dialog>;
}
