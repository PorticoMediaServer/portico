import { defineCollection } from 'astro:content';
import { glob } from 'astro/loaders';
import { z } from 'astro/zod';

const docs = defineCollection({
  loader: glob({ pattern: '**/*.md', base: './src/content/docs' }),
  schema: z.object({
    title: z.string(),
    description: z.string(),
    category: z.string(),
    order: z.number(),
    audience: z.enum(['Everyone', 'Server owners', 'Viewers', 'Administrators', 'Developers']),
    updated: z.coerce.date(),
    related: z.array(z.string()).default([])
  })
});

export const collections = { docs };
