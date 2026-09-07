import { expect, test, type Page } from "@playwright/test";

async function authenticateDevelopment(page: Page) {
  await page.addInitScript(() => sessionStorage.setItem("lens-token", "browser-test"));
  await page.route("**/v1/auth/config", async (route) => route.fulfill({ json: {
    mode: "development", enabled: false, development_bootstrap: true, exposure_enabled: true,
    self_serve_enabled: true, connectors: { endpoint: true, aws: false, azure: false, gcp: false, github: false, kubernetes: false },
  }}));
  await page.route("**/v1/notifications", async (route) => route.fulfill({ json: { items: [] } }));
}

test("delegated endpoint setup selects a platform before issuing a command", async ({ page }) => {
  await page.route("**/v1/public/endpoint-handoffs/resolve", async (route) => route.fulfill({ json: {
    workspace_name: "Acme Security", environment_name: "Finance laptop", platform: "linux",
    command: "npx --yes barrikade-lens@2.0.0 enroll one-use --hub https://lens.example --install",
    expires_at: new Date(Date.now() + 900_000).toISOString(), prerequisites: ["Node.js 18 or newer"], what_lens_reads: [], excluded: [],
  }}));
  await page.goto("/install#token=handoff-token");
  await expect(page).toHaveURL(/\/install$/);
  const generate = page.getByRole("button", { name: "Generate single-use command" });
  await expect(generate).toBeDisabled();
  await page.getByRole("button", { name: "Linux" }).click();
  await generate.click();
  await expect(page.getByRole("heading", { name: "Install for Finance laptop" })).toBeVisible();
  await expect(page.getByText(/barrikade-lens@2\.0\.0/)).toBeVisible();
});

test("Clerk sign-up is ready for a development tenant", async ({ page }) => {
  test.skip(!process.env.CLERK_E2E_BASE_URL, "Set CLERK_E2E_BASE_URL for the live Clerk acceptance gate");
  await page.goto(`${process.env.CLERK_E2E_BASE_URL}/#sign-up`);
  await expect(page.getByText("Create your account")).toBeVisible();
});

test("CISO overview makes an unassessed workspace actionable without navigation clutter", async ({ page }) => {
  await authenticateDevelopment(page);
  await page.route("**/v1/overview?*", async (route) => route.fulfill({ json: {
    window: "7d", generated_at: new Date().toISOString(), coverage: [],
    footprint: { system_types: {}, states: {}, surfaces: {} },
    attention: { unattributed_systems: 0, possible_only_systems: 0, non_loopback_services: 0, partial_scans: 0, stale_targets: 0, fact_conflicts: 0 },
    changes: [], data_quality: { confidence: {}, confidence_note: "No evidence yet", coverage_note: "Connect an endpoint" },
    executive_summary: { coverage_state: "unassessed", systems: { known: 0, fresh: 0, stale: 0, fresh_by_type: {}, stale_by_type: {} }, findings: { fresh: 0, stale: 0, fresh_by_severity: {}, stale_by_severity: {} }, effective_ownership: { owned: 0, unowned: 0, unassigned_high_priority_findings: 0 }, top_findings: [] },
  }}));
  await page.route("**/v1/systems?*", async (route) => route.fulfill({ json: { items: [], limit: 50 } }));
  await page.goto("/overview");
  await expect(page.getByRole("heading", { name: "Your organization is unassessed" })).toBeVisible();
  await expect(page.getByRole("button", { name: /Connect endpoint/ })).toBeVisible();
  const primary = page.locator(".main-nav button");
  await expect(primary).toHaveCount(4);
  await expect(primary).toHaveText([/Overview/, /Findings/, /Inventory/, /Connections/]);
  await page.getByRole("button", { name: "24h" }).click();
  await expect(page).toHaveURL(/\/overview\?window=24h$/);
  await page.reload();
  await expect(page.getByRole("button", { name: "24h" })).toHaveClass(/active/);
});

test("finding route, filters, reload, and accessible dialog state are durable", async ({ page }) => {
  await authenticateDevelopment(page);
  const finding = {
    id: "finding-1", root_entity_id: "system-1", root_name: "Production agent", rule_id: "public-agent", rule_version: "1",
    severity: "high", title: "Public agent endpoint", explanation: "A listener is reachable beyond the endpoint.",
    recommended_next_step: "Confirm the intended network boundary.", path: [{ entity_id: "system-1", name: "Production agent", kind: "agent", basis: "observed" }],
    evidence_bases: ["observed"], first_seen_at: new Date().toISOString(), last_seen_at: new Date().toISOString(),
    evidence_last_seen_at: new Date().toISOString(), evidence_freshness: "stale", effective_ownership: { owned: false },
  };
  await page.route(/\/v1\/exposures(?:\/[^?]+)?(?:\?.*)?$/, async (route) => {
    const url = new URL(route.request().url());
    await route.fulfill({ json: url.pathname.endsWith("/finding-1") ? finding : { items: [finding], limit: 50 } });
  });
  await page.goto("/findings?freshness=stale");
  const row = page.getByRole("button", { name: /Public agent endpoint/ });
  await row.click();
  await expect(page).toHaveURL(/\/findings\/finding-1\?freshness=stale$/);
  const dialog = page.getByRole("dialog", { name: "Finding details" });
  await expect(dialog).toBeFocused();
  await page.keyboard.press("Escape");
  await expect(page).toHaveURL(/\/findings\?freshness=stale$/);
  await expect(row).toBeFocused();
  await row.click();
  await page.reload();
  await expect(page.getByRole("dialog", { name: "Finding details" })).toContainText("Confirm the intended network boundary");
});
