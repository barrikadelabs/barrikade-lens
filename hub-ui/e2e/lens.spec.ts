import { expect, test, type Page } from "@playwright/test";

async function authenticateDevelopment(page: Page, options: { role?: "owner" | "admin" | "viewer"; connectors?: Record<string, boolean> } = {}) {
  const connectors = options.connectors ?? { endpoint: true, aws: false, azure: false, gcp: false, github: false, kubernetes: false };
  await page.addInitScript(() => sessionStorage.setItem("lens-token", "browser-test"));
  await page.route("**/v1/auth/config", async (route) => route.fulfill({ json: {
    mode: "development", enabled: false, development_bootstrap: true, exposure_enabled: true,
    self_serve_enabled: true, connectors,
  }}));
  await page.route("**/v1/session", async (route) => route.fulfill({ json: {
    user: { id: "browser-user" }, workspace: { id: "browser-workspace", name: "Browser test" }, role: options.role ?? "owner",
    permissions: [], needs_bootstrap: false, can_delete_account: true,
    analytics: { enabled: false, user_id: "browser-user", workspace_id: "browser-workspace" },
  }}));
  await page.route("**/v1/notifications", async (route) => route.fulfill({ json: { items: [] } }));
  await page.route("**/v1/products", async (route) => route.fulfill({ json: { items: [] } }));
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
  await page.goto("/overview");
  await expect(page.getByRole("heading", { name: "Let’s map your AI environment" })).toBeVisible();
  await expect(page.getByRole("button", { name: /Start scan/ })).toBeVisible();
  const primary = page.locator(".main-nav button");
  await expect(primary).toHaveCount(4);
  await expect(primary).toHaveText([/Overview/, /Findings/, /Inventory/, /Connections/]);
  await page.getByRole("button", { name: "24h" }).click();
  await expect(page).toHaveURL(/\/overview\?window=24h$/);
  await page.reload();
  await expect(page.getByRole("button", { name: "24h" })).toHaveClass(/active/);
});

test("environment-first onboarding is gated, keyboard accessible, responsive, and retryable", async ({ page }) => {
  await page.setViewportSize({ width: 390, height: 844 });
  await authenticateDevelopment(page, { connectors: { endpoint: true, aws: false, azure: false, gcp: false, github: false, kubernetes: false } });
  await page.route("**/v1/environments", async (route) => route.fulfill({ json: { items: [] } }));
  await page.route("**/v1/overview?*", async (route) => route.fulfill({ json: {
    window: "7d", generated_at: new Date().toISOString(), coverage: [],
    footprint: { system_types: {}, states: {}, surfaces: {} }, attention: {}, changes: [],
    data_quality: { confidence: {}, confidence_note: "", coverage_note: "" },
  }}));
  await page.route("**/v1/targets?*", async (route) => route.fulfill({ json: { items: [], limit: 50 } }));
  const requests: Array<Record<string, unknown>> = [];
  let attempts = 0;
  await page.route("**/v1/environments/setup-sessions", async (route) => {
    attempts += 1;
    requests.push(JSON.parse(route.request().postData() || "{}") as Record<string, unknown>);
    if (attempts === 1) return route.fulfill({ status: 503, json: { error: { message: "Setup is temporarily unavailable" } } });
    return route.fulfill({ status: 201, json: {
      id: "setup-1", environment_id: "environment-1", kind: "endpoint", expires_at: new Date(Date.now() + 900_000).toISOString(), token_displayed_once: true,
      setup: { method: "managed_collector", commands: { macos: "install lens macos", windows: "install lens windows", linux: "install lens linux" }, what_lens_reads: [], excluded: [] },
    } });
  });

  await page.goto("/connections/new");
  const dialog = page.getByRole("dialog", { name: "Scan environment" });
  await expect(dialog).toBeVisible();
  await expect(dialog.getByRole("heading", { name: "Code & CI" })).toBeVisible();
  await expect(dialog.getByRole("heading", { name: "Employee devices" })).toBeVisible();
  await expect(dialog.getByRole("heading", { name: "Cloud & infrastructure" })).toBeVisible();
  await expect(dialog.getByRole("heading", { name: "SaaS & identity" })).toBeVisible();
  await expect(dialog.getByRole("button", { name: /GitHub.*Not enabled/ })).toBeDisabled();
  await expect(dialog.getByRole("button", { name: /AWS.*Not enabled/ })).toBeDisabled();

  const employeeDevices = dialog.getByRole("button", { name: /Employee devices.*Available/ });
  await employeeDevices.focus();
  await page.keyboard.press("Enter");
  await expect(dialog.getByRole("radio", { name: /Install on this computer/ })).toBeVisible();
  await expect(dialog.getByLabel("Installation platform")).toHaveCount(0);
  await expect(dialog.getByLabel("Display name")).toHaveCount(0);

  const installHere = dialog.getByRole("radio", { name: /Install on this computer/ });
  await installHere.focus();
  await page.keyboard.press("Enter");
  await expect(dialog.getByLabel("Installation platform")).toBeVisible();
  await dialog.getByRole("button", { name: "Start scan" }).click();
  await expect(dialog.getByText("Setup is temporarily unavailable")).toBeVisible();
  await dialog.getByRole("button", { name: "Start scan" }).click();
  await expect(dialog.getByText("install lens macos")).toBeVisible();
  expect(requests).toHaveLength(2);
  expect(requests[1]).toMatchObject({ kind: "endpoint", display_name: "Employee device", configuration: { deployment_method: "this_computer", platform: "macos" } });
  expect(requests[1]).not.toHaveProperty("external_id");
});

