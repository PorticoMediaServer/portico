import {createContext, useContext, useLayoutEffect, useRef, useState, type ReactNode} from 'react';
import {loadSecureAddress, secureAddressReceipt, type SecureAddressReader, type SecureAddressReceipt} from './secure-address';

export type SecureAddressOffer = Readonly<{receipt: SecureAddressReceipt; open: () => void}>;

type Lease = {
  readonly api: SecureAddressReader;
  readonly isCurrent: () => boolean;
  readonly active: boolean;
  live: boolean;
  readonly abort: AbortController;
};
type Held = Readonly<{lease: Lease; offer: SecureAddressOffer}>;
const SecureAddressContext = createContext<SecureAddressOffer | null>(null);

/** The only reader of the sign-in page's own address. Null whenever no exact,
 * still-current read is published for the current props. */
export const useSecureOwnAddress = () => useContext(SecureAddressContext);

/** A small holder for the sign-in section: it owns one read per captured
 * build/activation tuple, publishes only that read's receipt, and retires the lease
 * before aborting it. Nothing here reads a later address at click time. */
export function SecureAddressProvider({api, isCurrent, active, children}: {
  api: SecureAddressReader; isCurrent: () => boolean; active: boolean; children: ReactNode;
}) {
  const [held, setHeld] = useState<Held | null>(null);
  const leaseRef = useRef<Lease | null>(null);
  useLayoutEffect(() => {
    if (!active || !isCurrent()) return;
    const lease: Lease = {api, isCurrent, active, live: true, abort: new AbortController()};
    leaseRef.current = lease;
    setHeld(null);
    void loadSecureAddress(api, isCurrent, lease.abort.signal).then(receipt => {
      if (!receipt || !lease.live || leaseRef.current !== lease || !isCurrent()) return;
      const offer: SecureAddressOffer = Object.freeze({receipt, open: () => {
        // The click speaks only for this exact receipt and this live read. A replaced,
        // retired or inactive read refuses; a re-composed URL must still be the drawn one.
        if (!lease.live || leaseRef.current !== lease || !lease.active || !isCurrent()) return;
        if (secureAddressReceipt(receipt.status, receipt.remote)?.url !== receipt.url) return;
        window.location.assign(receipt.url);
      }});
      setHeld({lease, offer});
    }, () => {});
    return () => {
      lease.live = false;
      if (leaseRef.current === lease) leaseRef.current = null;
      setHeld(current => current?.lease === lease ? null : current);
      lease.abort.abort();
    };
  }, [api, isCurrent, active]);
  // A new prop tuple must not draw the previous read for one commit: the published
  // offer is returned only for its own live lease and the exact current props.
  const current = () => { try { return isCurrent() === true; } catch { return false; } };
  const offer = held && held.lease.live && leaseRef.current === held.lease
    && held.lease.api === api && held.lease.isCurrent === isCurrent && held.lease.active === active
    && active && current() ? held.offer : null;
  return <SecureAddressContext.Provider value={offer}>{children}</SecureAddressContext.Provider>;
}
