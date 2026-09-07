import { fireEvent, render, screen, waitFor } from "@testing-library/react";
import { afterEach, describe, expect, it, vi } from "vitest";
import { App } from "./App";

describe("delegated endpoint setup", () => {
  afterEach(() => { history.replaceState({}, "", "/"); vi.restoreAllMocks(); });

  it("keeps the token out of the address bar and requires an explicit platform", async () => {
    history.replaceState({}, "", "/install#token=secret-handoff");
    const fetchMock = vi.spyOn(globalThis, "fetch").mockResolvedValue(new Response(JSON.stringify({
      workspace_name: "Acme", environment_name: "CISO laptop", command: "npx barrikade-lens@2.0.6 enroll token", expires_at: new Date(Date.now() + 600_000).toISOString(), prerequisites: ["Node.js 18+"], what_lens_reads: [], excluded: [],
    }), { status: 200, headers: { "Content-Type": "application/json" } }));
    render(<App />);
    expect(location.hash).toBe("");
    const generate = screen.getByRole("button", { name: /generate single-use command/i });
    expect(generate).toBeDisabled();
    fireEvent.click(screen.getByRole("button", { name: "Linux" }));
    fireEvent.click(generate);
    await screen.findByText("Install for CISO laptop");
    expect(fetchMock).toHaveBeenCalledWith("/v1/public/endpoint-handoffs/resolve", expect.objectContaining({ method: "POST", body: JSON.stringify({ token: "secret-handoff", platform: "linux" }) }));
  });
});
