import { useEffect, useState } from "react";
import { CheckCircle2, ShieldCheck } from "lucide-react";
import { Brand, CopyBlock, InlineError, pretty } from "../../ui";

export function PublicEndpointInstall() {
  const [token] = useState(() => decodeURIComponent(location.hash.replace(/^#token=/, "")));
  const [platform, setPlatform] = useState<"macos" | "windows" | "linux" | "">("");
  const [result, setResult] = useState<{ workspace_name: string; environment_name: string; command: string; expires_at: string; prerequisites: string[]; what_lens_reads: string[]; excluded: string[] }>();
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState("");
  useEffect(() => { history.replaceState({}, "", "/install"); }, []);
  const generate = async () => {
    if (!token || !platform) return;
    setBusy(true); setError("");
    try {
      const response = await fetch("/v1/public/endpoint-handoffs/resolve", { method: "POST", headers: { "Content-Type": "application/json" }, body: JSON.stringify({ token, platform }) });
      const body = await response.json().catch(() => ({}));
      if (!response.ok) throw new Error(body?.error?.message || "This installation link is no longer available");
      setResult(body);
    } catch (reason) { setError(String(reason)); } finally { setBusy(false); }
  };
  return <main className="public-install"><header><Brand /><span>Install Lens on a device</span></header><section className="public-install-card"><p className="eyebrow">BARRIKADE LENS</p><h1>{result ? `Install Lens on ${result.environment_name}` : "Choose this device’s platform"}</h1><p>{result ? `${result.workspace_name} approved this read-only Lens installation.` : "Choose a platform to create a command. The command works once and expires after 15 minutes."}</p>{!result && <><div className="platform-picker" aria-label="Device platform">{(["macos", "windows", "linux"] as const).map((value) => <button key={value} aria-pressed={platform === value} className={platform === value ? "active" : ""} onClick={() => setPlatform(value)}>{value === "macos" ? "macOS" : pretty(value)}</button>)}</div><button className="button primary full" disabled={!platform || busy || !token} onClick={generate}>{busy ? "Creating…" : "Create single-use command"}</button></>}{result && <><div className="wizard-boundary"><ShieldCheck size={18} /><p><b>Read-only by design</b><span>Lens checks installed software, whether it is running, network access, and relevant configuration names. It does not read prompts, outputs, secret values, or file contents.</span></p></div><CopyBlock value={result.command} /><div className="setup-read"><div><h3>Requirements</h3>{result.prerequisites.map((item) => <span key={item}><CheckCircle2 size={14} />{item}</span>)}</div><div><h3>Command</h3><span><CheckCircle2 size={14} />Works once</span><span><CheckCircle2 size={14} />Expires {new Date(result.expires_at).toLocaleTimeString()}</span></div></div></>}{error && <InlineError text={error} />}</section></main>;
}
