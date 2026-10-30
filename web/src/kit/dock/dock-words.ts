/** One visual owner for the dock's identity words, per element. Paint state only. */
const owners = new WeakMap<HTMLElement, symbol>();

export function claimDockWords(element: HTMLElement, owner: symbol): void {
  owners.set(element, owner);
}

export function releaseDockWords(element: HTMLElement, owner: symbol): void {
  if (owners.get(element) === owner) owners.delete(element);
}

export function dockWordsOwner(element: HTMLElement): symbol | undefined {
  return owners.get(element);
}
