import { expect, test } from "@playwright/test";
import { Allmend, login } from "./allmend";

// ADR orte: trips from KML, the location history from Timeline.json, the
// heatmap with cells from the server.
const app = new Allmend(8192);

test.afterAll(async () => app.dispose());

const kml = `<kml><Document><name>Rhein</name><TimeSpan><begin>2024-05-01</begin><end>2024-05-04</end></TimeSpan>
<Placemark><name>Basel</name><Point><coordinates>7.5886,47.5596</coordinates></Point></Placemark>
<Placemark><name>Weg</name><LineString><coordinates>7.58,47.55 7.60,47.57 7.62,47.58</coordinates></LineString></Placemark></Document></kml>`;

const timeline = JSON.stringify({
  semanticSegments: [
    {
      startTime: "2024-05-03T10:00:00.000+02:00",
      endTime: "2024-05-03T10:20:00.000+02:00",
      timelinePath: [
        { point: "47.5500°, 7.5800°", time: "2024-05-03T10:00:00.000+02:00" },
        { point: "47.5600°, 7.6000°", time: "2024-05-03T10:20:00.000+02:00" },
      ],
    },
    {
      startTime: "2024-05-03T10:25:00.000+02:00",
      endTime: "2024-05-03T12:00:00.000+02:00",
      visit: { hierarchyLevel: 0, topCandidate: { semanticType: "WORK", placeLocation: { latLng: "47.5600°, 7.6000°" } } },
    },
  ],
});

test("import a trip and the history, see them on the maps", async ({ page }) => {
  await app.start();
  await login(page, app);
  const errors: string[] = [];
  page.on("pageerror", (e) => errors.push(e.message));

  await page.goto(`${app.url}/orte`);
  await page.getByText("Reise importieren (KML/KMZ)").click();
  await page.getByLabel("Datei").setInputFiles({ name: "rhein.kml", mimeType: "application/vnd.google-earth.kml+xml", buffer: Buffer.from(kml) });
  await page.getByRole("button", { name: "Importieren" }).click();
  await expect(page.getByRole("status")).toContainText("Reise „Rhein“ mit 1 Orten importiert.");
  await expect(page.locator("#karte path.leaflet-interactive")).toHaveCount(2); // track + place

  await page.goto(`${app.url}/orte/verlauf`);
  await page.getByText("Timeline.json importieren").click();
  await page.getByLabel("Datei").setInputFiles({ name: "Timeline.json", mimeType: "application/json", buffer: Buffer.from(timeline) });
  await page.getByRole("button", { name: "Importieren" }).click();
  await expect(page.getByRole("status")).toContainText("2 neue Punkte und 1 Aufenthalte übernommen.");
  await expect(page.getByText("Arbeit", { exact: true })).toBeVisible();
  await expect(page.locator("#karte path.leaflet-interactive")).toHaveCount(2); // path + stay

  const cells = page.waitForResponse((r) => r.url().includes("/orte/karte/zellen") && r.ok());
  await page.goto(`${app.url}/orte/karte`);
  await cells;
  await expect(page.locator("#karte canvas.leaflet-heatmap-layer")).toHaveCount(1);
  expect(errors).toEqual([]);
});
