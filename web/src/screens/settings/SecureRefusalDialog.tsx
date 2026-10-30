import {useLayoutEffect, useRef, useState} from 'react';
import {defaultI18n} from '@i18n';
import {Button} from '../../kit/controls/Button';
import {Dialog} from '../../kit/overlays/Dialog';
import {useSecureOwnAddress, type SecureAddressOffer} from './SecureAddressProvider';

const t = defaultI18n.t;

type Opening = {readonly offer: SecureAddressOffer | null; live: boolean; spent: boolean};

/** auth 24j: the one secure-context refusal all three sign-in changes show. The
 * forward link appears only while the sign-in holder publishes an exact own-address
 * receipt, and every open gets its own witness (with a null offer when no address is
 * admitted). A retained Cancel or Open from an earlier opening can neither close nor
 * navigate a successor. */
export function SecureRefusalDialog({open, onOpenChange}: {open: boolean; onOpenChange: (open: boolean) => void}) {
  const address = useSecureOwnAddress();
  const [opening, setOpening] = useState<Opening | null>(null);
  // A same-tick close/reopen can batch false->true so [open, address] never changes.
  // Retiring an accepted opening advances this generation, so the next live render
  // mints a fresh witness even when the open prop ends where it started.
  const [generation, setGeneration] = useState(0);
  const openingRef = useRef<Opening | null>(null);
  useLayoutEffect(() => {
    if (!open) return;
    // Every open, offer change or Strict replay gets a fresh witness, never a spent one.
    const fresh: Opening = {offer: address ?? null, live: true, spent: false};
    openingRef.current = fresh;
    setOpening(fresh);
    return () => {
      fresh.live = false;
      if (openingRef.current === fresh) openingRef.current = null;
      setOpening(current => current === fresh ? null : current);
    };
  }, [open, address, generation]);
  const drawn = opening && opening.live && openingRef.current === opening && open && opening.offer !== null && opening.offer === address && !opening.spent ? opening : null;
  const retire = (held: Opening) => {
    held.live = false;held.spent = true;
    if (openingRef.current === held) openingRef.current = null;
    setOpening(current => current === held ? null : current);
    // Only an accepted, exact witness advances this; cleanup and stale handlers do not.
    setGeneration(value => value + 1);
  };
  const dismiss = () => {
    // Only this rendered, live witness may close the dialog. A retained close from an
    // older opening has no ref (or a successor's) and refuses.
    const held = openingRef.current;
    if (!held || !opening || held !== opening || !held.live || held.spent) return;
    retire(held);
    onOpenChange(false);
  };
  const openSecure = () => {
    const held = openingRef.current;
    if (!held || held !== opening || !held.live || held.spent || !held.offer) return;
    const target = held.offer;
    // Accepted navigation contract: retire this exact witness and close the dialog
    // before the source's own lease check, so a refused lease never leaves an inert
    // Cancel-only dialog behind.
    retire(held);
    onOpenChange(false);
    target.open();
  };
  return <Dialog children={null} open={open} onOpenChange={value => {if (!value) dismiss();}}
    title={t('accounts.ownerSwitch.secureTitle')}
    description={t('accounts.ownerSwitch.secureBody', {localUrl: 'http://localhost:32500'})}
    footer={<>
      <Button variant="glass" onClick={dismiss}>{t('action.cancel')}</Button>
      {drawn && <Button variant="primary" onClick={openSecure}>{t('accounts.ownerSwitch.openSecure')}</Button>}
    </>}/>;
}