test("a resumable setup closes into live device status after enrollment", async ({ page }) => {
  await authenticateDevelopment(page);
  let connected = false;
  const environment = () => ({
    id: "environment-1", kind: "endpoint", provider: "endpoint", display_name: "Recovered device",
    connection_status: connected ? "connected" : "setup_pending", configuration: {}, schedule_enabled: true,
    source_id: connected ? "source-1" : undefined, created_at: new Date().toISOString(), updated_at: new Date().toISOString(),
  });
  await page.route("**/v1/environments", async (route) => route.fulfill({ json: { items: [environment()] } }));
  await page.route("**/v1/environments/environment-1/activation", async (route) => route.fulfill({ json: {
    environment_id: "environment-1", phase: connected ? "connected" : "awaiting_install", connection_status: connected ? "connected" : "setup_pending",
  } }));
  await page.route("**/v1/environments/environment-1/enrollment-credentials", async (route) => route.fulfill({ status: 201, json: {
    id: "setup-resumed", environment_id: "environment-1", kind: "endpoint", expires_at: new Date(Date.now() + 900_000).toISOString(), token_displayed_once: true,
    setup: { method: "managed_collector", commands: { macos: "install resumed macos", windows: "install resumed windows", linux: "install resumed linux" }, what_lens_reads: [], excluded: [] },
  } }));
  await page.route("**/v1/environments/environment-1", async (route) => {
    connected = true;
    await route.fulfill({ json: environment() });
  });
  await page.route("**/v1/overview?*", async (route) => route.fulfill({ json: { window: "7d", generated_at: new Date().toISOString(), coverage: [], footprint: { system_types: {}, states: {}, surfaces: {} }, attention: {}, changes: [], data_quality: { confidence: {}, confidence_note: "", coverage_note: "" } } }));
  await page.route("**/v1/targets?*", async (route) => route.fulfill({ json: { items: [], limit: 50 } }));

  await page.goto("/connections/environment-1");
  await expect(page).toHaveURL(/\/connections$/);
  await expect(page.getByRole("dialog", { name: "Scan environment" })).toHaveCount(0);
  await expect(page.getByRole("heading", { name: "1 connected sources" })).toBeVisible();
});

test("viewers can inspect coverage but cannot start an environment scan", async ({ page }) => {
  await authenticateDevelopment(page, { role: "viewer" });
  await page.route("**/v1/environments", async (route) => route.fulfill({ json: { items: [] } }));
  await page.route("**/v1/overview?*", async (route) => route.fulfill({ json: { window: "7d", generated_at: new Date().toISOString(), coverage: [], footprint: { system_types: {}, states: {}, surfaces: {} }, attention: {}, changes: [], data_quality: { confidence: {}, confidence_note: "", coverage_note: "" } } }));
  await page.route("**/v1/targets?*", async (route) => route.fulfill({ json: { items: [], limit: 50 } }));
  await page.goto("/connections/new");
  await expect(page).toHaveURL(/\/connections$/);
  await expect(page.getByText("Ask a workspace owner or admin to start an environment scan.")).toBeVisible();
  await expect(page.getByRole("button", { name: "Start scan" })).toHaveCount(0);
  await expect(page.getByRole("button", { name: "Scan environment" })).toHaveCount(0);
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
