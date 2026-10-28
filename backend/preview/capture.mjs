// Screenshots routes of a running app with headless Chromium (playwright-core).
// Usage: node capture.mjs --base URL --out DIR --routes-json JSON --viewports-json JSON
// Prints a JSON manifest [{route,width,height,file,error?}] on stdout.
import { chromium } from 'playwright-core';
import path from 'node:path';

const arg = (name) => {
  const i = process.argv.indexOf('--' + name);
  return i >= 0 ? process.argv[i + 1] : undefined;
};
const base = arg('base');
const out = arg('out');
const routes = JSON.parse(arg('routes-json') || '[]');
const viewports = JSON.parse(arg('viewports-json') || '[]');

const manifest = [];
const browser = await chromium.launch({ args: ['--no-sandbox', '--disable-dev-shm-usage'] });
try {
  let idx = 0;
  for (const route of routes) {
    for (const vp of viewports) {
      // Filenames never derive from the route.
      const file = `${idx++}-${vp.width}.png`;
      const entry = { route, width: vp.width, height: vp.height, file };
      let ctx;
      try {
        ctx = await browser.newContext({ viewport: { width: vp.width, height: vp.height }, deviceScaleFactor: 1 });
        const page = await ctx.newPage();
        try {
          await page.goto(base + route, { waitUntil: 'networkidle', timeout: 20000 });
        } catch {
          await page.goto(base + route, { waitUntil: 'load', timeout: 20000 });
        }
        await page.screenshot({ path: path.join(out, file), fullPage: false });
      } catch (e) {
        entry.error = String(e && e.message ? e.message : e).split('\n')[0].slice(0, 200);
        delete entry.file;
      } finally {
        if (ctx) await ctx.close().catch(() => {});
      }
      manifest.push(entry);
    }
  }
} finally {
  await browser.close().catch(() => {});
}
process.stdout.write(JSON.stringify(manifest));
