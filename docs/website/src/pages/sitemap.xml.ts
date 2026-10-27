import { getCollection } from 'astro:content';
import type { APIRoute } from 'astro';

export const GET: APIRoute = async ({ site }) => {
  const origin = site ?? new URL('https://simplerelay.dev');
  const docs = await getCollection('docs');
  const paths = [
    '/',
    '/docs/',
    ...docs.map((doc) => `/docs/${doc.id.replace(/\.md$/, '')}`),
    '/alternatives/hookdeck/',
    '/alternatives/svix/',
    '/alternatives/convoy/',
    '/alternatives/temporal/',
    '/privacy',
  ];

  const urls = paths.map((path) => `  <url><loc>${new URL(path, origin).href}</loc></url>`).join('\n');
  return new Response(`<?xml version="1.0" encoding="UTF-8"?>\n<urlset xmlns="http://www.sitemaps.org/schemas/sitemap/0.9">\n${urls}\n</urlset>`, {
    headers: { 'Content-Type': 'application/xml; charset=utf-8' },
  });
};
