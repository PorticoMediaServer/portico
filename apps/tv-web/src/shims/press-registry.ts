/** What OK does on a focused control: the press handlers its `Pressable` was given (`components.tsx`). */
export type PressHandlers = {press?: () => void; pressIn?: () => void; pressOut?: () => void; longPress?: () => boolean};

const handlers = new WeakMap<Element, PressHandlers>();

export function setPressHandlers(element: Element, value: PressHandlers | undefined): void {
  if (value) handlers.set(element, value);
  else handlers.delete(element);
}

export function pressHandlers(element: Element): PressHandlers | undefined {
  return handlers.get(element);
}
