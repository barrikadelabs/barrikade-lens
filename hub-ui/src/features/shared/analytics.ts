export function analyticsControl(key: string): string {
  return ({ owner_status: "ownership", system_type: "system_type", target_type: "target_type", window: "window", category: "change_category", surface: "surface" } as Record<string, string>)[key] ?? key;
}
