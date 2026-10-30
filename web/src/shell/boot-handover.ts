export function bootMotion(document: Document): {duration: number; contentDelay: number} {
  const reduced = document.documentElement.dataset.reduceMotion === 'true' || document.body.dataset.reduceMotion === 'true'
    || document.defaultView?.matchMedia('(prefers-reduced-motion: reduce)').matches;
  return reduced ? {duration: 160, contentDelay: 0} : {duration: 320, contentDelay: 60};
}

/** One document, one hand-over. React remounts and warm resumes never replay it. */
export function handoverDocumentBoot(document: Document): boolean {
  const boot = document.getElementById('boot'), content = document.getElementById('root');
  if (!boot || !content || boot.hasAttribute('data-leaving')) return false;
  const {duration, contentDelay} = bootMotion(document);
  boot.setAttribute('aria-hidden', 'true');
  boot.setAttribute('data-leaving', '');
  content.setAttribute('data-boot-arriving', '');
  setTimeout(() => { boot.remove(); }, duration);
  setTimeout(() => { content.removeAttribute('data-boot-arriving'); }, duration + contentDelay);
  return true;
}

/** The real route (including its own loading outline) is already committed. */
export function revealBootDestination(document: Document): void {
  const {duration, contentDelay} = bootMotion(document);
  const page = document.querySelector('[data-route-content]'), navigation = document.querySelector('[data-shell-navigation]');
  page?.setAttribute('data-boot-content-arriving', '');
  navigation?.setAttribute('data-boot-navigation-arriving', '');
  setTimeout(() => { page?.removeAttribute('data-boot-content-arriving'); }, duration + contentDelay);
  setTimeout(() => { navigation?.removeAttribute('data-boot-navigation-arriving'); }, duration);
}
