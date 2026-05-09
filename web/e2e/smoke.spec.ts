import { test, expect } from '@playwright/test';

test.describe('Saker workspace smoke tests', () => {
  test('health endpoint returns OK', async ({ request }) => {
    const resp = await request.get('/health');
    expect(resp.status()).toBe(200);
  });

  test('workspace page loads', async ({ page }) => {
    await page.goto('/');
    // Wait for the Next.js app to hydrate
    await page.waitForSelector('body', { timeout: 10000 });
    // The page should not show a Next.js error overlay
    expect(page.locator('#__next-build-error-overlay')).toBeHidden();
  });

  test('editor sub-app is accessible', async ({ page }) => {
    await page.goto('/editor/');
    await page.waitForSelector('body', { timeout: 10000 });
  });
});