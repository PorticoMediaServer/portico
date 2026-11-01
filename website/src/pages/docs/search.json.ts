import { getCollection } from 'astro:content';
import { getDocCategory } from '@/data/docs';
import { apiOperations, apiGroups, operationUrl } from '@/lib/api';

const plain = (markdown: string) =>
  markdown
    .replace(/```[\s\S]*?```/g, ' ')
    .replace(/^#{1,6}\s+(.+)$/gm, '$1.')
    .replace(/!\[[^\]]*\]\([^)]*\)/g, ' ')
    .replace(/\[([^\]]+)\]\([^)]*\)/g, '$1')
    .replace(/^\s*(?:[-+]|\d+\.)\s+/gm, '')
    .replace(/[#>*_`|]/g, ' ')
    .replace(/\s+/g, ' ')
    .trim();

export async function GET() {
  const entries = await getCollection('docs');
  const index = entries.map((entry) => ({
    url: `/docs/${entry.id}/`,
    title: entry.data.title,
    description: entry.data.description,
    category: getDocCategory(entry.data.category)?.title ?? '',
    headings: [...(entry.body ?? '').matchAll(/^#{2,3}\s+(.+)$/gm)].map((match) => match[1].trim()),
    text: plain(entry.body ?? '')
  }));
  const groupTitles = new Map(apiGroups.map((group) => [group.id, group.title]));
  for (const operation of apiOperations) {
    index.push({
      url: operationUrl(operation),
      title: `${operation.method.toUpperCase()} ${operation.path}`,
      description: operation.title,
      category: `API · ${operation.audience} · ${groupTitles.get(operation.group) ?? ''}`,
      headings: [operation.title],
      text: `${operation.title}. ${operation.description}`
    });
  }
  for (const guide of await getCollection('integrationGuides')) {
    index.push({ url: `/developers/api/integrations/${guide.id}/`, title: guide.data.title,
      description: guide.data.description, category: 'Integrations',
      headings: [...(guide.body ?? '').matchAll(/^#{2,3}\s+(.+)$/gm)].map((match) => match[1].trim()),
      text: plain(guide.body ?? '') });
  }
  index.push({ url: '/developers/api/', title: 'Portico API docs', description: 'Integrations and Clients',
    category: 'Developers', headings: ['Integrations', 'Building a Portico app'], text: 'Task guides, tool setup and API references.' });
  return new Response(JSON.stringify(index), { headers: { 'Content-Type': 'application/json' } });
}
