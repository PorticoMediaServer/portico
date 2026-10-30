/** A callback captured by an old dialog render cannot act after replacement,
 * including the microtask between a successful save and its .then callback. */
export function dialogAction<T extends {isActive: () => boolean}>(owner: {current: T}, captured: T, action: () => void) {
  return () => {if (owner.current === captured && captured.isActive()) action();};
}

/** One opening may act only while it is still the event-owned current modal. */
export const openingActionAllowed = (original: {token: {live: boolean}}, current: unknown) => current === original && original.token.live;
/** One parent view may act only inside the live layout admission for that same source. */
export const sourceAdmissionAllowed = (admission: Readonly<{token: object; source: unknown}> | undefined, live: object | undefined, source: unknown) =>
  !!admission && live === admission.token && admission.source === source;
/** An effectful action may use only the exact snapshot its render drew, plus the drawn
 * confirmation/preview that render matched. A later controller snapshot is never adopted. */
export const drawnActionAllowed = <T,>(snapshot: T, drawn: T, matches: boolean): boolean => snapshot === drawn && matches;
