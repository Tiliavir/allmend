import { expect, test } from "@playwright/test";
import { Allmend, login } from "./allmend";

// ADR markmap: a ```markmap block becomes an interactive mind map, under the
// strict script CSP and without requests to other hosts.
const app = new Allmend(8193);

test.afterAll(async () => app.dispose());

const body = "```markmap\n# Ich\n\n## Arbeit\n\n- Helvetia\n  - seit 2018\n- ~~Colygon~~\n\n## Wohnen\n\n- Wollbach\n```\n";

test("a markmap block is drawn as a mind map", async ({ page }) => {
  await app.start();
  await login(page, app);
  const problems: string[] = [];
  page.on("pageerror", (e) => problems.push(e.message));
  page.on("console", (m) => m.type() === "error" && problems.push(m.text()));
  page.on("request", (r) => !r.url().startsWith(app.url) && problems.push(`external request ${r.url()}`));

  await page.goto(`${app.url}/seiten/neu`);
  await page.getByRole("textbox", { name: "Titel", exact: true }).fill("Ich");
  await page.locator("textarea[name=body]").fill(body);
  await page.getByRole("button", { name: "Speichern" }).click();

  const map = page.locator("svg.markmap");
  await expect(map).toBeVisible();
  // three levels open: "seit 2018" (level 4) waits behind a click on Helvetia
  await expect(map.locator("g.markmap-node")).toHaveCount(6);
  await expect(map.locator("del", { hasText: "Colygon" })).toBeVisible();
  await expect(page.locator("pre.markmap")).toHaveCount(0);
  expect(problems).toEqual([]);
});
