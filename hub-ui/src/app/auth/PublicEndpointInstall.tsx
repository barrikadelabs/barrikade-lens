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
      if (!response.ok) throw new Error(body?.error?.message || "This setup link is unavailable");
      setResult(body);
    } catch (reason) { setError(String(reason)); } finally { setBusy(false); }
  };
  return <main className="public-install"><header><Brand /><span>Delegated endpoint setup</span></header><section className="public-install-card"><p className="eyebrow">BARRIKADE LENS</p><h1>{result ? `Install for ${result.environment_name}` : "Choose this endpoint's platform"}</h1><p>{result ? `${result.workspace_name} has authorized a read-only Lens installation.` : "The command is generated only after you select a platform. It expires in 15 minutes and can be used once."}</p>{!result && <><div className="platform-picker" aria-label="Endpoint platform">{(["macos", "windows", "linux"] as const).map((value) => <button key={value} aria-pressed={platform === value} className={platform === value ? "active" : ""} onClick={() => setPlatform(value)}>{value === "macos" ? "macOS" : pretty(value)}</button>)}</div><button className="button primary full" disabled={!platform || busy || !token} onClick={generate}>{busy ? "Generating…" : "Generate single-use command"}</button></>}{result && <><div className="wizard-boundary"><ShieldCheck size={18} /><p><b>Read-only discovery</b><span>Lens inventories software, runtime state, network listeners, and configuration references. It excludes prompts, outputs, secret values, and file contents.</span></p></div><CopyBlock value={result.command} /><div className="setup-read"><div><h3>Requirements</h3>{result.prerequisites.map((item) => <span key={item}><CheckCircle2 size={14} />{item}</span>)}</div><div><h3>Credential</h3><span><CheckCircle2 size={14} />Single use</span><span><CheckCircle2 size={14} />Expires {new Date(result.expires_at).toLocaleTimeString()}</span></div></div></>}{error && <InlineError text={error} />}</section></main>;
}
