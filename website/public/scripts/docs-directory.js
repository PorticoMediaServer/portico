const search = document.querySelector('[data-doc-search]');
const articles = [...document.querySelectorAll('[data-doc-article]')];
const categories = [...document.querySelectorAll('[data-doc-category]')];
const noResults = document.querySelector('[data-doc-no-results]');

search?.addEventListener('input', () => {
  const query = search.value.trim().toLowerCase();
  let visible = 0;
  for (const article of articles) {
    const match = !query || article.dataset.search?.includes(query);
    article.hidden = !match;
    if (match) visible += 1;
  }
  for (const category of categories) {
    category.hidden = !category.querySelector('[data-doc-article]:not([hidden])');
    if (query && !category.hidden) category.setAttribute('open', '');
  }
  if (noResults) noResults.hidden = visible !== 0;
});

if (window.matchMedia('(max-width: 640px)').matches) {
  categories.forEach((category, index) => category.toggleAttribute('open', index === 0));
}
