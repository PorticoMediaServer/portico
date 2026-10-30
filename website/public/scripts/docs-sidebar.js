const sidebarSearch = document.querySelector('[data-sidebar-search]');
const sidebar = sidebarSearch?.closest('.docs-sidebar');

sidebarSearch?.addEventListener('input', () => {
  const query = sidebarSearch.value.trim().toLowerCase();
  const details = [...(sidebar?.querySelectorAll('details') ?? [])];
  for (const group of details) {
    const links = [...group.querySelectorAll('a')];
    let matchCount = 0;
    for (const link of links) {
      const matches = !query || link.textContent?.toLowerCase().includes(query);
      link.hidden = !matches;
      if (matches) matchCount += 1;
    }
    group.hidden = matchCount === 0;
    if (query && matchCount > 0) group.open = true;
  }
});
