import { readFileSync } from 'node:fs';
import { resolve } from 'node:path';

/** WCAG audit of the token pairs the design system actually uses.
 * 04-experience.md: "Validate final combinations rather than trusting hex
 * values." This spec is that validation; it fails the build on regressions. */

const css = readFileSync(resolve(process.cwd(), 'src/styles/tokens.css'), 'utf8');
// Accept either quote style in attribute selectors; formatters may rewrite them.
// Normalize single quotes to double quotes first, then escape the selector.
const normalizedCss = css.replaceAll("'", '"');

function themeVars(selector: string): Record<string, string> {
  const re = new RegExp(`${selector.replace(/[[\]]/g, (m) => '\\' + m)}\\s*\\{([^}]*)\\}`);
  const block = normalizedCss.match(re)?.[1];
  if (!block) throw new Error(`token block not found: ${selector}`);
  const vars: Record<string, string> = {};
  for (const m of block.matchAll(/--([a-z-]+):\s*(#[0-9A-Fa-f]{6})/g)) {
    vars[m[1]] = m[2];
  }
  return vars;
}

function relLum(hex: string): number {
  const h = hex.replace('#', '');
  const ch = [0, 2, 4].map((i) => parseInt(h.slice(i, i + 2), 16) / 255);
  const f = (c: number) => (c <= 0.04045 ? c / 12.92 : ((c + 0.055) / 1.055) ** 2.4);
  const [r, g, b] = ch.map(f);
  return 0.2126 * r + 0.7152 * g + 0.0722 * b;
}

function contrast(a: string, b: string): number {
  const hi = Math.max(relLum(a), relLum(b));
  const lo = Math.min(relLum(a), relLum(b));
  return (hi + 0.05) / (lo + 0.05);
}

interface Pair {
  name: string;
  fg: string;
  bg: string;
  min: number; // 4.5 text, 3.0 non-text (WCAG 1.4.11)
}

const pairs: Pair[] = [
  { name: 'primary text on canvas', fg: 'text-primary', bg: 'surface-canvas', min: 4.5 },
  { name: 'primary text on base', fg: 'text-primary', bg: 'surface-base', min: 4.5 },
  { name: 'primary text on raised', fg: 'text-primary', bg: 'surface-raised', min: 4.5 },
  { name: 'muted text on base', fg: 'text-muted', bg: 'surface-base', min: 4.5 },
  { name: 'muted text on raised', fg: 'text-muted', bg: 'surface-raised', min: 4.5 },
  { name: 'accent foreground on accent fill', fg: 'accent-fg', bg: 'accent', min: 4.5 },
  {
    name: 'accent foreground on accent fill, hover',
    fg: 'accent-fg',
    bg: 'accent-hover',
    min: 4.5,
  },
  { name: 'accent as text on base', fg: 'accent', bg: 'surface-base', min: 4.5 },
  { name: 'accent as text on raised', fg: 'accent', bg: 'surface-raised', min: 4.5 },
  { name: 'accent as text on canvas', fg: 'accent', bg: 'surface-canvas', min: 4.5 },
  { name: 'accent as non-text indicator on base', fg: 'accent', bg: 'surface-base', min: 3.0 },
  { name: 'accent as non-text indicator on raised', fg: 'accent', bg: 'surface-raised', min: 3.0 },
  { name: 'warning as non-text on raised', fg: 'warning', bg: 'surface-raised', min: 3.0 },
  { name: 'danger as non-text on raised', fg: 'danger', bg: 'surface-raised', min: 3.0 },
  { name: 'warning text on raised', fg: 'warning', bg: 'surface-raised', min: 4.5 },
  { name: 'danger text on raised', fg: 'danger', bg: 'surface-raised', min: 4.5 },
  { name: 'warning text on canvas', fg: 'warning', bg: 'surface-canvas', min: 4.5 },
  { name: 'danger text on canvas', fg: 'danger', bg: 'surface-canvas', min: 4.5 },
  { name: 'structural border on base', fg: 'border', bg: 'surface-base', min: 3.0 },
  { name: 'structural border on raised', fg: 'border', bg: 'surface-raised', min: 3.0 },
  { name: 'structural border on canvas', fg: 'border', bg: 'surface-canvas', min: 3.0 },
  { name: 'focus ring on base', fg: 'focus-ring', bg: 'surface-base', min: 3.0 },
  { name: 'focus ring on raised', fg: 'focus-ring', bg: 'surface-raised', min: 3.0 },
  { name: 'focus ring on canvas', fg: 'focus-ring', bg: 'surface-canvas', min: 3.0 },
];

// The forest navigation keeps one palette in both themes (the mock's fixed dark
// sidebar), so its pairs are checked once against :root instead of per theme.
const navPairs: Pair[] = [
  { name: 'nav label on nav surface', fg: 'nav-fg', bg: 'nav-surface', min: 4.5 },
  { name: 'nav muted text on nav surface', fg: 'nav-muted', bg: 'nav-surface', min: 4.5 },
  { name: 'nav brand on nav surface', fg: 'nav-brand', bg: 'nav-surface', min: 4.5 },
  { name: 'active nav label on active pill', fg: 'nav-active-fg', bg: 'nav-active-bg', min: 4.5 },
  { name: 'nav icon on nav surface', fg: 'nav-icon', bg: 'nav-surface', min: 3.0 },
  { name: 'nav accent on nav surface', fg: 'nav-accent', bg: 'nav-surface', min: 3.0 },
  { name: 'active pill against nav surface', fg: 'nav-active-bg', bg: 'nav-surface', min: 3.0 },
];

for (const theme of ['[data-theme="dark"]', '[data-theme="light"]']) {
  describe(`token contrast — ${theme}`, () => {
    const vars = themeVars(theme);
    for (const p of pairs) {
      it(`${p.name} >= ${p.min}:1`, () => {
        const fg = vars[p.fg];
        const bg = vars[p.bg];
        expect(fg, `missing token --${p.fg}`).toBeTruthy();
        expect(bg, `missing token --${p.bg}`).toBeTruthy();
        const ratio = contrast(fg, bg);
        expect(
          ratio,
          `${p.name}: ${fg} on ${bg} = ${ratio.toFixed(2)} (need ${p.min})`,
        ).toBeGreaterThanOrEqual(p.min);
      });
    }
  });
}

describe('token contrast — forest navigation (theme-independent)', () => {
  const vars = themeVars(':root');
  for (const p of navPairs) {
    it(`${p.name} >= ${p.min}:1`, () => {
      const fg = vars[p.fg];
      const bg = vars[p.bg];
      expect(fg, `missing token --${p.fg}`).toBeTruthy();
      expect(bg, `missing token --${p.bg}`).toBeTruthy();
      const ratio = contrast(fg, bg);
      expect(
        ratio,
        `${p.name}: ${fg} on ${bg} = ${ratio.toFixed(2)} (need ${p.min})`,
      ).toBeGreaterThanOrEqual(p.min);
    });
  }
});
