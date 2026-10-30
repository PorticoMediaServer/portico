/** Menu belongs to the app while any visible TV owner needs a Back step. */
export function createTVMenuPolicy(apply: (capture: boolean) => void) {
  const owners = new Map<object, boolean>();
  let applied: boolean | undefined;
  const update = (reassert = false) => {
    const capture = [...owners.values()].some(Boolean);
    if (!reassert && capture === applied) return;
    applied = capture;
    apply(capture);
  };
  return {
    acquire(capture = true) {
      const owner = {};
      // A newly mounted owner follows legacy AccountEntry cleanup, which can reset the native recognizer.
      owners.set(owner, capture); update(true);
      return () => {if (owners.delete(owner)) update();};
    },
  };
}
