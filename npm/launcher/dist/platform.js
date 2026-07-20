import { createRequire } from "node:module";
import { existsSync } from "node:fs";
import { dirname, join } from "node:path";
import { fileURLToPath } from "node:url";
const require = createRequire(import.meta.url);
const supported = new Set([
    "darwin-arm64", "darwin-x64", "linux-arm64", "linux-x64", "win32-arm64", "win32-x64",
]);
export function platformPackage(platform = process.platform, architecture = process.arch) {
    const target = `${platform}-${architecture}`;
    if (!supported.has(target)) {
        throw new Error(`Barrikade Lens does not provide a native binary for ${target}.`);
    }
    return `@barrikade/lens-${target}`;
}
export function binaryPath(platform = process.platform, architecture = process.arch) {
    const packageName = platformPackage(platform, architecture);
    const executable = platform === "win32" ? "barrikade-lens.exe" : "barrikade-lens";
    let manifest;
    try {
        manifest = require.resolve(`${packageName}/package.json`);
    }
    catch {
        // Local source checkouts stage their development binary next to the
        // platform package. Published launchers continue to resolve only the
        // optional npm package because this sibling path does not exist there.
        const target = `${platform}-${architecture}`;
        const workspaceBinary = join(dirname(fileURLToPath(import.meta.url)), "..", "..", target, "bin", executable);
        if (existsSync(workspaceBinary))
            return workspaceBinary;
        throw new Error(`The optional native package ${packageName} is missing. Reinstall without disabling optional dependencies. ` +
            "Lens never downloads a binary during installation or startup.");
    }
    return join(dirname(manifest), "bin", executable);
}
//# sourceMappingURL=platform.js.map