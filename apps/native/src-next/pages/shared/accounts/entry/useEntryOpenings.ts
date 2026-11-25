import {useCallback, useLayoutEffect, useRef, useState, type Dispatch, type MutableRefObject, type SetStateAction} from 'react';
import {Platform} from 'react-native';
import type {ResetFileLocation} from '../../../../../../../packages/client-core/src/account-reset';
import type {IdentityTransaction} from '../../../../../../../packages/client-core/src/hosted-security';
import type {NativePorticoSignIn} from '../../../../platform/portico-sign-in';
import type {AccountDownloadsEntry} from '../../../../screens/accounts/downloads-entry-model';
import type {
  DownloadsOpening,
  FactorOpening,
  NativeAccountEntryStage,
  OwnerRecoveryOpening,
  OwnerResetIntent,
  OwnerResetOpening,
  ProviderOpening,
  RegisterOpening,
  VerifyOpening,
} from '../../../../pages/shared/accounts/entry/entry-types';

type EntryOpeningsParams = {
  stage: NativeAccountEntryStage;
  transaction: IdentityTransaction | undefined;
  name: string;
  setName: Dispatch<SetStateAction<string>>;
  downloads: AccountDownloadsEntry | undefined;
  account: MutableRefObject<NativePorticoSignIn | undefined>;
  factorKind: MutableRefObject<'portico' | 'local'>;
};

