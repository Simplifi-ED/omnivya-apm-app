import { test, expect, expectAnyVisible } from '../fixtures';
import { ROUTES } from '../../src/constants';

test.describe('Capacity', () => {
  test.beforeEach(async ({ gotoPage }) => {
    await gotoPage(`/${ROUTES.Capacity}`);
  });

  test('renders overview, empty-state, or capability notice — never blank', async ({ page }) => {
    await expectAnyVisible(
      [
        page.getByText('Capacity metrics unavailable'),
        page.getByPlaceholder('Filter namespace'),
        page.getByText('Waste by namespace'),
        page.getByRole('alert'),
        page.getByText(/Resource efficiency/i),
      ],
      { message: 'Capacity page did not render any meaningful content' }
    );
  });

  test('page heading and tabs are visible', async ({ page }) => {
    const heading = page
      .getByRole('heading', { name: /Capacity/i })
      .or(page.getByRole('heading', { name: /Application Observability/i }))
      .or(page.getByRole('link', { name: /Capacity/i }));
    await expect(heading.first()).toBeVisible({ timeout: 15_000 });

    await expect(page.getByRole('tab', { name: /Overview/i }).first()).toBeVisible({ timeout: 15_000 });
    await expect(page.getByRole('tab', { name: /Right-sizing/i }).first()).toBeVisible();
    await expect(page.getByRole('tab', { name: /History/i }).first()).toBeVisible();
    await expect(page.getByRole('tab', { name: /Forecast/i }).first()).toBeVisible();
  });

  test('right-sizing tab switches without blanking the page', async ({ page }) => {
    await page
      .getByRole('tab', { name: /Right-sizing/i })
      .first()
      .click();
    await expectAnyVisible(
      [
        page.getByText('No recommendations'),
        page.getByText(/Burstable|Workload|Confidence/i),
        page.getByRole('alert'),
        page.locator('table'),
      ],
      { message: 'Right-sizing tab did not render' }
    );
  });
});
