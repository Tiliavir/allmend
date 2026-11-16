import { expect, test } from "@playwright/test";
import { Allmend, login } from "./allmend";

// ADR markmap: a ```markmap block is drawn by Markmap 1.x, which reads its
// own front matter and magic comments, under the strict script CSP and
// without requests to other hosts. Unusable options are listed under the map.
const app = new Allmend(8193);

test.afterAll(async () => app.dispose());

const body = [
  "```markmap",
  "---",
  "markmap:",
  "  theme: cards",
  "  foldLevel: 2",
  "  zoom: viel",
  "---",
  "# Ich",
  "## Arbeit",
  "- Helvetia",
  "  - seit 2018",
  "- ~~Colygon~~",
  "## Wohnen <!-- markmap: fold -->",
  "- Wollbach",
  "```",
  "",
].join("\n");

test("a markmap block is drawn with its options and magic comments", async ({ page }) => {
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
  await expect(page.locator("pre.markmap")).toHaveCount(0);
  // foldLevel 2: Ich, Arbeit, Helvetia, Colygon, Wohnen; "seit 2018" folded
  // by the level, "Wollbach" by the magic comment on Wohnen
  await expect(map.getByText("Helvetia")).toBeVisible();
  await expect(map.getByText("Wohnen")).toBeVisible();
  await expect(map.getByText("seit 2018")).toHaveCount(0);
  await expect(map.getByText("Wollbach")).toHaveCount(0);
  await expect(page.locator(".markmap-warnings li")).toHaveText(["Markmap: `zoom` must be true or false"]);
  expect(problems).toEqual([]);
});
