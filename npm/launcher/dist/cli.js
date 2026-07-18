#!/usr/bin/env node
import { spawnSync } from "node:child_process";
import { binaryPath } from "./platform.js";
try {
    const result = spawnSync(binaryPath(), process.argv.slice(2), {
        stdio: "inherit",
        windowsHide: false,
        env: process.env,
    });
    if (result.error)
        throw result.error;
    process.exitCode = result.status ?? 1;
}
catch (error) {
    const message = error instanceof Error ? error.message : String(error);
    process.stderr.write(`Barrikade Lens could not start: ${message}\n`);
    process.exitCode = 1;
}
//# sourceMappingURL=cli.js.map