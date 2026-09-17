import { describe, expect, it } from "vitest";
import { defaultEnvironmentName, environmentCatalog, sourceCategories, sourceEnabled } from "./connection-options";

describe("environment scan options", () => {
  it("keeps every source in one of the four onboarding categories", () => {
    expect(sourceCategories.map((category) => category.id)).toEqual(["code_ci", "employee_devices", "cloud_infrastructure", "saas_identity"]);
    expect(environmentCatalog.every((source) => sourceCategories.some((category) => category.id === source.category))).toBe(true);
  });

  it("reflects connector gates without hiding disabled sources", () => {
    const endpoint = environmentCatalog.find((source) => source.kind === "endpoint")!;
    const github = environmentCatalog.find((source) => source.kind === "github_repository")!;
    expect(sourceEnabled(endpoint, { endpoint: true, github: false })).toBe(true);
    expect(sourceEnabled(github, { endpoint: true, github: false })).toBe(false);
  });

  it("uses collector-supplied identity instead of requiring an endpoint name", () => {
    expect(defaultEnvironmentName("endpoint")).toBe("Employee device");
  });
});
