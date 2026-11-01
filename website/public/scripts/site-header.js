const header = document.querySelector('[data-site-header]');
const button = document.querySelector('.menu-button');
const navigation = document.querySelector('.header-navigation');

const setMenu = (open) => {
  button?.setAttribute('aria-expanded', String(open));
  navigation?.toggleAttribute('data-open', open);
  if (open) navigation?.querySelector('a')?.focus();
};

button?.addEventListener('click', () => setMenu(button.getAttribute('aria-expanded') !== 'true'));

document.addEventListener('keydown', (event) => {
  if (event.key === 'Escape' && navigation?.hasAttribute('data-open')) {
    setMenu(false);
    button?.focus();
  }
});

document.addEventListener('click', (event) => {
  if (navigation?.hasAttribute('data-open') && !header?.contains(event.target)) setMenu(false);
});

const markScrolled = () => header?.toggleAttribute('data-scrolled', window.scrollY > 8);
markScrolled();
window.addEventListener('scroll', markScrolled, { passive: true });
