/** A synchronous physical activation, consumed only by an admitted route effect.
 * It carries visual evidence, never entry identity or navigation permission. */
type Activation = {art: HTMLElement; current: () => boolean; consumed: boolean};
let activation: Activation | undefined;
export function withSourceArtActivation(art: HTMLElement | null | undefined, navigate: () => void, current: () => boolean): void {
  const previous = activation;
  activation = art ? {art, current, consumed: false} : undefined;
  try {navigate();} finally {activation = previous;}
}
export function consumeSourceArtActivation(): Readonly<{art: HTMLElement; current: () => boolean}> | undefined {
  const original = activation;
  if (!original || original.consumed) return;
  original.consumed = true;
  return Object.freeze({art: original.art, current: original.current});
}
