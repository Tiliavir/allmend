import { type ChildProcess, execFileSync, spawn } from "node:child_process";
import { mkdtempSync, rmSync } from "node:fs";
import { tmpdir } from "node:os";
import { join } from "node:path";
import type { Page } from "@playwright/test";

export const adminEmail = "admin@example.org";
export const adminPassword = "e2e-password-0123";
const bin = ".cache/e2e/allmend";

/** Allmend runs the real binary on its own port and data directory. */
export class Allmend {
  readonly url: string;
  private readonly dir = mkdtempSync(join(tmpdir(), "allmend-e2e-"));
  private readonly hash = execFileSync(bin, ["hashpw"], { input: `${adminPassword}\n` }).toString().trim();
  private proc: ChildProcess | undefined;

  constructor(private readonly port: number) {
    this.url = `http://127.0.0.1:${port}`;
  }

  async start(): Promise<void> {
    if (this.proc && this.proc.exitCode === null) return;
    this.proc = spawn(bin, [], {
      stdio: ["ignore", "ignore", "inherit"],
      env: {
        ...process.env,
        ALLMEND_ADDR: `127.0.0.1:${this.port}`,
        ALLMEND_DATA_DIR: this.dir,
        ALLMEND_SECURE_COOKIE: "false",
        ALLMEND_ADMIN_EMAIL: adminEmail,
        ALLMEND_ADMIN_PASSWORD_HASH: this.hash,
      },
    });
    const p = this.proc;
    process.once("exit", () => p.kill("SIGKILL")); // never leave a server behind
    for (let i = 0; i < 100 && p.exitCode === null; i++) {
      try {
        if ((await fetch(`${this.url}/health`)).ok) return;
      } catch {
        // not up yet
      }
      await new Promise((r) => setTimeout(r, 100));
    }
    throw new Error("allmend did not start");
  }

  /** stop makes the server unreachable (connection refused), like a NAS that is off. */
  async stop(): Promise<void> {
    const p = this.proc;
    process.once("exit", () => p.kill("SIGKILL")); // never leave a server behind
    if (!p || p.exitCode !== null) return;
    const done = new Promise((r) => p.once("exit", r));
    p.kill("SIGTERM");
    await done;
  }

  async dispose(): Promise<void> {
    await this.stop();
    rmSync(this.dir, { recursive: true, force: true });
  }
}

/** login signs in as admin and waits until the service worker controls the page. */
export async function login(page: Page, app: Allmend): Promise<void> {
  await page.goto(`${app.url}/anmelden`);
  await page.getByLabel("E-Mail").fill(adminEmail);
  await page.getByLabel("Passwort").fill(adminPassword);
  await page.getByRole("button", { name: "Anmelden", exact: true }).click();
  await page.waitForURL(`${app.url}/`);
  await page.evaluate(async () => {
    await navigator.serviceWorker.ready;
  });
  await page.reload();
  await page.waitForFunction(() => navigator.serviceWorker.controller !== null);
}
