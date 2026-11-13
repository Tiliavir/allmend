import { execFileSync } from "node:child_process";
import { mkdirSync } from "node:fs";

// Builds the binary once; every spec starts its own server with it.
export default function globalSetup() {
  mkdirSync(".cache/e2e", { recursive: true });
  execFileSync("go", ["build", "-o", ".cache/e2e/allmend", "./cmd/allmend"], { stdio: "inherit", env: { ...process.env, CGO_ENABLED: "0" } });
}
