#!/usr/bin/env node

import { chmodSync, lstatSync, mkdirSync, rmSync, symlinkSync, writeFileSync } from "node:fs";
import { spawnSync } from "node:child_process";
import { dirname, join, relative } from "node:path";
import { fileURLToPath } from "node:url";

const root = dirname(dirname(fileURLToPath(import.meta.url)));
const target = `${process.platform}-${process.arch}`;
const supported = new Set([
  "darwin-arm64", "darwin-x64", "linux-arm64", "linux-x64", "win32-arm64", "win32-x64",
]);

if (!supported.has(target)) {
  throw new Error(`Barrikade Lens does not provide a native binary for ${target}.`);
}

function run(command, args, cwd = root) {
  const result = spawnSync(command, args, { cwd, stdio: "inherit", shell: process.platform === "win32" });
  if (result.error) throw result.error;
  if (result.status !== 0) throw new Error(`${command} exited with status ${result.status}`);
}

const packageRoot = join(root, "npm", target);
const binaryName = process.platform === "win32" ? "barrikade-lens.exe" : "barrikade-lens";
const binary = join(packageRoot, "bin", binaryName);
mkdirSync(dirname(binary), { recursive: true });
run("go", ["build", "-trimpath", "-ldflags=-s -w -X main.version=2.0.0-dev", "-o", binary, "./cmd/barrikade-lens"]);
if (process.platform !== "win32") chmodSync(binary, 0o755);

run("npm", ["run", "build", "-w", "barrikade-lens"]);

const localBinDirectory = join(root, "node_modules", ".bin");
mkdirSync(localBinDirectory, { recursive: true });
if (process.platform === "win32") {
  const command = join(localBinDirectory, "barrikade-lens.cmd");
  writeFileSync(command, `@ECHO OFF\r\nnode "%~dp0\\..\\..\\npm\\launcher\\dist\\cli.js" %*\r\n`);
} else {
  const command = join(localBinDirectory, "barrikade-lens");
  try {
    lstatSync(command);
    rmSync(command);
  } catch (error) {
    if (error?.code !== "ENOENT") throw error;
  }
  symlinkSync(relative(localBinDirectory, join(root, "npm", "launcher", "dist", "cli.js")), command);
}

run("npm", ["link"], join(root, "npm", "launcher"));
process.stdout.write(`Barrikade Lens ${target} development command installed.\n`);
