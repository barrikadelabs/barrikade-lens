import assert from "node:assert/strict";
import test from "node:test";
import { platformPackage } from "./platform.js";

test("selects every release target deterministically", () => {
  assert.equal(platformPackage("darwin", "arm64"), "@barrikade-lens/darwin-arm64");
  assert.equal(platformPackage("linux", "x64"), "@barrikade-lens/linux-x64");
  assert.equal(platformPackage("win32", "x64"), "@barrikade-lens/win32-x64");
});

test("fails clearly on unsupported targets", () => {
  assert.throws(() => platformPackage("freebsd", "x64"), /does not provide/);
});