/** Owns per-stage form openings, owner recovery/reset tickets, and download-entry observation. */
export function useEntryOpenings({stage, transaction, name, setName, downloads, account, factorKind}: EntryOpeningsParams) {
  // Root 43: only the server's confirmed public-handle collision may mark this
  // field, and only for the form opening that asked. The opening retires with its
  // stage, so a retained Submit or a late refusal cannot mark a successor form.
  const [usernameUnavailable, setUsernameUnavailable] = useState(false);
  const [passwordShown, setPasswordShown] = useState(false);
  const [registerOpening, setRegisterOpening] = useState<RegisterOpening>();
  const [verifyOpening, setVerifyOpening] = useState<VerifyOpening>();
  const verifyOpeningRef = useRef(verifyOpening); verifyOpeningRef.current = verifyOpening;
  // The provider completion's opening: the exact transaction this form was drawn for.
  const [providerOpening, setProviderOpening] = useState<ProviderOpening>();
  const [providerUsernameUnavailable, setProviderUsernameUnavailable] = useState(false);
  // The phone Portico factor form's opening: its exact issued witness, source and life.
  const [factorOpening, setFactorOpening] = useState<FactorOpening>();

  const ownerRecoveryContact = useRef<OwnerRecoveryOpening | undefined>(undefined);
  const ownerRecoveryMounted = useRef(false);
  const retireOwnerRecoveryContact = useCallback(() => {
    const ticket = ownerRecoveryContact.current; ownerRecoveryContact.current = undefined;
    if (ticket) {ticket.active = false; ticket.contact?.dispose(); ticket.finish?.();}
  }, []);
  const [resetLocation, setResetLocation] = useState<ResetFileLocation>();
  const ownerResetIntent = useRef<OwnerResetIntent | undefined>(undefined);
  const ownerResetOpening = useRef<OwnerResetOpening | undefined>(undefined);
  const retireOwnerReset = useCallback(() => {
    const ticket = ownerResetOpening.current; ownerResetOpening.current = undefined;
    ticket?.controller.abort(); ticket?.source.dispose();
  }, []);

  // Exact witness of the drawn name draft, so the provider opening can read the committed draft
  // it was drawn against without reopening its owner on every keystroke. It mirrors state and
  // carries no authority of its own.
  const nameDraft = useRef(name);
  // An opaque revision, advanced by every editable register field (the password
  // included), so a Submit drawn against an older draft cannot send its old values.
  const registerOpeningRef = useRef(registerOpening);
  const providerOpeningRef = useRef(providerOpening);
  const registerDraftRev = useRef(0);
  // The transaction whose provider-suggested name has already been seeded into this draft.
  const providerNameSeed = useRef<string | undefined>(undefined);
  registerOpeningRef.current = registerOpening;
  providerOpeningRef.current = providerOpening;

  const downloadsOwner = useRef<DownloadsOpening | undefined>(undefined);
  const [downloadValue, setDownloadValue] = useState<{opening: DownloadsOpening; available: boolean}>();
  if (downloadsOwner.current?.source !== downloads && downloadsOwner.current) downloadsOwner.current.active = false;
  useLayoutEffect(() => {
    if (!downloads) {setDownloadValue(undefined); return;}
    const opening: DownloadsOpening = {source: downloads, active: true}; downloadsOwner.current = opening;
    const update = () => {
      if (!opening.active || downloadsOwner.current !== opening) return;
      const available = downloads.current() && downloads.getSnapshot().available;
      setDownloadValue({opening, available});
    };
    const off = downloads.subscribe(update); update();
    return () => {opening.active = false; if (downloadsOwner.current === opening) downloadsOwner.current = undefined; off();};
  }, [downloads]);

  // This public browser contact retires during the unmount commit, before passive
  // account cleanup; a late proof cannot navigate after this view has left.
  useLayoutEffect(() => {
    ownerRecoveryMounted.current = true;
    return () => {ownerRecoveryMounted.current = false; retireOwnerRecoveryContact();};
  }, [retireOwnerRecoveryContact]);
  useLayoutEffect(() => {if (stage !== 'servers') retireOwnerRecoveryContact();}, [stage, retireOwnerRecoveryContact]);

  // Open one signup form per entry to the register stage. Its layout cleanup retires
  // that exact opening before the next effect or the unmount commit finishes, so a
  // late answer cannot mark a form that has already left.
  useLayoutEffect(() => {
    setUsernameUnavailable(false); setPasswordShown(false);
    if (stage !== 'register') {setRegisterOpening(undefined); return;}
    const source = account.current;
    if (!source) {setRegisterOpening(undefined); return;}
    const owner = {live: true};
    setRegisterOpening(Object.freeze({source, owner}));
    return () => {owner.live = false;};
  }, [stage, account]);

  // One provider completion opening per drawn transaction, carrying the source it was
  // drawn with and its own lifetime; retired synchronously with its stage.
  // The drawn name draft's committed witness. Publishing it in the layout turn means an
  // abandoned render cannot replace the draft the opening reads, and the opening copies it
  // once at its own effect run below.
  useLayoutEffect(() => {nameDraft.current = name;}, [name]);
  useLayoutEffect(() => {
    if (stage !== 'provider' || !transaction) {setProviderOpening(undefined); return;}
    const source = account.current;
    if (!source) {setProviderOpening(undefined); return;}
    const owner = {live: true}, life = new AbortController();
    setProviderOpening(Object.freeze({transaction, source, owner, life}));
    setProviderUsernameUnavailable(false);
    // The phone's first-time provider step considers this transaction once, on its first
    // eligible opening, whether or not a name is suggested. Only an empty draft takes the
    // suggested name, and only an actual seed advances the revision that retires an older
    // Submit; a cleared draft on a later refresh is never seeded over. The draft is read
    // once, from the witness this commit published, so an edit never reopens this provider
    // owner and a later render cannot lend it a newer draft.
    const draft = nameDraft.current;
    if (!Platform.isTV && transaction.needsUsername && providerNameSeed.current !== transaction.transactionId) {
      providerNameSeed.current = transaction.transactionId;
      const suggested = transaction.suggestedName;
      if (suggested && !draft) {
        registerDraftRev.current++;
        setName(suggested);
      }
    }
    return () => {owner.live = false; life.abort();};
  }, [stage, transaction, account, setName]);

  // One factor opening per phone Portico challenge; its layout cleanup retires and
  // aborts it so a retained handler or late settlement cannot act on a successor form.
  useLayoutEffect(() => {
    if (stage !== 'factor' || factorKind.current !== 'portico' || Platform.isTV) {setFactorOpening(undefined); return;}
    const source = account.current, witness = source?.factorChallengeIdentity();
    if (!source || !witness) {setFactorOpening(undefined); return;}
    const owner = {live: true}, life = new AbortController();
    setFactorOpening(Object.freeze({source, witness, owner, life}));
    return () => {owner.live = false; life.abort();};
  }, [stage, account, factorKind]);

  return {
    usernameUnavailable, setUsernameUnavailable,
    passwordShown, setPasswordShown,
    registerOpening, registerOpeningRef,
    verifyOpening, setVerifyOpening, verifyOpeningRef,
    providerOpening, providerOpeningRef,
    providerUsernameUnavailable, setProviderUsernameUnavailable,
    factorOpening, registerDraftRev,
    ownerRecoveryContact, ownerRecoveryMounted, retireOwnerRecoveryContact,
    resetLocation, setResetLocation, ownerResetIntent, ownerResetOpening, retireOwnerReset,
    downloadsOwner, downloadValue,
  };
}
