const button = document.querySelector('.menu-button');
const navigation = document.querySelector('.header-navigation');

button?.addEventListener('click', () => {
  const open = button.getAttribute('aria-expanded') === 'true';
  button.setAttribute('aria-expanded', String(!open));
  navigation?.toggleAttribute('data-open', !open);
});
