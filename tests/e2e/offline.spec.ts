import { expect, test } from "@playwright/test";
import { Allmend, login } from "./allmend";

// ADR offline: cached pages while the server is unreachable, an outbox for
// the three offline forms, a visible error everywhere else.
const app = new Allmend(8191);

test.describe.configure({ mode: "serial" });
test.afterAll(async () => app.dispose());

test.beforeEach(async ({ page }) => {
  await app.start(); // a previous test may have stopped it
  await login(page, app);
});

const outboxLink = (page: import("@playwright/test").Page) => page.locator("[data-outbox]");

test("pages opened before are readable offline, others say so", async ({ page }) => {
  await page.goto(`${app.url}/velo`);
  await app.stop();

  await page.goto(`${app.url}/velo`);
  await expect(page.getByRole("status")).toContainText("Offline – Stand vom");
  await expect(page.getByRole("heading", { level: 1 })).toBeVisible();

  await page.goto(`${app.url}/finanzen`);
  await expect(page.getByRole("heading", { name: "Offline nicht verfügbar" })).toBeVisible();

  await page.goto(`${app.url}/schiffe`);
  await expect(page.getByText("Schiffe versenken braucht eine Verbindung.")).toBeVisible();
});

test("a tour entered offline is transferred once the server is back", async ({ page }) => {
  await page.goto(`${app.url}/velo/touren/neu`);
  await app.stop();

  await page.getByLabel("Datum").fill("2026-11-12");
  await page.getByLabel("Distanz (km)").fill("31.5");
  await page.getByLabel("Titel").fill("Offline-Runde");
  await page.getByRole("button", { name: "Speichern" }).click();
  await expect(page.getByRole("heading", { name: "Offline gespeichert" })).toBeVisible();
  await expect(outboxLink(page)).toHaveText("1 nicht übertragen");

  await app.start();
  await page.goto(`${app.url}/velo/touren?jahr=2026`);
  await expect(outboxLink(page)).toBeHidden();
  await page.reload();
  await expect(page.getByText("Offline-Runde")).toBeVisible();
});

test("a form that needs a connection is not kept offline", async ({ page }) => {
  await page.goto(`${app.url}/velo/velos/neu`);
  await app.stop();
  await page.getByLabel("Name").fill("Rennvelo");
  await page.getByRole("button", { name: "Speichern" }).click();
  await expect(page.getByRole("heading", { name: "Offline – Änderung nicht möglich" })).toBeVisible();
  await expect(outboxLink(page)).toBeHidden();
});

test("a stale offline change is rejected, shown and can be corrected", async ({ page }) => {
  const year = {
    year: "2025",
    car_dist_km: "7.6",
    train_dist_km: "15",
    bike_dist_km: "18",
  };
  expect((await page.request.post(`${app.url}/arbeitswege`, { form: year, maxRedirects: 0 })).status()).toBe(303);
  await page.goto(`${app.url}/arbeitswege/2025/bearbeiten`);
  await app.stop();
  await page.getByLabel("Arbeitstage Jan").fill("20");
  await page.getByRole("button", { name: "Speichern" }).click();
  await expect(page.getByRole("heading", { name: "Offline gespeichert" })).toBeVisible();

  // Meanwhile someone else saves the year (version 1 → 2).
  await app.start();
  const other = await page.request.post(`${app.url}/arbeitswege/2025`, { form: { ...year, base: "1", workdays_1: "18" }, maxRedirects: 0 });
  expect(other.status()).toBe(303);

  await page.goto(`${app.url}/offline`);
  const item = page.locator(".outbox-item");
  await expect(item).toContainText("Arbeitswege 2025");
  await expect(item).toContainText("Vom Server abgelehnt (409)");
  await expect(outboxLink(page)).toHaveText("1 nicht übertragen (1 abgelehnt)");

  await item.getByRole("link", { name: "Antwort ansehen" }).click();
  await expect(page.getByRole("alert")).toContainText("inzwischen anderswo geändert");
  await page.getByRole("button", { name: "Speichern" }).click();
  await page.waitForURL(/\/arbeitswege\?jahr=2025/);

  await page.goto(`${app.url}/offline`);
  await expect(page.getByText("Alles übertragen.")).toBeVisible();
  await expect(outboxLink(page)).toBeHidden();
});

test("logout warns while entries are not transferred", async ({ page }) => {
  await page.goto(`${app.url}/velo/touren/neu`);
  await app.stop();
  await page.getByLabel("Datum").fill("2026-11-13");
  await page.getByLabel("Distanz (km)").fill("12");
  await page.getByRole("button", { name: "Speichern" }).click();
  await expect(outboxLink(page)).toHaveText("1 nicht übertragen");

  await page.locator(".user summary").click();
  await page.getByRole("button", { name: "Abmelden" }).click();
  await expect(page.getByRole("alert")).toContainText("gehen beim Abmelden verloren");

  // Discarding empties the outbox.
  await page.goto(`${app.url}/offline`);
  await page.getByRole("button", { name: "Verwerfen" }).click();
  await expect(page.getByText("Alles übertragen.")).toBeVisible();
});

test("logging out removes the offline copies", async ({ page }) => {
  await page.goto(`${app.url}/velo`);
  await page.locator(".user summary").click();
  await page.getByRole("button", { name: "Abmelden" }).click();
  await page.waitForURL(`${app.url}/`);
  expect(await page.evaluate(async () => (await caches.keys()).length)).toBe(0);
  await app.stop();
  await expect(page.goto(`${app.url}/velo`)).rejects.toThrow(/ERR_CONNECTION_REFUSED/);
});
