import { createRequire } from 'node:module';
import { readdirSync, readFileSync } from 'node:fs';
import { resolve } from 'node:path';
// resolve playwright from web/node_modules (pinned install) regardless of cwd
const require_ = createRequire(new URL('../web/package.json', import.meta.url));
const { chromium } = require_('playwright');

/** Pixel audit of T05 screenshots: dimensions, theme canvas color, non-blank,
 * and dark/light pairs actually differ. Complement to human review. */
const DIR = resolve(process.argv[2] ?? '.artifacts/agents/T05/screenshots');
const VIEWPORT_W = { 'mobile-390': 390, 'tablet-768': 768, 'desktop-1440': 1440 };
// must match tokens.css: top-left is the nav (surface-base) on wide viewports,
// canvas on mobile (no sidebar)
const CORNER = {
  dark: { base: [18, 29, 24], canvas: [11, 18, 16] }, // #121D18 / #0B1210
  light: { base: [255, 255, 255], canvas: [245, 247, 242] }, // #FFF / #F5F7F2
};

const browser = await chromium.launch();
const page = await browser.newPage();
await page.setContent('<canvas id="c"></canvas>');

let fails = 0;
const files = readdirSync(DIR).filter((f) => f.endsWith('.png')).sort();
for (const f of files) {
  const vp = Object.keys(VIEWPORT_W).find((k) => f.endsWith(`${k}.png`));
  const theme = f.includes('-dark-') ? 'dark' : 'light';
  const b64 = readFileSync(resolve(DIR, f)).toString('base64');
  const info = await page.evaluate(async (b64) => {
    const img = new Image();
    await new Promise((ok, err) => { img.onload = ok; img.onerror = err; img.src = 'data:image/png;base64,' + b64; });
    const c = document.getElementById('c');
    c.width = img.width; c.height = img.height;
    const ctx = c.getContext('2d');
    ctx.drawImage(img, 0, 0);
    const px = (x, y) => Array.from(ctx.getImageData(x, y, 1, 1).data.slice(0, 3));
    const { data } = ctx.getImageData(0, 0, img.width, img.height);
    const colors = new Set();
    for (let i = 0; i < data.length; i += 4 * 997) colors.add((data[i] << 16) | (data[i + 1] << 8) | data[i + 2]);
    return { w: img.width, h: img.height, corner: px(2, 2), mid: px(Math.floor(img.width / 2), Math.floor(img.height / 2)), colors: colors.size };
  }, b64);

  const problems = [];
  if (info.w !== VIEWPORT_W[vp]) problems.push(`width ${info.w} != ${VIEWPORT_W[vp]}`);
  if (info.h < 400) problems.push(`height ${info.h} suspiciously small`);
  const want = CORNER[theme][vp === 'mobile-390' ? 'canvas' : 'base'];
  const near = (a, b) => a.every((v, i) => Math.abs(v - b[i]) <= 3);
  if (!near(info.corner, want)) problems.push(`corner ${info.corner} != ${want}`);
  if (info.colors < 15) problems.push(`only ${info.colors} sampled colors — looks blank`);
  if (problems.length) { fails++; console.log(`FAIL ${f}: ${problems.join('; ')}`); }
  else console.log(`ok   ${f} (${info.w}x${info.h}, ${info.colors} colors)`);
}

// dark vs light pairs must differ
for (const f of files.filter((f) => f.includes('-dark-'))) {
  const light = f.replace('-dark-', '-light-');
  if (files.includes(light)) {
    const a = readFileSync(resolve(DIR, f)), b = readFileSync(resolve(DIR, light));
    if (a.equals(b)) { fails++; console.log(`FAIL pair identical: ${f}`); }
  }
}
await browser.close();
console.log(fails === 0 ? `PIXEL AUDIT: ${files.length} screenshots OK` : `PIXEL AUDIT: ${fails} problems`);
process.exit(fails === 0 ? 0 : 1);
