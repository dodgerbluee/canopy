import { expect, test } from '@playwright/test';
import AxeBuilder from '@axe-core/playwright';
import { resolve } from 'node:path';

/** T05 visual references: composed screens over fixtures, both themes, three
 * viewports. Screenshots are written to .artifacts/agents/T05/screenshots and
 * must be reviewed by a human before acceptance (packet rule). */

const SCREENS: Array<[name: string, path: string]> = [
  ['today', '/stories/today'],
  ['today-empty', '/stories/today?state=empty'],
  ['today-error', '/stories/today?state=error'],
  ['today-stale', '/stories/today?state=stale'],
  ['collection', '/stories/plants'],
  ['plant-detail', '/stories/plant'],
  ['capture', '/stories/capture'],
  ['compare', '/stories/compare'],
  ['purchase', '/stories/purchase'],
  ['purchases', '/stories/purchases'],
  ['insight', '/stories/insight'],
];

const THEMES = ['dark', 'light'] as const;

const SHOT_DIR = resolve(__dirname, '../../.artifacts/agents/T05/screenshots');

for (const [name, path] of SCREENS) {
  for (const theme of THEMES) {    test(`${name} — ${theme}`, async ({ page }, testInfo) => {
      const consoleErrors: string[] = [];
      page.on('console', (m) => m.type() === 'error' && consoleErrors.push(m.text()));
      page.on('pageerror', (e) => consoleErrors.push(String(e)));

      // paths may already carry a fixture-state query (?state=…)
      const join = path.includes('?') ? '&' : '?';
      await page.goto(`${path}${join}theme=${theme}`, { waitUntil: 'networkidle' });
      await expect(page.locator('story-bar')).toBeVisible();
      await page.evaluate(() => document.fonts.ready);

      // freeze motion for pixel-stable captures
      await page.addStyleTag({
        content: '*,*::before,*::after{animation:none!important;transition:none!important}',
      });
      await page.waitForTimeout(120);

      await page.screenshot({
        path: resolve(SHOT_DIR, `${name}-${theme}-${testInfo.project.name}.png`),
        fullPage: true,
      });

      const axe = await new AxeBuilder({ page }).analyze();
      expect(
        axe.violations.map((v) => `${v.id}: ${v.nodes.length} node(s) — ${v.nodes[0]?.target}`),
      ).toEqual([]);

      expect(consoleErrors, consoleErrors.join('\n')).toEqual([]);
    });
  }
}

test.describe('reduced motion', () => {
  test.use({ reducedMotion: 'reduce' });
  test('no element animates or transitions beyond 1ms', async ({ page }) => {
    await page.goto('/stories/today?theme=dark', { waitUntil: 'networkidle' });
    await expect(page.locator('story-bar')).toBeVisible();
    const offenders = await page.evaluate(() => {
      const bad: string[] = [];
      for (const el of document.querySelectorAll('*')) {
        const s = getComputedStyle(el);
        const dur = (v: string) => Math.max(...v.split(',').map((x) => parseFloat(x) || 0));
        if (dur(s.transitionDuration) > 0.001 || dur(s.animationDuration) > 0.001) {
          bad.push(`${el.tagName}.${el.className}`);
        }
      }
      return bad;
    });
    expect(offenders).toEqual([]);
  });
});
