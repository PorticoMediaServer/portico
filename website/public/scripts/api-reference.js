for (const pre of document.querySelectorAll('.api-guide pre')) {
  pre.classList.add('api-code');
  const button = document.createElement('button');
  button.type = 'button';
  button.className = 'api-copy';
  button.textContent = 'Copy';
  button.setAttribute('aria-label', 'Copy code example');
  button.dataset.copy = '';
  pre.prepend(button);
}

for (const button of document.querySelectorAll('[data-copy]')) {
  button.addEventListener('click', async () => {
    const code = button.parentElement?.querySelector('code')?.textContent ?? '';
    try {
      await navigator.clipboard.writeText(code);
      button.textContent = 'Copied';
    } catch {
      button.textContent = 'Press Ctrl+C';
    }
    window.setTimeout(() => { button.textContent = 'Copy'; }, 1600);
  });
}

const openFromHash = () => {
  if (!location.hash) return;
  const target = document.getElementById(decodeURIComponent(location.hash.slice(1)));
  if (target instanceof HTMLDetailsElement) {
    target.open = true;
    target.scrollIntoView({ block: 'start' });
  }
};
openFromHash();
window.addEventListener('hashchange', openFromHash);

const filter = document.querySelector('[data-api-filter]');
const empty = document.querySelector('[data-api-empty]');
filter?.addEventListener('input', () => {
  const query = filter.value.trim().toLowerCase();
  let visible = 0;
  for (const operation of document.querySelectorAll('[data-api-op]')) {
    const match = !query || operation.dataset.search?.includes(query);
    operation.hidden = !match;
    if (match) visible += 1;
  }
  if (empty) empty.hidden = visible !== 0;
});
