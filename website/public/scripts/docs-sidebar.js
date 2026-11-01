const sidebar = document.querySelector('[data-docs-sidebar]');
const sidebarSearch = sidebar?.querySelector('[data-sidebar-search]');
const toggle = sidebar?.querySelector('[data-docs-sidebar-toggle]');

toggle?.addEventListener('click', () => {
  const open = toggle.getAttribute('aria-expanded') === 'true';
  toggle.setAttribute('aria-expanded', String(!open));
  sidebar.toggleAttribute('data-collapsed', open);
});

sidebarSearch?.addEventListener('input', () => {
  const query = sidebarSearch.value.trim().toLowerCase();
  for (const group of sidebar.querySelectorAll('details')) {
    const groupTitle = group.querySelector('summary')?.textContent?.toLowerCase() ?? '';
    let matchCount = 0;
    for (const link of group.querySelectorAll('a')) {
      const matches = !query || groupTitle.includes(query) || link.textContent?.toLowerCase().includes(query);
      link.hidden = !matches;
      if (matches) matchCount += 1;
    }
    group.hidden = matchCount === 0;
    if (query && matchCount > 0) group.open = true;
  }
});

const current = sidebar?.querySelector('a[aria-current="page"]');
if (current && sidebar && window.matchMedia('(min-width: 961px)').matches) {
  sidebar.scrollTop = current.offsetTop - sidebar.clientHeight / 2;
}
