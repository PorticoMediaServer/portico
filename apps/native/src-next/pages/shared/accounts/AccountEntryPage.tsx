import React from 'react';
import {defaultI18n} from '../../../../../../packages/i18n/src';
import {ServerNeedsUpdate} from '../../../kit/accounts/ServerNeedsUpdate';
import {StateLayout} from '../../../kit/states';
// Named through pages/shared (not `./`) so the screen harness loads the hook and stage modules for real.
import {profileFace, setupElsewhereRefusal, useAccountEntry, type AccountEntryProps} from '../../../pages/shared/accounts/useAccountEntry';
import {createEntryPrimitives, type EntryStageContext} from '../../../pages/shared/accounts/entry/EntryPrimitives';
import {renderEntryWelcomeStage} from '../../../pages/shared/accounts/entry/EntryWelcomeStages';
import {renderEntryServerStage} from '../../../pages/shared/accounts/entry/EntryServerStages';
import {renderEntryInvitationStage} from '../../../pages/shared/accounts/entry/EntryInvitationStages';
import {renderEntryPorticoAuthStage} from '../../../pages/shared/accounts/entry/EntryPorticoAuthStages';
import {renderEntryDirectoryStage} from '../../../pages/shared/accounts/entry/EntryDirectoryStages';
import {renderEntryProfileStage} from '../../../pages/shared/accounts/entry/EntryProfileStages';
import {renderEntryTVCodeStage} from '../../../pages/shared/accounts/entry/EntryTVCodeStage';

export type {AccountEntryProps, NativeAccountEntryStage} from '../../../pages/shared/accounts/useAccountEntry';
export {profileFace, setupElsewhereRefusal};

const t = defaultI18n.t;

type StageRenderer = (ctx: EntryStageContext) => React.ReactElement | null | undefined;
const STAGE_RENDERERS: readonly StageRenderer[] = [
  renderEntryWelcomeStage,
  renderEntryServerStage,
  renderEntryInvitationStage,
  renderEntryPorticoAuthStage,
  renderEntryDirectoryStage,
  renderEntryProfileStage,
];

/** Current normal entry, drawn in connect39 onward. The separately labelled historical
 * setup entry is not imported. Every button dispatches an actual native/core command. */
export function AccountEntryPage(props: AccountEntryProps) {
  const view = useAccountEntry(props);
  const {stage, providerLoading, directoryList, account, refusal, setError, reset} = view;

  if (refusal) {
    return <ServerNeedsUpdate refusal={refusal} onBack={() => {setError(undefined); reset();}}/>;
  }

  // Keep the same native ancestry and loading title when callback settlement
  // hands off to directory loading. No signed-out form or second loading mount
  // belongs between them. Decision forms (username/contact/MFA) remain distinct.
  if ((stage === 'portico' && providerLoading && providerLoading === account.current)
    || (stage === 'servers' && directoryList === 'loading')) {
    return <StateLayout testID="accounts-server-list-loading" title={t('web.chooser.loading')}/>;
  }

  const kit = createEntryPrimitives(view);
  const ctx: EntryStageContext = {view, props, kit};

  for (const render of STAGE_RENDERERS) {
    const matched = render(ctx);
    if (matched !== undefined) return matched;
  }

  return renderEntryTVCodeStage(ctx);
}
